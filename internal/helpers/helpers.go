package helpers

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"github.com/matin1999/webcli-go/internal/configs"
	"strconv"
	"strings"
	"time"
)

func RunLocal(cmd string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, cmd, args...)
	c.Env = []string{"PATH=/usr/bin:/bin", "LANG=C"}
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	return c.Run()
}

func GettingUsername() string {
	h := os.Getenv("HTTP_X_FORWARDED_USER")
	if h == "" {
		return ""
	}
	return h
}

func Help() {
	fmt.Printf(`
Allowed commands:
  help                           - show this help
  exit | quit                    - close session
  date                           - show server time
  df                             - disk usage (df -h)
  ping <host> [-c N]             - ping host (max count 5)

Service:
	service                            -getting allowed commands
  
GroupVars helpers (group_vars/*.yaml):
	gvars                          - list available commands in groupsvars 

Vars helpers (top-level vars.yaml):
	vars                               - list available commands for vars

PCAP (packet capture helpers):
	pcap                               - list available commands in pcap 

Snapshots (ansible project backup / restore):
	snapshot                           - list available commands in snapshots 
`)
}

func KeysCSV(m map[string]string) string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return strings.Join(out, ", ")
}

/* ========= tiny helpers ========= */

func ToBool(s string) bool { _, ok := configs.BoolTrue[strings.ToLower(s)]; return ok }

func ParseArgs(args []string) (map[string]string, []string) {
	kvs := map[string]string{}
	var rest []string
	for _, a := range args {
		if i := strings.IndexByte(a, '='); i > 0 {
			k := strings.TrimSpace(a[:i])
			v := strings.TrimSpace(a[i+1:])
			if k != "" {
				kvs[k] = v
				continue
			}
		}
		rest = append(rest, a)
	}
	return kvs, rest
}

func ParseTimeoutSeconds(s string, def time.Duration) (time.Duration, error) {
	if s == "" {
		return def, nil
	}
	secs, err := strconv.Atoi(s)
	if err != nil || secs < 1 || secs > 36000 {
		return 0, fmt.Errorf("invalid timeout (seconds 1..36000)")
	}
	return time.Duration(secs) * time.Second, nil
}

func MaskSecrets(cmd string) string {
	// mask pass=... and become_pass=...
	cmd = regexp.MustCompile(`(?i)(\bpass=)([^ \t]+)`).ReplaceAllString(cmd, "$1***")
	cmd = regexp.MustCompile(`(?i)(\bbecome_pass=)([^ \t]+)`).ReplaceAllString(cmd, "$1***")
	return cmd
}

func SplitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func HandlePing(args []string) {
	if len(args) < 1 || len(args) > 3 {
		fmt.Println("Usage: ping <host> [-c N]")
		return
	}
	host := args[0]
	if !configs.HostRe.MatchString(host) {
		fmt.Println("ERR: invalid host")
		return
	}
	count := "3"
	if len(args) == 3 {
		if args[1] != "-c" || !configs.NumRe.MatchString(args[2]) {
			fmt.Println("Usage: ping <host> [-c N]")
			return
		}
		if args[2] > "5" {
			count = "5"
		} else {
			count = args[2]
		}
	}
	if err := RunLocal("/bin/ping", "-n", "-c", count, host); err != nil {
		fmt.Println("ERR:", err)
	}
}

func ParseUsers(str string) map[string]string {
	m := map[string]string{}
	if strings.TrimSpace(str) == "" {
		return m
	}
	pairs := strings.Split(str, ",")
	for _, p := range pairs {
		kv := strings.SplitN(strings.TrimSpace(p), ":", 2)
		if len(kv) != 2 {
			continue
		}
		u := strings.TrimSpace(kv[0])
		pw := strings.TrimSpace(kv[1])
		if u != "" && pw != "" {
			m[u] = pw
		}
	}
	return m
}
