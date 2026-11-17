package webui

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/skip2/go-qrcode"

	"github.com/daffirds/baileys-cc-bridge/internal/app"
	"github.com/daffirds/baileys-cc-bridge/internal/bridge"
	"github.com/daffirds/baileys-cc-bridge/internal/config"
	"github.com/daffirds/baileys-cc-bridge/internal/whatsapp"
)

var errNotConnected = errors.New("not connected to WhatsApp yet")

// status is what the panel polls: where the WhatsApp side stands, with the QR code to scan (as an
// image) while linking.
func (s *server) status(w http.ResponseWriter, _ *http.Request) {
	st := s.account.Status()
	out := struct {
		Status
		Number string `json:"number,omitempty"`
		QR     string `json:"qr,omitempty"`
	}{Status: st}
	if st.Me != nil {
		out.Number = user(st.Me.ID)
	}
	if st.QR != "" {
		if png, err := qrcode.Encode(st.QR, qrcode.Medium, 320); err == nil {
			out.QR = "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// connected returns the linked number and its config path, or fails while there is none.
func (s *server) connected() (number, path string, err error) {
	st := s.account.Status()
	if st.State != "connected" || st.Me == nil {
		return "", "", errNotConnected
	}
	return user(st.Me.ID), st.ConfigPath, nil
}

// state is what the panel edits: the linked number's config as written, and the built-in system
// prompt a chat starts from.
func (s *server) state(w http.ResponseWriter, _ *http.Request) {
	number, path, err := s.connected()
	if err != nil {
		writeErr(w, http.StatusConflict, err)
		return
	}
	cfg, err := config.Read(path)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if cfg.Chats == nil {
		cfg.Chats = []config.Chat{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"chats_dir":       filepath.Join(app.AccountDir(number), "chats"),
		"config":          cfg,
		"default_prompts": map[string]string{"group": bridge.DefaultGroupPrompt, "direct": bridge.DefaultDirectPrompt},
	})
}

// saveConfig writes the config; the running bridge picks it up within seconds.
func (s *server) saveConfig(w http.ResponseWriter, r *http.Request) {
	_, path, err := s.connected()
	if err != nil {
		writeErr(w, http.StatusConflict, err)
		return
	}
	var c config.Config
	if err := readJSON(r, &c); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := config.Save(path, &c); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// groups lists the linked number's groups, fresh from WhatsApp.
func (s *server) groups(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	groups, err := s.account.Groups(ctx)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	if groups == nil {
		groups = []whatsapp.Group{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"groups": groups})
}

// people lists the people the linked number knows by name, to pick a direct chat from.
func (s *server) people(w http.ResponseWriter, _ *http.Request) {
	number, _, err := s.connected()
	if err != nil {
		writeErr(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"people": whatsapp.People(number)})
}

// logout unlinks the number; the panel then shows a QR code for the next one.
func (s *server) logout(w http.ResponseWriter, _ *http.Request) {
	if _, _, err := s.connected(); err != nil {
		writeErr(w, http.StatusConflict, err)
		return
	}
	s.account.Logout()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
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

// user is the part of a JID before the @ (and before a device suffix).
func user(jid string) string {
	u, _, _ := strings.Cut(jid, "@")
	u, _, _ = strings.Cut(u, ":")
	return u
}
