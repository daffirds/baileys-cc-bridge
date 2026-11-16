// Package webui serves the config UI: the Vite app in web/ (built into dist/ and embedded) plus a
// JSON API on 127.0.0.1. API calls need the random token from the printed link, and requests from
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
	"os/exec"
	"strings"
	"time"
)

//go:embed all:dist
var dist embed.FS

type Options struct {
	Addr string // host:port; the host must be loopback
	Open bool   // open the link in the default browser
	Log  *slog.Logger
}

type server struct {
	token string
	log   *slog.Logger
}

// Serve runs the UI until ctx is cancelled.
func Serve(ctx context.Context, o Options) error {
	host, _, err := net.SplitHostPort(o.Addr)
	if err != nil {
		return fmt.Errorf("-addr: %w", err)
	}
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return errors.New("-addr must be a loopback address (127.0.0.1 or localhost)")
	}
	ln, err := net.Listen("tcp", o.Addr)
	if err != nil {
		// The port is taken (another `ui`?): any free one will do.
		if ln, err = net.Listen("tcp", net.JoinHostPort(host, "0")); err != nil {
			return err
		}
	}
	s := &server{token: randomHex(24), log: o.Log}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/state", s.state)
	mux.HandleFunc("PUT /api/config", s.saveConfig)
	mux.HandleFunc("POST /api/groups", s.refreshGroups)
	mux.HandleFunc("POST /api/pick-dir", s.pickDir)
	mux.HandleFunc("POST /api/check-dir", s.checkDir)
	sub, _ := fs.Sub(dist, "dist")
	files := http.FileServerFS(sub)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		files.ServeHTTP(w, r)
	})
	srv := &http.Server{Handler: s.guard(mux), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()

	link := "http://" + ln.Addr().String() + "/#t=" + s.token
	fmt.Printf("Config UI: %s\nPress Ctrl-C to stop; the link only works while this runs.\n", link)
	if o.Open {
		_ = exec.Command("open", link).Start()
	}
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
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
				writeErr(w, http.StatusUnauthorized, errors.New("open the link printed by `bailey-cc-bridge ui`"))
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
