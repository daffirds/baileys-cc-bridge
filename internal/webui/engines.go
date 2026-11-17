package webui

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/daffirds/baileys-cc-bridge/internal/bridge"
	"github.com/daffirds/baileys-cc-bridge/internal/config"
)

// engineHealth is whether an engine can run agents on this Mac, and if not, the command that
// fixes it.
type engineHealth struct {
	Name   string `json:"name"`
	State  string `json:"state"`            // ready | missing | signed_out | error
	Detail string `json:"detail,omitempty"` // what went wrong, for error
	Fix    string `json:"fix,omitempty"`    // a command to run in a terminal
}

// setup is how each engine's CLI is installed and logged in.
var setup = map[string]struct{ install, login string }{
	config.EngineClaude: {"curl -fsSL https://claude.ai/install.sh | bash", "claude auth login"},
	config.EngineCodex:  {"npm install -g @openai/codex", "codex login"},
}

// listEngines says whether each engine's CLI is installed and logged in. The answer is kept for a
// minute unless ?fresh=1.
func (s *server) listEngines(w http.ResponseWriter, r *http.Request) {
	// Detached: an answer cut short by a closed tab would be kept as an error.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 30*time.Second)
	defer cancel()
	writeJSON(w, http.StatusOK, map[string]any{"engines": s.health.get(ctx, r.URL.Query().Get("fresh") != "")})
}

type healthCache struct {
	mu   sync.Mutex // held while checking, so requests at the same time share one check
	at   time.Time
	list []engineHealth
}

func (c *healthCache) get(ctx context.Context, fresh bool) []engineHealth {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !fresh && c.list != nil && time.Since(c.at) < time.Minute {
		return c.list
	}
	names := []string{config.EngineClaude, config.EngineCodex}
	list := make([]engineHealth, len(names))
	var wg sync.WaitGroup
	for i, name := range names {
		wg.Go(func() { list[i] = checkEngine(ctx, name) })
	}
	wg.Wait()
	c.list, c.at = list, time.Now()
	return list
}

func checkEngine(ctx context.Context, name string) engineHealth {
	h := engineHealth{Name: name, State: "ready"}
	signedIn, err := bridge.SignedIn(ctx, name)
	switch {
	case errors.Is(err, bridge.ErrNoCLI):
		h.State, h.Fix = "missing", setup[name].install
	case err != nil:
		h.State, h.Detail = "error", err.Error()
	case !signedIn:
		h.State, h.Fix = "signed_out", setup[name].login
	}
	return h
}
