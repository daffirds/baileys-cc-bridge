package bridge

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/daffirds/baileys-cc-bridge/internal/tools"
	"github.com/daffirds/baileys-cc-bridge/internal/whatsapp"
)

// reminders keeps the reminders agents set (reminders.json) and hands each back to its chat when
// it comes due. One that came due while the bridge was off arrives as soon as it is back, late.
type reminders struct {
	path    string
	changed chan struct{} // the next one due may have changed

	mu   sync.Mutex
	list []reminder
}

type reminder struct {
	ID   string    `json:"id"`
	Chat string    `json:"chat"`
	At   time.Time `json:"at"`
	Note string    `json:"note"`
}

func loadReminders(path string) *reminders {
	r := &reminders{path: path, changed: make(chan struct{}, 1)}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &r.list)
	}
	return r
}

func (r *reminders) add(chat string, at time.Time, note string) (reminder, error) {
	now := time.Now()
	switch {
	case !at.After(now):
		return reminder{}, errors.New("that time has passed")
	case at.After(now.AddDate(1, 0, 0)):
		return reminder{}, errors.New("that is more than a year away")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	rem := reminder{ID: r.newID(), Chat: chat, At: at, Note: note}
	r.list = append(r.list, rem)
	if err := r.save(); err != nil {
		r.list = r.list[:len(r.list)-1]
		return reminder{}, err
	}
	r.poke()
	return rem, nil
}

// of lists chat's reminders, soonest first.
func (r *reminders) of(chat string) []reminder {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []reminder
	for _, rem := range r.list {
		if rem.Chat == chat {
			out = append(out, rem)
		}
	}
	slices.SortFunc(out, func(a, b reminder) int { return a.At.Compare(b.At) })
	return out
}

func (r *reminders) cancel(chat, id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	i := slices.IndexFunc(r.list, func(rem reminder) bool { return rem.Chat == chat && rem.ID == id })
	if i < 0 {
		return false
	}
	r.list = slices.Delete(r.list, i, i+1)
	_ = r.save()
	r.poke()
	return true
}

// next is when the soonest reminder is due.
func (r *reminders) next() (time.Time, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var at time.Time
	for _, rem := range r.list {
		if at.IsZero() || rem.At.Before(at) {
			at = rem.At
		}
	}
	return at, !at.IsZero()
}

// take removes and returns the reminders due by now.
func (r *reminders) take(now time.Time) []reminder {
	r.mu.Lock()
	defer r.mu.Unlock()
	var due []reminder
	r.list = slices.DeleteFunc(r.list, func(rem reminder) bool {
		if rem.At.After(now) {
			return false
		}
		due = append(due, rem)
		return true
	})
	if len(due) > 0 {
		_ = r.save()
	}
	return due
}

func (r *reminders) poke() {
	select {
	case r.changed <- struct{}{}:
	default:
	}
}

// newID is a short id no pending reminder has. Called with mu held.
func (r *reminders) newID() string {
	for {
		b := make([]byte, 3)
		_, _ = rand.Read(b)
		id := "r" + hex.EncodeToString(b)
		if !slices.ContainsFunc(r.list, func(rem reminder) bool { return rem.ID == id }) {
			return id
		}
	}
}

// save writes the list. Called with mu held.
func (r *reminders) save() error {
	b, err := json.MarshalIndent(r.list, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0o700); err != nil {
		return err
	}
	tmp := r.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, r.path)
}

// remind hands each reminder to its chat as it comes due, until ctx ends.
func (b *Bridge) remind(ctx context.Context) {
	t := time.NewTimer(time.Hour)
	defer t.Stop()
	for {
		wait := time.Hour
		if at, ok := b.reminders.next(); ok {
			wait = max(time.Until(at), 0)
		}
		t.Reset(wait)
		select {
		case <-ctx.Done():
			return
		case <-b.reminders.changed:
		case <-t.C:
			for _, r := range b.reminders.take(time.Now()) {
				b.deliverReminder(r)
			}
		}
	}
}

// deliverReminder gives a due reminder to its chat's agent the way a message comes in.
func (b *Bridge) deliverReminder(r reminder) {
	b.mu.Lock()
	defer b.mu.Unlock()
	chat := b.cfg.Chat(r.Chat)
	if chat == nil {
		b.log.Info("dropping a reminder for a chat that is no longer whitelisted", "jid", r.Chat, "id", r.ID)
		return
	}
	b.log.Info("reminder due", "chat", chat.Name, "id", r.ID)
	b.session(chat).deliver(&whatsapp.Message{ID: r.ID, Chat: chat.JID, Kind: "reminder", Text: r.Note, Timestamp: r.At.Unix()})
}

// chatReminders is one chat's reminders, as its tools see them.
type chatReminders struct {
	r    *reminders
	chat string
}

func (c chatReminders) Add(at time.Time, note string) (tools.Reminder, error) {
	rem, err := c.r.add(c.chat, at, note)
	return tools.Reminder{ID: rem.ID, At: rem.At, Note: rem.Note}, err
}

func (c chatReminders) List() []tools.Reminder {
	var out []tools.Reminder
	for _, rem := range c.r.of(c.chat) {
		out = append(out, tools.Reminder{ID: rem.ID, At: rem.At, Note: rem.Note})
	}
	return out
}

func (c chatReminders) Cancel(id string) bool { return c.r.cancel(c.chat, id) }
