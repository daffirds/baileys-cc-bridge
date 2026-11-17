package webui

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/daffirds/bailey-cc-bridge/internal/app"
	"github.com/daffirds/bailey-cc-bridge/internal/config"
	"github.com/daffirds/bailey-cc-bridge/internal/whatsapp"
)

// state is everything the page needs: the config as written, the linked account and the groups
// last fetched from WhatsApp.
func (s *server) state(w http.ResponseWriter, _ *http.Request) {
	number, _ := whatsapp.Linked()
	cfg, path, me := &config.Config{}, "", ""
	if number != "" {
		var err error
		path, me = config.Path(number), number+"@s.whatsapp.net"
		if cfg, err = config.Read(path); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
	}
	if cfg.Chats == nil {
		cfg.Chats = []config.Chat{}
	}
	groups, updated := whatsapp.CachedGroups()
	if groups == nil {
		groups = []whatsapp.Group{}
	}
	when := ""
	if !updated.IsZero() {
		when = updated.Format(time.RFC3339)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"config_path":    path,
		"chats_dir":      filepath.Join(app.AccountDir(number), "chats"),
		"linked":         number,
		"me":             me,
		"config":         cfg,
		"groups":         groups,
		"groups_updated": when,
	})
}

func (s *server) saveConfig(w http.ResponseWriter, r *http.Request) {
	var c config.Config
	if err := readJSON(r, &c); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	number, ok := whatsapp.Linked()
	if !ok {
		writeErr(w, http.StatusConflict, errors.New("link WhatsApp first: `bailey-cc-bridge login`"))
		return
	}
	if err := config.Save(config.Path(number), &c); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// refreshGroups connects to WhatsApp for a fresh group list. While the bridge runs it holds the
// connection, and the cached list (fetched when it started) has to do.
func (s *server) refreshGroups(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	wa, _, err := whatsapp.Connect(ctx, whatsapp.Options{Log: s.log})
	if errors.Is(err, whatsapp.ErrBusy) {
		err = errors.New("the bridge is running and holds the WhatsApp connection; this list is from when it started")
	}
	if err != nil {
		writeErr(w, http.StatusConflict, err)
		return
	}
	defer wa.Close()
	groups, err := wa.Groups(ctx)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"groups": groups, "groups_updated": time.Now().Format(time.RFC3339)})
}

// pickDir shows the macOS folder chooser and returns the chosen path ("" when cancelled).
func (s *server) pickDir(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Start string `json:"start"`
	}
	_ = readJSON(r, &in)
	choose := `choose folder with prompt "Working directory for the agent"`
	if dir, err := config.ExpandHome(in.Start); err == nil && in.Start != "" {
		if st, err := os.Stat(dir); err == nil && st.IsDir() {
			esc := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(dir)
			choose += ` default location (POSIX file "` + esc + `")`
		}
	}
	// "activate" brings the dialog in front of the browser.
	out, err := exec.CommandContext(r.Context(), "osascript", "-e", "tell me to activate", "-e", "POSIX path of ("+choose+")").Output()
	if err != nil { // cancelled (error -128) or no GUI session
		writeJSON(w, http.StatusOK, map[string]string{"path": ""})
		return
	}
	path := strings.TrimSpace(string(out))
	if len(path) > 1 {
		path = strings.TrimSuffix(path, "/")
	}
	writeJSON(w, http.StatusOK, map[string]string{"path": path})
}

func (s *server) checkDir(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path string `json:"path"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	dir, err := config.ExpandHome(in.Path)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	st, err := os.Stat(dir)
	switch {
	case err != nil:
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "path": dir, "error": "does not exist"})
	case !st.IsDir():
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "path": dir, "error": "not a directory"})
	default:
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": dir})
	}
}
