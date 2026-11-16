package bridge

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/daffirds/bailey-cc-bridge/internal/app"
	"github.com/daffirds/bailey-cc-bridge/internal/config"
	"github.com/daffirds/bailey-cc-bridge/internal/engine"
	"github.com/daffirds/bailey-cc-bridge/internal/engine/claude"
	"github.com/daffirds/bailey-cc-bridge/internal/tools"
	"github.com/daffirds/bailey-cc-bridge/internal/whatsapp"
)

// session is one chat's agent. Its goroutine (run) owns the CLI process: it starts it on the first
// message, steers every later message into it and stops it after the idle timeout.
type session struct {
	b      *Bridge
	chat   *config.Chat
	dir    string // ~/.bailey-cc-bridge/chats/<chat>
	log    *slog.Logger
	inbox  chan *whatsapp.Message
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}

	// Owned by run().
	proc       engine.Session
	mcpPath    string
	busy       bool
	pending    []string // prompts not delivered yet because the CLI failed to start
	transcript *os.File
}

func newSession(b *Bridge, chat *config.Chat) *session {
	ctx, cancel := context.WithCancel(context.Background())
	s := &session{
		b: b, chat: chat,
		dir:    filepath.Join(app.Home(), "chats", dirName(chat.JID)),
		log:    b.log.With("chat", chat.Name),
		inbox:  make(chan *whatsapp.Message, 256),
		ctx:    ctx,
		cancel: cancel,
		done:   make(chan struct{}),
	}
	go s.run()
	return s
}

func (s *session) deliver(m *whatsapp.Message) {
	select {
	case s.inbox <- m:
	default:
		s.log.Warn("inbox full; dropping a message", "id", m.ID)
	}
}

func (s *session) stop() {
	s.cancel()
	<-s.done
}

func (s *session) mediaDir() string { return filepath.Join(s.dir, "media") }

func (s *session) run() {
	defer close(s.done)
	idle := time.NewTimer(time.Hour)
	idle.Stop()
	var (
		events <-chan engine.Event
		exited <-chan struct{}
	)
	for {
		select {
		case <-s.ctx.Done():
			s.closeProcess()
			return

		case m := <-s.inbox:
			batch := s.collect(m)
			if p := s.render(batch); p != "" {
				s.pending = append(s.pending, p)
			}
			if len(s.pending) == 0 {
				continue
			}
			if s.proc == nil {
				if err := s.start(); err != nil {
					if s.ctx.Err() == nil {
						s.log.Error("cannot start the agent; retrying with the next message", "err", err)
					}
					if len(s.pending) > 20 {
						s.pending = s.pending[len(s.pending)-20:]
					}
					continue
				}
				events, exited = s.proc.Events(), s.proc.Done()
			}
			idle.Stop()
			s.busy = true
			prompt := strings.Join(s.pending, "\n\n")
			s.pending = nil
			if err := s.proc.Send(s.ctx, prompt); err != nil {
				s.log.Warn("could not deliver to the agent", "err", err)
			}

		case ev, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			s.onEvent(ev, idle)

		case <-exited:
			if err := s.proc.Err(); err != nil {
				s.log.Warn("agent process exited", "err", err)
			}
			s.release()
			events, exited = nil, nil
			idle.Stop()

		case <-idle.C:
			if s.proc != nil && !s.busy {
				s.log.Info("idle; stopping the agent process (the next message resumes it)")
				s.closeProcess()
				events, exited = nil, nil
			}
		}
	}
}

// A message that starts a turn waits until the chat has been quiet this long (at most maxSettle),
// so a burst (several photos, a long thought split over messages) arrives as one prompt.
const (
	settle    = 1500 * time.Millisecond
	maxSettle = 6 * time.Second
)

// collect gathers m and whatever follows it. Mid-turn nothing waits: messages are steered in as
// they come.
func (s *session) collect(m *whatsapp.Message) []*whatsapp.Message {
	batch := []*whatsapp.Message{m}
	if s.busy {
		for {
			select {
			case m := <-s.inbox:
				batch = append(batch, m)
			default:
				return batch
			}
		}
	}
	quiet := time.NewTimer(settle)
	defer quiet.Stop()
	deadline := time.NewTimer(maxSettle)
	defer deadline.Stop()
	for {
		select {
		case m := <-s.inbox:
			batch = append(batch, m)
			quiet.Reset(settle)
		case <-quiet.C:
			return batch
		case <-deadline.C:
			return batch
		case <-s.ctx.Done():
			return batch
		}
	}
}

