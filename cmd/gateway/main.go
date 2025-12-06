package main

import (
	"context"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"github.com/matin1999/webcli-go/internal/server"
	"github.com/matin1999/webcli-go/pkg/env"
	"strings"
	"syscall"

	"github.com/dchest/captcha"
)

func main() {
	envs := env.ReadEnvs()
	gottyAddr := "127.0.0.1:" + envs.GottyPortInternal

	args := []string{
		"-w", "--pass-headers",
		"-a", "127.0.0.1",
		"-p", envs.GottyPortInternal,
		"--title-format", "go-webcli",
		envs.WebcliBin, "bash", "--l",
	}
	gottyCmd := exec.Command("gotty", args...)
	gottyCmd.Stdout = os.Stdout
	gottyCmd.Stderr = os.Stderr
	if err := gottyCmd.Start(); err != nil {
		log.Fatalf("failed to start gotty: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		_ = gottyCmd.Process.Signal(syscall.SIGTERM)
		_ = gottyCmd.Wait()
	}()

	s := server.Init(envs)

	mux := http.NewServeMux()

	mux.Handle("/captcha/", captcha.Server(captcha.StdWidth, captcha.StdHeight))
	mux.HandleFunc("/login", s.LoginHandler)
	mux.HandleFunc("/logout", s.LogoutHandler)
	mux.Handle("/", s.RequireAuth(http.HandlerFunc(s.HomeHandler)))

	// reverse proxy /terminal/* -> gotty
	gottyURL, _ := url.Parse("http://" + gottyAddr)
	proxy := httputil.NewSingleHostReverseProxy(gottyURL)
	origDirector := proxy.Director
	proxy.Director = func(r *http.Request) {
		origDirector(r)
		r.URL.Path = strings.TrimPrefix(r.URL.Path, "/terminal")
		r.Header.Set("X-Forwarded-Proto", "http")
		r.Header.Set("X-Forwarded-Host", r.Host)
		u := s.Session.GetString(r.Context(), "user")
		if strings.TrimSpace(u) != "" {
			r.Header.Set("X-Forwarded-User", u)
		}
	}
	mux.Handle("/terminal", s.RequireAuth(http.RedirectHandler("/terminal/", http.StatusMovedPermanently)))
	mux.Handle("/terminal/", s.RequireAuth(http.StripPrefix("/terminal", proxy)))

	// serve pcaps/ (auth required)
	pcaps := http.FileServer(http.Dir("/app/pcaps"))
	mux.Handle("/pcaps/", s.RequireAuth(http.StripPrefix("/pcaps", pcaps)))

	port := envs.GatewayPort
	if err := http.ListenAndServe(port, s.Session.LoadAndSave(mux)); err != nil {
		log.Fatal(err)
	}
}
