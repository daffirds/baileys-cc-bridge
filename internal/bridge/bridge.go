// Package bridge routes whitelisted WhatsApp chats to agent sessions: one chat, one session. It
// never speaks itself; everything people see in WhatsApp is a tool call an agent made.
package bridge

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/daffirds/bailey-cc-bridge/internal/app"
	"github.com/daffirds/bailey-cc-bridge/internal/config"
	"github.com/daffirds/bailey-cc-bridge/internal/engine"
	"github.com/daffirds/bailey-cc-bridge/internal/engine/claude"
	"github.com/daffirds/bailey-cc-bridge/internal/engine/codex"
	"github.com/daffirds/bailey-cc-bridge/internal/tools"
	"github.com/daffirds/bailey-cc-bridge/internal/whatsapp"
)

// WhatsApp is the part of the sidecar client the bridge uses (*whatsapp.Client).
type WhatsApp interface {
	tools.Sender
	Download(ctx context.Context, id, path string) error
	Events() <-chan whatsapp.Event
	Done() <-chan struct{}
	Err() error
}

type Bridge struct {
	cfgPath string
	wa      WhatsApp
	me      *whatsapp.Me
	host    *tools.Host
	baseURL string // where host is served, e.g. http://127.0.0.1:53123
	dir     string // the linked number's folder (app.AccountDir)
	log     *slog.Logger
	state   *state

	mu       sync.Mutex
	cfg      *config.Config      // replaced when config.yaml changes
	sessions map[string]*session // by chat JID
	notes    *ring[note]         // recent messages by id, for quotes and reactions
	last     map[string]string   // chat JID → id of the latest message received there
	ignored  map[string]bool     // chats already logged as ignored
}

// note is what the bridge remembers about a recent message.
type note struct {
	from string
	text string
	own  bool // sent by an agent
}

var engines = map[string]engine.Engine{config.EngineClaude: claude.Engine{}, config.EngineCodex: codex.Engine{}}

// Models lists the models the named engine's CLI offers, its default first (ID "").
func Models(ctx context.Context, name string) ([]engine.Model, error) {
	e, ok := engines[name]
	if !ok {
		return nil, fmt.Errorf("unknown engine %q", name)
	}
	bin, err := engine.Find(name)
	if err != nil {
		return nil, fmt.Errorf("%s CLI not found", name)
	}
	return e.Models(ctx, bin)
}

// New makes a bridge for cfg, which was loaded from cfgPath; Run reloads it when the file changes.
func New(cfg *config.Config, cfgPath string, wa WhatsApp, me *whatsapp.Me, host *tools.Host, baseURL string, log *slog.Logger) *Bridge {
	return &Bridge{
		cfg: cfg, cfgPath: cfgPath, wa: wa, me: me, host: host, baseURL: baseURL, log: log,
		dir:      app.AccountDir(user(me.ID)),
		state:    loadState(filepath.Join(app.AccountDir(user(me.ID)), "state.json")),
		sessions: map[string]*session{},
		notes:    newRing[note](5000),
		last:     map[string]string{},
		ignored:  map[string]bool{},
	}
}

// Run routes WhatsApp events until ctx ends or the connection is lost for good. It stops the agent
// processes before returning; their conversations resume with the next message after a restart.
func (b *Bridge) Run(ctx context.Context) error {
	defer b.stop()
	poll := time.NewTicker(3 * time.Second)
	defer poll.Stop()
	modified := modTime(b.cfgPath)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-poll.C:
			if t := modTime(b.cfgPath); !t.Equal(modified) {
				modified = t
				b.reload()
			}
		case <-b.wa.Done():
			return fmt.Errorf("whatsapp sidecar exited: %v", b.wa.Err())
		case ev := <-b.wa.Events():
			switch ev.Type {
			case "message":
				if ev.Message != nil {
					b.route(ev.Message)
				}
			case "ready":
				b.log.Info("reconnected to WhatsApp")
			case "closed":
				return whatsapp.ClosedError(ev.Reason)
			case "qr":
				return errors.New("WhatsApp wants this Mac linked again: run `bailey-cc-bridge logout`, then `login`")
			}
		}
	}
}

// reload applies an edited config.yaml.
func (b *Bridge) reload() {
	cfg, err := config.Load(b.cfgPath)
	if err != nil {
		b.log.Warn("config.yaml changed but is invalid; keeping the previous config", "err", err)
		return
	}
	b.apply(cfg)
}

