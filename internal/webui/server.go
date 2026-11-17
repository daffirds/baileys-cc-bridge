// Package webui serves the panel: the Vite app in web/ (built into dist/ and embedded) plus a JSON
// API, on 127.0.0.1 inside `run`. API calls need the token from the panel's link, and requests from
// other hosts or origins are refused, so other sites open in the browser can't use it.
package webui

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/daffirds/baileys-cc-bridge/internal/app"
	"github.com/daffirds/baileys-cc-bridge/internal/whatsapp"
)

//go:embed all:dist
var dist embed.FS

// Account is the running bridge's WhatsApp side, as the panel sees it.
type Account interface {
	Status() Status
	Groups(ctx context.Context) ([]whatsapp.Group, error)
	// Logout unlinks the number and starts linking again. It returns at once; Status follows.
	Logout()
}

// Status is where the WhatsApp side stands.
type Status struct {
	State      string       `json:"state"` // starting | linking | connected | logging_out
	QR         string       `json:"-"`     // the code to scan while linking
	Me         *whatsapp.Me `json:"me,omitempty"`
	ConfigPath string       `json:"-"`               // the linked number's config, while connected
	Error      string       `json:"error,omitempty"` // why the last connection failed
}

type Options struct {
	Addr    string // host:port; the host must be loopback
	Account Account
	Log     *slog.Logger
}

type server struct {
	token   string
	account Account
	log     *slog.Logger
	models  modelCache
}

// LinkFile holds the running panel's link, for `baileys-cc-bridge ui`.
func LinkFile() string { return filepath.Join(app.Home(), "panel.url") }

// Start serves the panel until ctx ends and returns its link, which it also writes to LinkFile.
func Start(ctx context.Context, o Options) (string, error) {
	host, _, err := net.SplitHostPort(o.Addr)
	if err != nil {
		return "", fmt.Errorf("-addr: %w", err)
	}
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return "", errors.New("-addr must be a loopback address (127.0.0.1 or localhost)")
	}
	token, err := loadToken()
	if err != nil {
		return "", err
	}
	ln, err := net.Listen("tcp", o.Addr)
	if err != nil {
		// The port is taken: any free one will do; `ui` finds it in LinkFile.
		if ln, err = net.Listen("tcp", net.JoinHostPort(host, "0")); err != nil {
			return "", err
		}
	}
	s := &server{token: token, account: o.Account, log: o.Log}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/status", s.status)
	mux.HandleFunc("GET /api/state", s.state)
	mux.HandleFunc("PUT /api/config", s.saveConfig)
	mux.HandleFunc("GET /api/groups", s.groups)
	mux.HandleFunc("GET /api/people", s.people)
	mux.HandleFunc("GET /api/models", s.listModels)
	mux.HandleFunc("POST /api/logout", s.logout)
	mux.HandleFunc("POST /api/pick-dir", s.pickDir)
	mux.HandleFunc("POST /api/check-dir", s.checkDir)
	sub, _ := fs.Sub(dist, "dist")
	files := http.FileServerFS(sub)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		files.ServeHTTP(w, r)
	})
	srv := &http.Server{Handler: s.guard(mux), ReadHeaderTimeout: 10 * time.Second}
	link := "http://" + ln.Addr().String() + "/#t=" + token
	if err := os.WriteFile(LinkFile(), []byte(link+"\n"), 0o600); err != nil {
		_ = ln.Close()
		return "", err
	}
	go func() {
		<-ctx.Done()
		_ = os.Remove(LinkFile())
		sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			o.Log.Error("panel stopped", "err", err)
		}
	}()
	return link, nil
}

// loadToken returns the panel's token. It is made once and kept in panel.token, so a bookmarked
// link keeps working from one run to the next.
func loadToken() (string, error) {
	path := filepath.Join(app.Home(), "panel.token")
	if b, err := os.ReadFile(path); err == nil {
		if t := strings.TrimSpace(string(b)); len(t) >= 32 {
			return t, nil
		}
	}
	if err := os.MkdirAll(app.Home(), 0o700); err != nil {
		return "", err
	}
	t := randomHex(24)
	return t, os.WriteFile(path, []byte(t+"\n"), 0o600)
}

// guard refuses requests whose Host or Origin isn't this server (DNS rebinding, cross-site requests)
// and API calls without the token.
func (s *server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, _ := net.SplitHostPort(r.Host)
		if host == "" {
			host = r.Host
		}
		if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			if o := r.Header.Get("Origin"); o != "" && o != "http://"+r.Host {
				writeErr(w, http.StatusForbidden, errors.New("cross-origin request refused"))
				return
			}
			if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Bridge-Token")), []byte(s.token)) != 1 {
				writeErr(w, http.StatusUnauthorized, errors.New("open the panel with `baileys-cc-bridge ui`"))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

func readJSON(r *http.Request, v any) error {
	if err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20)).Decode(v); err != nil {
		return fmt.Errorf("bad request body: %w", err)
	}
	return nil
}