func (s *session) onEvent(ev engine.Event, idle *time.Timer) {
	switch ev.Kind {
	case engine.TurnStarted:
		s.busy = true
		idle.Stop()
	case engine.TurnCompleted:
		s.busy = false
		if ev.Error != "" {
			s.log.Warn("turn failed", "err", ev.Error)
		}
		idle.Reset(s.chat.IdleTimeout.Std())
	case engine.ToolCall:
		s.log.Info("tool call", "call", excerpt(ev.Text, 160))
	case engine.AssistantText:
		s.log.Debug("agent text (not sent)", "text", excerpt(ev.Text, 200))
	case engine.Notice:
		s.log.Warn(ev.Text)
	}
}

// start launches the chat's CLI, resuming its conversation when there is one.
func (s *session) start() error {
	a := &s.chat.Agent
	bin, err := engine.Find(a.Engine)
	if err != nil {
		return fmt.Errorf("%s CLI not found: %w", a.Engine, err)
	}
	workdir := a.Workdir
	if workdir == "" {
		workdir = filepath.Join(s.dir, "work")
		if err := os.MkdirAll(workdir, 0o700); err != nil {
			return err
		}
	} else if st, err := os.Stat(workdir); err != nil || !st.IsDir() {
		return fmt.Errorf("working directory %s does not exist", workdir)
	}
	if err := os.MkdirAll(s.mediaDir(), 0o700); err != nil {
		return err
	}
	resume := s.b.state.engineID(s.chat.JID, a.Engine)
	if resume != "" && a.Engine == config.EngineClaude && !claude.SessionExists(resume) {
		resume = ""
	}
	s.mcpPath = s.b.host.Register(&tools.Binding{
		Chat: s.chat.JID, Self: s.b.me.ID, Owner: s.b.ownerName(), Workdir: workdir, WA: s.b.wa,
		Resolve: s.b.resolve, LastIncoming: s.b.lastIncoming, Sent: s.b.sent, Log: s.log,
	})
	s.transcript, _ = os.OpenFile(filepath.Join(s.dir, "transcript.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	spec := engine.Spec{
		Binary: bin, Workdir: workdir, Instructions: instructions(s.chat, s.b.me, s.b.ownerName()),
		Model: a.Model, Effort: a.Effort, Autonomy: a.Autonomy,
		MCP:     engine.MCPServer{Name: tools.ServerName, URL: s.b.baseURL + s.mcpPath},
		AddDirs: []string{s.mediaDir()}, ResumeID: resume, StateDir: filepath.Join(s.dir, "engine"),
		Log: s.log, Transcript: engine.NewTranscriptLog(s.transcript),
	}
	eng := s.b.engines[a.Engine]
	p, err := eng.Start(s.ctx, spec)
	if err != nil && resume != "" {
		s.log.Warn("could not resume; starting a fresh conversation", "err", err)
		spec.ResumeID = ""
		p, err = eng.Start(s.ctx, spec)
	}
	if err != nil {
		s.release()
		return err
	}
	s.proc = p
	if err := s.b.state.set(s.chat.JID, a.Engine, p.ID()); err != nil {
		s.log.Warn("cannot save session state", "err", err)
	}
	s.log.Info("agent process started", "engine", a.Engine, "session", p.ID(), "resumed", spec.ResumeID != "", "workdir", workdir)
	return nil
}

func (s *session) closeProcess() {
	if s.proc != nil {
		_ = s.proc.Close()
	}
	s.release()
}

func (s *session) release() {
	s.proc = nil
	s.busy = false
	if s.mcpPath != "" {
		s.b.host.Unregister(s.mcpPath)
		s.mcpPath = ""
	}
	if s.transcript != nil {
		_ = s.transcript.Close()
		s.transcript = nil
	}
}

// dirName turns a JID into a directory name: 6281234567890@s.whatsapp.net → 6281234567890_s.whatsapp.net.
func dirName(jid string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '.':
			return r
		}
		return '_'
	}, jid)
}
