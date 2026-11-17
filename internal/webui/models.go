package webui

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/daffirds/bailey-cc-bridge/internal/bridge"
	"github.com/daffirds/bailey-cc-bridge/internal/engine"
)

// listModels lists an engine's models and their efforts, as its CLI reports them.
func (s *server) listModels(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	models, err := s.models.get(ctx, r.URL.Query().Get("engine"))
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": models})
}

// modelCache keeps each engine's list for a while: asking a CLI takes about half a second.
type modelCache struct {
	mu    sync.Mutex
	lists map[string]modelList
}

type modelList struct {
	at     time.Time
	models []engine.Model
}

func (c *modelCache) get(ctx context.Context, name string) ([]engine.Model, error) {
	c.mu.Lock()
	l, ok := c.lists[name]
	c.mu.Unlock()
	if ok && time.Since(l.at) < 10*time.Minute {
		return l.models, nil
	}
	models, err := bridge.Models(ctx, name)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.lists == nil {
		c.lists = map[string]modelList{}
	}
	c.lists[name] = modelList{at: time.Now(), models: models}
	return models, nil
}
