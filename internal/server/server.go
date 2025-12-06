package server

import (
	"embed"
	"fmt"
	"html/template"
	"log"
	"net"
	"net/http"
	"os"
	"github.com/matin1999/webcli-go/internal/helpers"
	"github.com/matin1999/webcli-go/pkg/env"
	"github.com/matin1999/webcli-go/pkg/rate"
	"strings"
	"time"

	scs "github.com/alexedwards/scs/v2"
	"github.com/dchest/captcha"
	"golang.org/x/crypto/bcrypt"
)

//go:embed tmpl/*
var tmplFS embed.FS

type Server struct {
	Session     *scs.SessionManager
	RateLimiter *rate.Attempts
	userDB      map[string]string
	BlockFor    time.Duration
}

type loginViewData struct {
	ID    string
	Error string
}

func Init(envs *env.Envs) *Server {
	blockFor := time.Duration(envs.CaptchaBlockTime) * time.Minute
	attempts := rate.NewAttempts(
		envs.CaptchaRetryCount,
		blockFor,
		time.Duration(envs.CaptchaRetryTimeWindow)*time.Minute,
		5*time.Minute,
	)

	session := scs.New()
	session.Cookie.Name = "sid"
	session.IdleTimeout = time.Duration(envs.IdleTimeoutMinutes) * time.Minute
	session.Lifetime = time.Duration(envs.AbsSessionHours) * time.Hour
	session.Cookie.HttpOnly = true
	session.Cookie.SameSite = http.SameSiteLaxMode
	session.Cookie.Secure = false
	session.Cookie.Path = "/"

	if ents, err := tmplFS.ReadDir("tmpl"); err == nil {
		for _, e := range ents {
			log.Println("embedded template:", e.Name())
		}
	} else {
		log.Println("embed ReadDir error:", err)
	}

	return &Server{
		Session:     session,
		RateLimiter: attempts,
		userDB:      helpers.ParseUsers(os.Getenv("ADMIN_USERS")),
		BlockFor:    blockFor,
	}
}

func (s *Server) LoginHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		key := s.attemptKeyIP(r)
		if s.RateLimiter.IsBlocked(key) {
			ip := clientIP(r)
			log.Printf("login blocked (GET): ip=%s", ip)
			s.render(w, "tmpl/login.gohtml",
				loginViewData{
					ID:    captcha.New(),
					Error: s.blockMsg(),
				})
			return
		}
		s.render(w, "tmpl/login.gohtml", loginViewData{ID: captcha.New()})

	case http.MethodPost:
		user := r.FormValue("user")
		pass := r.FormValue("pass")
		id := r.FormValue("captcha_id")
		sol := r.FormValue("captcha_solution")

		ip := clientIP(r)
		key := s.attemptKeyIP(r)

		if s.RateLimiter.IsBlocked(key) {
			log.Printf("login blocked (POST): user=%q ip=%s", user, ip)
			s.render(w, "tmpl/login.gohtml",
				loginViewData{
					ID:    captcha.New(),
					Error: s.blockMsg(),
				})
			return
		}

		if !captcha.VerifyString(id, sol) {
			if s.RateLimiter.Fail(key) {
				log.Printf("captcha block triggered: user=%q ip=%s", user, ip)
				s.render(w, "tmpl/login.gohtml",
					loginViewData{
						ID:    captcha.New(),
						Error: s.blockMsg(),
					})
				return
			}
			s.render(w, "tmpl/login.gohtml",
				loginViewData{ID: captcha.New(), Error: "Captcha failed"})
			return
		}

		s.RateLimiter.Success(key)

		if !s.checkUser(user, pass) {
			s.render(w, "tmpl/login.gohtml", loginViewData{ID: captcha.New(), Error: "Invalid credentials"})
			return
		}

		if err := s.Session.RenewToken(r.Context()); err != nil {
			http.Error(w, "renew failed", http.StatusInternalServerError)
			return
		}
		s.Session.Put(r.Context(), "user", user)
		s.Session.Put(r.Context(), "issued_at", time.Now().Unix())

		log.Printf("login success: user=%q ip=%s", user, ip)
		http.Redirect(w, r, "/", http.StatusSeeOther)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) LogoutHandler(w http.ResponseWriter, r *http.Request) {
	_ = s.Session.Destroy(r.Context())
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) HomeHandler(w http.ResponseWriter, r *http.Request) {
	user := s.Session.GetString(r.Context(), "user")
	data := struct {
		User string
		Idle time.Duration
	}{user, s.Session.IdleTimeout}

	s.renderInline(w,
		`<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">`+
			`<title>WebCLI</title><style>body{font-family:system-ui,-apple-system,Segoe UI,Roboto,Ubuntu,Helvetica,Arial,sans-serif;max-width:720px;margin:5vh auto;padding:24px}</style>`+
			`<p>Session idle timeout: {{.Idle}}</p>`+
			`<p><a href="/terminal/">Open Terminal</a> · <a href="/logout">Logout</a></p>`,
		data,
	)
}

func (s *Server) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.Session.GetString(r.Context(), "user") == "" {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ---------- helpers ----------

func (s *Server) blockMsg() string {
	mins := int(s.BlockFor.Minutes())
	if mins <= 0 {
		mins = 1
	}
	return fmt.Sprintf("Too many failed captcha attempts. Try again in %d minute(s).", mins)
}

func (s *Server) checkUser(user, pass string) bool {
	if len(s.userDB) == 0 {
		return false
	}
	if stored, ok := s.userDB[user]; ok {
		if strings.HasPrefix(stored, "$2") {
			return bcrypt.CompareHashAndPassword([]byte(stored), []byte(pass)) == nil
		}
		return pass == stored
	}
	return false
}

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	t, err := template.ParseFS(tmplFS, name)
	if err != nil {
		if t2, err2 := template.ParseFiles(name); err2 == nil {
			_ = t2.Execute(w, data)
			return
		}
		http.Error(w, err.Error(), 500)
		return
	}
	_ = t.Execute(w, data)
}

func (s *Server) renderInline(w http.ResponseWriter, src string, data any) {
	t := template.Must(template.New("inline").Parse(src))
	_ = t.Execute(w, data)
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if idx := strings.IndexByte(xff, ','); idx > 0 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}
	if xr := r.Header.Get("X-Real-IP"); xr != "" {
		return strings.TrimSpace(xr)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

func (s *Server) attemptKeyIP(r *http.Request) string {
	return rate.KeyFrom(clientIP(r), "")
}
