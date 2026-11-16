//go:build unix

package whatsapp

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/daffirds/bailey-cc-bridge/internal/app"
)

// ErrBusy means another process holds the WhatsApp connection.
var ErrBusy = errors.New("another bailey-cc-bridge process is connected to WhatsApp (is `run` running?)")

// lockConnection makes sure one process at a time connects with these credentials: a second
// connection makes WhatsApp drop the first, and a running bridge would miss messages meanwhile.
// The lock goes away with the file (or the process).
func lockConnection() (*os.File, error) {
	if err := os.MkdirAll(app.Home(), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(app.Home(), "whatsapp.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, ErrBusy
	}
	return f, nil
}

func groupsPath() string { return filepath.Join(app.Home(), "groups.json") }

type groupCache struct {
	Updated time.Time `json:"updated"`
	Groups  []Group   `json:"groups"`
}

// CachedGroups returns the group list from the last time a command fetched it (zero time if never),
// so the config UI can offer groups without connecting while the bridge runs.
func CachedGroups() ([]Group, time.Time) {
	var c groupCache
	if b, err := os.ReadFile(groupsPath()); err == nil {
		_ = json.Unmarshal(b, &c)
	}
	return c.Groups, c.Updated
}

func saveGroups(groups []Group) {
	b, err := json.Marshal(groupCache{Updated: time.Now(), Groups: groups})
	if err == nil {
		_ = os.WriteFile(groupsPath(), b, 0o600)
	}
}
