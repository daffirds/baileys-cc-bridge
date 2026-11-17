package bridge

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/daffirds/baileys-cc-bridge/internal/tools"
	"github.com/daffirds/baileys-cc-bridge/internal/whatsapp"
)

// jobs keeps the jobs agents schedule (jobs.json) and hands each back to its chat when it comes
// due. One that came due while the bridge was off arrives as soon as it is back, late; a repeating
// one then carries on from its next time, so missed runs don't pile up.
type jobs struct {
	path    string
	changed chan struct{} // the next one due may have changed

	mu   sync.Mutex
	list []job
}

type job struct {
	ID   string    `json:"id"`
	Chat string    `json:"chat"`
	At   time.Time `json:"at"`             // when it is next due
	Cron string    `json:"cron,omitempty"` // its schedule, if it repeats
	Task string    `json:"task"`
}

// nextRun is when a job on the cron schedule spec is next due after t; zero if never.
func nextRun(spec string, t time.Time) (time.Time, error) {
	sched, err := cron.ParseStandard(spec)
	if err != nil {
		return time.Time{}, err
	}
	return sched.Next(t), nil
}

func loadJobs(path string) *jobs {
	j := &jobs{path: path, changed: make(chan struct{}, 1)}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &j.list)
	}
	return j
}

// add schedules task at a time, or on the cron schedule spec.
func (j *jobs) add(chat string, at time.Time, spec, task string) (job, error) {
	now := time.Now()
	if spec != "" {
		next, err := nextRun(spec, now)
		if err != nil {
			return job{}, fmt.Errorf("cron: %w", err)
		}
		if next.IsZero() {
			return job{}, errors.New("cron: that schedule never comes")
		}
		at = next
	}
	switch {
	case !at.After(now):
		return job{}, errors.New("that time has passed")
	case spec == "" && at.After(now.AddDate(1, 0, 0)):
		return job{}, errors.New("that is more than a year away")
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	jb := job{ID: j.newID(), Chat: chat, At: at, Cron: spec, Task: task}
	j.list = append(j.list, jb)
	if err := j.save(); err != nil {
		j.list = j.list[:len(j.list)-1]
		return job{}, err
	}
	j.poke()
	return jb, nil
}

// of lists chat's jobs, soonest first.
func (j *jobs) of(chat string) []job {
	j.mu.Lock()
	defer j.mu.Unlock()
	var out []job
	for _, jb := range j.list {
		if jb.Chat == chat {
			out = append(out, jb)
		}
	}
	slices.SortFunc(out, func(a, b job) int { return a.At.Compare(b.At) })
	return out
}

func (j *jobs) cancel(chat, id string) bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	i := slices.IndexFunc(j.list, func(jb job) bool { return jb.Chat == chat && jb.ID == id })
	if i < 0 {
		return false
	}
	j.list = slices.Delete(j.list, i, i+1)
	_ = j.save()
	j.poke()
	return true
}

// next is when the soonest job is due.
func (j *jobs) next() (time.Time, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	var at time.Time
	for _, jb := range j.list {
		if at.IsZero() || jb.At.Before(at) {
			at = jb.At
		}
	}
	return at, !at.IsZero()
}

// take returns the jobs due by now. One-off jobs leave the list; repeating ones move on to their
// next time after now.
func (j *jobs) take(now time.Time) []job {
	j.mu.Lock()
	defer j.mu.Unlock()
	var due []job
	j.list = slices.DeleteFunc(j.list, func(jb job) bool {
		if jb.At.After(now) {
			return false
		}
		due = append(due, jb)
		return true
	})
	for _, jb := range due {
		if jb.Cron == "" {
			continue
		}
		if next, err := nextRun(jb.Cron, now); err == nil && !next.IsZero() {
			jb.At = next
			j.list = append(j.list, jb)
		}
	}
	if len(due) > 0 {
		_ = j.save()
	}
	return due
}

func (j *jobs) poke() {
	select {
	case j.changed <- struct{}{}:
	default:
	}
}

// newID is a short id no pending job has. Called with mu held.
func (j *jobs) newID() string {
	for {
		b := make([]byte, 3)
		_, _ = rand.Read(b)
		id := "j" + hex.EncodeToString(b)
		if !slices.ContainsFunc(j.list, func(jb job) bool { return jb.ID == id }) {
			return id
		}
	}
}

// save writes the list. Called with mu held.
func (j *jobs) save() error {
	b, err := json.MarshalIndent(j.list, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(j.path), 0o700); err != nil {
		return err
	}
	tmp := j.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, j.path)
}

// runJobs hands each job to its chat as it comes due, until ctx ends.
func (b *Bridge) runJobs(ctx context.Context) {
	t := time.NewTimer(time.Hour)
	defer t.Stop()
	for {
		// Timers stop counting while the Mac sleeps, so the clock is checked again at least every
		// minute: a job that came due during sleep runs within a minute of waking.
		wait := time.Minute
		if at, ok := b.jobs.next(); ok {
			wait = min(max(time.Until(at), 0), time.Minute)
		}
		t.Reset(wait)
		select {
		case <-ctx.Done():
			return
		case <-b.jobs.changed:
		case <-t.C:
			for _, jb := range b.jobs.take(time.Now()) {
				b.deliverJob(jb)
			}
		}
	}
}

// deliverJob gives a due job to its chat's agent the way a message comes in: a <scheduled_job>
// block with the task.
func (b *Bridge) deliverJob(jb job) {
	b.mu.Lock()
	defer b.mu.Unlock()
	chat := b.cfg.Chat(jb.Chat)
	if chat == nil {
		b.log.Info("dropping a job for a chat that is no longer whitelisted", "jid", jb.Chat, "id", jb.ID)
		return
	}
	b.log.Info("job due", "chat", chat.Name, "id", jb.ID)
	attrs := []string{attr("id", jb.ID), attr("due", jb.At.Format("2006-01-02 15:04"))}
	if jb.Cron != "" {
		attrs = append(attrs, attr("cron", jb.Cron))
	}
	if time.Since(jb.At) > time.Minute {
		attrs = append(attrs, `late="true"`)
	}
	block := "<scheduled_job " + strings.Join(attrs, " ") + ">\n" + jb.Task + "\n</scheduled_job>"
	b.session(chat).deliver(&whatsapp.Message{ID: jb.ID, Chat: chat.JID, Kind: "scheduled_job", Text: block, Timestamp: time.Now().Unix()})
}

// chatJobs is one chat's jobs, as its tools see them.
type chatJobs struct {
	j    *jobs
	chat string
}

func (c chatJobs) Add(at time.Time, spec, task string) (tools.Job, error) {
	jb, err := c.j.add(c.chat, at, spec, task)
	return tools.Job{ID: jb.ID, At: jb.At, Cron: jb.Cron, Task: jb.Task}, err
}

func (c chatJobs) List() []tools.Job {
	var out []tools.Job
	for _, jb := range c.j.of(c.chat) {
		out = append(out, tools.Job{ID: jb.ID, At: jb.At, Cron: jb.Cron, Task: jb.Task})
	}
	return out
}

func (c chatJobs) Cancel(id string) bool { return c.j.cancel(c.chat, id) }
