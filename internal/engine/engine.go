// Package engine drives local coding-agent CLIs (Claude Code, Codex) as long-lived, steerable
// sessions. The CLIs use their own logins, so subscriptions work as they are. The drivers are
// ported from agent-shift.
package engine

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// Spec describes how to start or resume a session.
type Spec struct {
	Binary       string // absolute path of the CLI
	Workdir      string
	Instructions string // appended to the engine's own system prompt
	Model        string // "" = the CLI's default
	Effort       string // reasoning effort; "" = the model's default
	Autonomy     string // config.Autonomy*
	MCP          MCPServer
	AddDirs      []string // extra directories the agent may read and write (claude --add-dir)
	ResumeID     string   // engine-native session id to resume; "" starts fresh
	StateDir     string   // private per-session directory for prompt and config files
	Log          *slog.Logger
}

// MCPServer is the bridge's WhatsApp tool server the session connects to.
type MCPServer struct {
	Name string
	URL  string
}

type EventKind int

const (
	TurnStarted EventKind = iota
	TurnCompleted
	AssistantText
	ToolCall
	Notice // warnings worth logging (MCP failures, denied permissions…)
)

type Event struct {
	Kind  EventKind
	Text  string
	Error string // TurnCompleted: non-empty when the turn failed
}

// Session is a running CLI process bound to one conversation.
type Session interface {
	// ID is the engine-native conversation id used to resume later.
	ID() string
	// Send delivers a user message. If a turn is running it is steered into it; otherwise a new
	// turn starts.
	Send(ctx context.Context, text string) error
	Events() <-chan Event
	// Done is closed when the process exits; Err tells why.
	Done() <-chan struct{}
	Err() error
	// Close shuts the process down (gracefully, then forcefully).
	Close() error
}

type Engine interface {
	Start(ctx context.Context, spec Spec) (Session, error)
}

var ErrClosed = errors.New("session closed")

// Find locates an engine CLI on PATH, then in the usual install locations.
func Find(name string) (string, error) {
	if p, err := exec.LookPath(name); err == nil {
		if abs, err := filepath.Abs(p); err == nil {
			return abs, nil
		}
		return p, nil
	}
	home, _ := os.UserHomeDir()
	for _, c := range []string{
		filepath.Join(home, ".local", "bin", name),
		filepath.Join(home, ".claude", "local", name),
		"/opt/homebrew/bin/" + name,
		"/usr/local/bin/" + name,
		filepath.Join(home, ".npm-global", "bin", name),
		filepath.Join(home, ".bun", "bin", name),
		filepath.Join(home, ".volta", "bin", name),
	} {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c, nil
		}
	}
	return "", exec.ErrNotFound
}

// StderrTail keeps the last few KB of a process's stderr for error messages.
type StderrTail struct {
	mu  sync.Mutex
	buf []byte
}

func (r *StderrTail) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf = append(r.buf, p...)
	if len(r.buf) > 8192 {
		r.buf = r.buf[len(r.buf)-8192:]
	}
	return len(p), nil
}

// Suffix returns ": <last stderr lines>" for appending to an error, or "".
func (r *StderrTail) Suffix() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := strings.TrimSpace(string(r.buf))
	if s == "" {
		return ""
	}
	if len(s) > 800 {
		s = "…" + s[len(s)-800:]
	}
	return ": " + s
}
