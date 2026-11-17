package bridge

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/daffirds/bailey-cc-bridge/internal/config"
	"github.com/daffirds/bailey-cc-bridge/internal/engine"
	"github.com/daffirds/bailey-cc-bridge/internal/tools"
	"github.com/daffirds/bailey-cc-bridge/internal/whatsapp"
)

// fakeEngine records every start; its processes start a turn on each Send and end it when told.
type fakeEngine struct{ starts chan *fakeProc }

func (f *fakeEngine) Start(_ context.Context, spec engine.Spec) (engine.Session, error) {
	p := &fakeProc{spec: spec, events: make(chan engine.Event, 16), done: make(chan struct{}), sent: make(chan string, 16)}
	f.starts <- p
	return p, nil
}

func (f *fakeEngine) Models(context.Context, string) ([]engine.Model, error) { return nil, nil }

type fakeProc struct {
	spec   engine.Spec
	events chan engine.Event
	done   chan struct{}
	sent   chan string
	once   sync.Once
}

func (p *fakeProc) ID() string { return "fake-conversation" }

func (p *fakeProc) Send(_ context.Context, text string) error {
	p.sent <- text
	p.events <- engine.Event{Kind: engine.TurnStarted}
	return nil
}

func (p *fakeProc) Events() <-chan engine.Event { return p.events }
func (p *fakeProc) Done() <-chan struct{}       { return p.done }
func (p *fakeProc) Err() error                  { return nil }
func (p *fakeProc) Close() error                { p.once.Do(func() { close(p.done) }); return nil }

func TestChangedSettingsWaitForTheTurn(t *testing.T) {
	t.Setenv("BAILEY_CC_BRIDGE_HOME", t.TempDir())
	// session.start looks the CLI up on PATH.
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	fake := &fakeEngine{starts: make(chan *fakeProc, 4)}
	saved := engines[config.EngineCodex]
	engines[config.EngineCodex] = fake
	defer func() { engines[config.EngineCodex] = saved }()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	me := &whatsapp.Me{ID: "628880000000@s.whatsapp.net", Name: "Owner"}
	chat := config.Chat{JID: me.ID, Name: "Me", Agent: config.Agent{Engine: config.EngineCodex, Autonomy: config.AutonomyAuto}}
	b := New(&config.Config{Chats: []config.Chat{chat}}, "", &fakeWA{sent: make(chan string, 8)}, me, tools.NewHost(log), "http://127.0.0.1:1", log)
	defer b.stop()
	msg := func(id string) *whatsapp.Message {
		return &whatsapp.Message{ID: id, Chat: me.ID, Sender: me.ID, FromMe: true, Kind: "text", Text: "hi", Timestamp: time.Now().Unix()}
	}

	b.route(msg("M1"))
	first := receive(t, fake.starts)
	receive(t, first.sent) // the turn is running now

	changed := chat
	changed.Model = "gpt-small"
	b.apply(&config.Config{Chats: []config.Chat{changed}})
	select {
	case <-first.done:
		t.Fatal("the agent was stopped in the middle of a turn")
	case <-time.After(300 * time.Millisecond):
	}

	first.events <- engine.Event{Kind: engine.TurnCompleted}
	receive(t, first.done) // the turn is over: the old settings go

	b.route(msg("M2"))
	second := receive(t, fake.starts)
	if second.spec.Model != "gpt-small" {
		t.Errorf("restarted with model %q, want gpt-small", second.spec.Model)
	}
	if second.spec.ResumeID != "fake-conversation" {
		t.Errorf("restarted with resume id %q, want the same conversation", second.spec.ResumeID)
	}
}

func receive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(5 * time.Second):
		t.Fatal("timed out")
		panic("unreachable")
	}
}
