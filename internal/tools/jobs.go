package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Job is a task an agent gets back in its chat when it is due: a reminder, a check-in, a summary.
type Job struct {
	ID   string
	At   time.Time // when it is next due
	Cron string    // its schedule, if it repeats
	Task string
}

// Jobs keeps one chat's scheduled jobs.
type Jobs interface {
	// Add schedules task at a time, or on a cron schedule (then at is zero).
	Add(at time.Time, cron, task string) (Job, error)
	List() []Job // not yet due, soonest first
	Cancel(id string) bool
}

// whenLayout is how times are written to and read from agents: the format of message times.
const whenLayout = "2006-01-02 15:04"

type scheduleIn struct {
	In   string `json:"in,omitempty" jsonschema:"once, this long from now, e.g. 10m, 2h, 1h30m"`
	At   string `json:"at,omitempty" jsonschema:"once, at a local time as YYYY-MM-DD HH:MM, like message times"`
	Cron string `json:"cron,omitempty" jsonschema:"repeatedly, on a cron schedule in local time: minute hour day-of-month month day-of-week, e.g. 0 8 * * 1-5"`
	Task string `json:"task" jsonschema:"what to do then; you will read it"`
}

func (b *Binding) scheduleJob(_ context.Context, _ *mcp.CallToolRequest, in scheduleIn) (*mcp.CallToolResult, any, error) {
	task := strings.TrimSpace(in.Task)
	if task == "" {
		return nil, nil, errors.New("task is empty")
	}
	given := 0
	for _, v := range []string{in.In, in.At, in.Cron} {
		if v != "" {
			given++
		}
	}
	var at time.Time
	switch {
	case given > 1:
		return nil, nil, errors.New("give one of in, at or cron")
	case in.Cron != "":
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
		return nil, nil, errors.New("give in, at or cron")
	}
	j, err := b.Jobs.Add(at, strings.TrimSpace(in.Cron), task)
	if err != nil {
		return nil, nil, err
	}
	if j.Cron != "" {
		return text("Job %s scheduled on %s, first at %s.", j.ID, j.Cron, j.At.Format(whenLayout))
	}
	return text("Job %s scheduled for %s.", j.ID, j.At.Format(whenLayout))
}

func (b *Binding) listJobs(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
	list := b.Jobs.List()
	if len(list) == 0 {
		return text("No jobs scheduled in this chat.")
	}
	var out strings.Builder
	for _, j := range list {
		fmt.Fprintf(&out, "%s  next %s", j.ID, j.At.Format(whenLayout))
		if j.Cron != "" {
			fmt.Fprintf(&out, ", on %s", j.Cron)
		}
		fmt.Fprintf(&out, "  %s\n", j.Task)
	}
	return text("%s", strings.TrimSuffix(out.String(), "\n"))
}

type cancelJobIn struct {
	ID string `json:"id"`
}

func (b *Binding) cancelJob(_ context.Context, _ *mcp.CallToolRequest, in cancelJobIn) (*mcp.CallToolResult, any, error) {
	if !b.Jobs.Cancel(strings.TrimSpace(in.ID)) {
		return nil, nil, fmt.Errorf("no job %q in this chat", in.ID)
	}
	return text("Job %s cancelled.", in.ID)
}
