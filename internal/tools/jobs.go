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
	At   time.Time
	Task string
}

// Jobs keeps one chat's scheduled jobs.
type Jobs interface {
	Add(at time.Time, task string) (Job, error)
	List() []Job // not yet due, soonest first
	Cancel(id string) bool
}

// whenLayout is how times are written to and read from agents: the format of message times.
const whenLayout = "2006-01-02 15:04"

type scheduleIn struct {
	In   string `json:"in,omitempty" jsonschema:"how long from now, e.g. 10m, 2h, 1h30m"`
	At   string `json:"at,omitempty" jsonschema:"local time as YYYY-MM-DD HH:MM, like message times"`
	Task string `json:"task" jsonschema:"what to do then; you will read it"`
}

func (b *Binding) scheduleJob(_ context.Context, _ *mcp.CallToolRequest, in scheduleIn) (*mcp.CallToolResult, any, error) {
	task := strings.TrimSpace(in.Task)
	if task == "" {
		return nil, nil, errors.New("task is empty")
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
	j, err := b.Jobs.Add(at, task)
	if err != nil {
		return nil, nil, err
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
		fmt.Fprintf(&out, "%s  %s  %s\n", j.ID, j.At.Format(whenLayout), j.Task)
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
