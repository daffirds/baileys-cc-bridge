package bridge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// state remembers each chat's engine conversation, so it resumes after a restart.
type state struct {
	path string

	mu    sync.Mutex
	Chats map[string]chatState `json:"chats"`
}

type chatState struct {
	Engine   string    `json:"engine"`
	EngineID string    `json:"engine_id"`
	Updated  time.Time `json:"updated"`
}

func loadState(path string) *state {
	s := &state{path: path, Chats: map[string]chatState{}}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, s)
	}
	if s.Chats == nil {
		s.Chats = map[string]chatState{}
	}
	return s
}

// engineID returns the conversation to resume for chat, if it ran on the same engine.
func (s *state) engineID(chat, engine string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c := s.Chats[chat]; c.Engine == engine {
		return c.EngineID
	}
	return ""
}

func (s *state) set(chat, engine, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Chats[chat] = chatState{Engine: engine, EngineID: id, Updated: time.Now()}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