// apply switches to cfg. Removed chats' agents stop. A chat whose settings changed gets them
// between turns: its agent is left to finish what it is doing, and the next message that finds it
// idle starts it again with the new settings, resuming the same conversation.
func (b *Bridge) apply(cfg *config.Config) {
	b.mu.Lock()
	var removed []*session
	changed := 0
	for jid, s := range b.sessions {
		switch c := cfg.Chat(jid); {
		case c == nil:
			removed = append(removed, s)
			delete(b.sessions, jid)
		case *c != *s.given:
			s.given = c
			s.update(c)
			changed++
		}
	}
	b.cfg = cfg
	b.ignored = map[string]bool{}
	b.mu.Unlock()
	for _, s := range removed {
		go s.stop()
	}
	b.log.Info("config reloaded", "chats", len(cfg.Chats), "changed", changed, "removed", len(removed))
}

func modTime(path string) time.Time {
	st, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return st.ModTime()
}

func (b *Bridge) route(m *whatsapp.Message) {
	b.log.Debug("message", "chat", m.Chat, "id", m.ID, "kind", m.Kind, "from_me", m.FromMe)
	b.mu.Lock()
	defer b.mu.Unlock()
	chat := b.cfg.Chat(m.Chat, m.ChatAlt)
	if chat == nil {
		if !b.ignored[m.Chat] {
			b.ignored[m.Chat] = true
			b.log.Info("ignoring a chat that is not in config.yaml", "jid", m.Chat, "from", m.PushName)
		}
		return
	}
	if n, ok := b.notes.get(m.ID); ok && n.own {
		return // an agent's own message coming back
	}
	switch m.Kind {
	case "reaction", "edit", "revoke":
	default:
		b.last[chat.JID] = m.ID
		from, _, _ := b.sender(m)
		b.notes.put(m.ID, note{from: from, text: excerpt(m.Text, 200)})
	}
	s := b.sessions[chat.JID]
	if s == nil {
		s = newSession(b, chat)
		b.sessions[chat.JID] = s
	}
	s.deliver(m)
}

func (b *Bridge) stop() {
	b.mu.Lock()
	var all []*session
	for _, s := range b.sessions {
		all = append(all, s)
	}
	b.mu.Unlock()
	var wg sync.WaitGroup
	for _, s := range all {
		wg.Go(s.stop)
	}
	wg.Wait()
}

// sender names who wrote m. owner is true for the account owner (typed on their phone).
func (b *Bridge) sender(m *whatsapp.Message) (name, number string, owner bool) {
	if m.FromMe {
		return b.ownerName(), "+" + user(b.me.ID), true
	}
	if strings.HasSuffix(m.Sender, "@s.whatsapp.net") {
		number = "+" + user(m.Sender)
	}
	name = m.PushName
	if name == "" {
		name = number
	}
	if name == "" {
		name = m.Sender
	}
	return name, number, false
}

func (b *Bridge) ownerName() string {
	if b.me.Name != "" {
		return b.me.Name
	}
	return "the owner"
}

// --- tools.Binding callbacks ---

// resolve maps a chat name, JID or phone number to a whitelisted chat's JID.
func (b *Bridge) resolve(name string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if jid, err := config.NormalizeJID(name); err == nil {
		if c := b.cfg.Chat(jid); c != nil {
			return c.JID, nil
		}
	}
	for i := range b.cfg.Chats {
		if strings.EqualFold(b.cfg.Chats[i].Name, strings.TrimSpace(name)) {
			return b.cfg.Chats[i].JID, nil
		}
	}
	return "", fmt.Errorf("%q is not a whitelisted chat", name)
}

func (b *Bridge) lastIncoming(chat string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.last[chat]
}

func (b *Bridge) sent(_, id, text string) {
	if id == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.notes.put(id, note{from: "you", text: excerpt(text, 200), own: true})
}

// ring is a map that forgets its oldest entries beyond max.
type ring[V any] struct {
	keys []string
	m    map[string]V
	max  int
}

func newRing[V any](max int) *ring[V] { return &ring[V]{m: map[string]V{}, max: max} }

func (r *ring[V]) put(k string, v V) {
	if _, ok := r.m[k]; !ok {
		r.keys = append(r.keys, k)
		if len(r.keys) > r.max {
			delete(r.m, r.keys[0])
			r.keys = r.keys[1:]
		}
	}
	r.m[k] = v
}

func (r *ring[V]) get(k string) (V, bool) {
	v, ok := r.m[k]
	return v, ok
}

// user is the part of a JID before the @ (and before a device suffix).
func user(jid string) string {
	u, _, _ := strings.Cut(jid, "@")
	u, _, _ = strings.Cut(u, ":")
	return u
}

func excerpt(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
