package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Reminder is a note an agent gets back in its chat at a set time.
type Reminder struct {
	ID   string
	At   time.Time
	Note string
}

// Reminders keeps one chat's reminders.
type Reminders interface {
	Add(at time.Time, note string) (Reminder, error)
	List() []Reminder // not yet due, soonest first
	Cancel(id string) bool
}

// whenLayout is how times are written to and read from agents: the format of message times.
const whenLayout = "2006-01-02 15:04"

type remindIn struct {
	In   string `json:"in,omitempty" jsonschema:"how long from now, e.g. 10m, 2h, 1h30m"`
	At   string `json:"at,omitempty" jsonschema:"local time as YYYY-MM-DD HH:MM, like message times"`
	Note string `json:"note" jsonschema:"what to do then; you will read it"`
}

func (b *Binding) setReminder(_ context.Context, _ *mcp.CallToolRequest, in remindIn) (*mcp.CallToolResult, any, error) {
	note := strings.TrimSpace(in.Note)
	if note == "" {
		return nil, nil, errors.New("note is empty")
	}
	var at time.Time
	switch {
	case in.In != "" && in.At != "":
		return nil, nil, errors.New("give in or at, not both")
	case in.In != "":
		d, err := time.ParseDuration(strings.ReplaceAll(in.In, " ", ""))
		if err != nil {
			return nil, nil, fmt.Errorf("in: %w", err)
		}
		at = time.Now().Add(d)
	case in.At != "":
		t, err := time.ParseInLocation(whenLayout, strings.TrimSpace(in.At), time.Local)
		if err != nil {
			return nil, nil, fmt.Errorf("at: want YYYY-MM-DD HH:MM, got %q", in.At)
		}
		at = t
	default:
		return nil, nil, errors.New("give in or at")
	}
	r, err := b.Reminders.Add(at, note)
	if err != nil {
		return nil, nil, err
	}
	return text("Reminder %s set for %s.", r.ID, r.At.Format(whenLayout))
}

func (b *Binding) listReminders(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
	list := b.Reminders.List()
	if len(list) == 0 {
		return text("No reminders set in this chat.")
	}
	var out strings.Builder
	for _, r := range list {
		fmt.Fprintf(&out, "%s  %s  %s\n", r.ID, r.At.Format(whenLayout), r.Note)
	}
	return text("%s", strings.TrimSuffix(out.String(), "\n"))
}

type cancelReminderIn struct {
	ID string `json:"id"`
}

func (b *Binding) cancelReminder(_ context.Context, _ *mcp.CallToolRequest, in cancelReminderIn) (*mcp.CallToolResult, any, error) {
	if !b.Reminders.Cancel(strings.TrimSpace(in.ID)) {
		return nil, nil, fmt.Errorf("no reminder %q in this chat", in.ID)
	}
	return text("Reminder %s cancelled.", in.ID)
}
