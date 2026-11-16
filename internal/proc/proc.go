//go:build unix

// Package proc starts child processes in their own process group, so Ctrl-C in the terminal
// reaches only the bridge (which then shuts its children down in order) and stopping a child
// also stops everything it started.
package proc

import (
	"os/exec"
	"syscall"
	"time"
)

// Group is a started child process whose whole tree can be killed at once.
type Group struct {
	Cmd *exec.Cmd
}

// Start starts cmd as the leader of a new process group.
func Start(cmd *exec.Cmd) (*Group, error) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &Group{Cmd: cmd}, nil
}

// Kill terminates the process and all of its descendants.
func (g *Group) Kill() {
	if g.Cmd.Process == nil {
		return
	}
	// A negative pid signals the whole process group created by Setpgid.
	_ = syscall.Kill(-g.Cmd.Process.Pid, syscall.SIGKILL)
	_ = g.Cmd.Process.Kill()
}

// Stop waits up to grace for the process to exit on its own (callers close its stdin first),
// then kills the tree.
func (g *Group) Stop(done <-chan struct{}, grace time.Duration) {
	select {
	case <-done:
	case <-time.After(grace):
	}
	g.Kill()
}
