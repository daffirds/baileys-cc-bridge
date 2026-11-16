// Package claude drives Claude Code headless: `claude -p --input-format stream-json
// --output-format stream-json`. One process per session; user messages written to stdin while a
// turn runs are injected into that turn (steering).
package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/daffirds/bailey-cc-bridge/internal/config"
	"github.com/daffirds/bailey-cc-bridge/internal/engine"
	"github.com/daffirds/bailey-cc-bridge/internal/proc"
)

type Engine struct{}

func (Engine) Start(_ context.Context, spec engine.Spec) (engine.Session, error) {
	if err := os.MkdirAll(spec.StateDir, 0o700); err != nil {
		return nil, err
	}
	promptFile := filepath.Join(spec.StateDir, "claude-system-prompt.md")
	if err := os.WriteFile(promptFile, []byte(spec.Instructions), 0o600); err != nil {
		return nil, err
	}
	mcpFile := filepath.Join(spec.StateDir, "claude-mcp.json")
	mcpCfg, _ := json.MarshalIndent(map[string]any{"mcpServers": map[string]any{
		spec.MCP.Name: map[string]any{"type": "http", "url": spec.MCP.URL},
	}}, "", "  ")
	if err := os.WriteFile(mcpFile, mcpCfg, 0o600); err != nil {
		return nil, err
	}

	// Valued flags use the --flag=value form so a value can never be parsed as another flag.
	help := helpText(spec.Binary)
	args := []string{"-p", "--input-format=stream-json", "--output-format=stream-json", "--verbose"}
	if strings.Contains(help, "--append-system-prompt-file") {
		args = append(args, "--append-system-prompt-file="+promptFile)
	} else {
		args = append(args, "--append-system-prompt="+spec.Instructions)
	}
	// The WhatsApp tools are pre-approved: they must never wait for a permission prompt.
	args = append(args, "--mcp-config="+mcpFile, "--allowedTools=mcp__"+spec.MCP.Name)
	switch spec.Autonomy {
	case config.AutonomyFull:
		args = append(args, "--permission-mode=bypassPermissions")
	case config.AutonomyAuto:
		args = append(args, "--permission-mode=auto")
	default:
		mode := "default" // renamed "manual" in newer versions
		if strings.Contains(help, `"manual"`) {
			mode = "manual"
		}
		args = append(args, "--permission-mode="+mode)
	}
	if spec.Autonomy != config.AutonomyFull && strings.Contains(help, "--permission-prompts") {
		// Nobody is at the keyboard: anything that would prompt is denied, not left hanging.
		args = append(args, "--permission-prompts=none")
	}
	if spec.Model != "" {
		args = append(args, "--model="+spec.Model)
	}
	if spec.Effort != "" && strings.Contains(help, "--effort") {
		args = append(args, "--effort="+spec.Effort)
	}
	for _, d := range spec.AddDirs {
		args = append(args, "--add-dir="+d)
	}
	id := spec.ResumeID
	if id != "" {
		args = append(args, "--resume="+id)
	} else {
		id = uuid.NewString()
		args = append(args, "--session-id="+id)
	}

	cmd := exec.Command(spec.Binary, args...)
	cmd.Dir = spec.Workdir
	cmd.Env = childEnv(spec)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	s := &session{
		id:     id,
		spec:   spec,
		stdin:  stdin,
		events: make(chan engine.Event, 256),
		done:   make(chan struct{}),
	}
	cmd.Stderr = &s.stderr
	g, err := proc.Start(cmd)
	if err != nil {
		return nil, fmt.Errorf("start claude: %w", err)
	}
	s.group = g
	spec.Log.Debug("claude started", "pid", cmd.Process.Pid, "session", id, "resume", spec.ResumeID != "")

	readDone := make(chan struct{})
	go func() { s.read(stdout); close(readDone) }()
	go func() {
		<-readDone // stdout must be drained before Wait closes it
		if err := cmd.Wait(); err != nil {
			s.setErr(fmt.Errorf("claude exited: %w%s", err, s.stderr.Suffix()))
		}
		s.mu.Lock()
		s.closed = true
		close(s.events)
		s.mu.Unlock()
		close(s.done)
	}()
	return s, nil
}

type session struct {
	id     string
	spec   engine.Spec
	group  *proc.Group
	stdin  io.WriteCloser
	stderr engine.StderrTail

	writeMu sync.Mutex

	mu          sync.Mutex
	busy        bool
	stateFrames bool   // the CLI reports session_state_changed (idle = really done)
	turnErr     string // error from the latest result, reported when the turn ends
	closed      bool
	err         error
	events      chan engine.Event
	done        chan struct{}
}

func (s *session) ID() string                  { return s.id }
func (s *session) Events() <-chan engine.Event { return s.events }
func (s *session) Done() <-chan struct{}       { return s.done }

func (s *session) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *session) setErr(err error) {
	s.mu.Lock()
	if s.err == nil {
		s.err = err
	}
	s.mu.Unlock()
}

func (s *session) emit(ev engine.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	select {
	case s.events <- ev:
	default: // the consumer is far behind; drop rather than wedge the CLI reader
	}
}

// markBusy flips to busy and announces a turn if we weren't already in one.
func (s *session) markBusy() {
	s.mu.Lock()
	was := s.busy
	s.busy = true
	s.mu.Unlock()
	if !was {
		s.emit(engine.Event{Kind: engine.TurnStarted})
	}
}

// finishTurn flips to idle and announces the end of the turn.
func (s *session) finishTurn() {
	s.mu.Lock()
	was, errText := s.busy, s.turnErr
	s.busy, s.turnErr = false, ""
	s.mu.Unlock()
	if was {
		s.emit(engine.Event{Kind: engine.TurnCompleted, Error: errText})
	}
}

func (s *session) writeLine(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	s.spec.Transcript.Write("out", b)
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	select {
	case <-s.done:
		return engine.ErrClosed
	default:
	}
	_, err = s.stdin.Write(append(b, '\n'))
	return err
}

func (s *session) Send(_ context.Context, text string) error {
	s.markBusy()
	return s.writeLine(map[string]any{
		"type":               "user",
		"session_id":         s.id,
		"parent_tool_use_id": nil,
		// Deliver chat text verbatim: no slash-command dispatch, no @file expansion.
		"client_composed": true,
		"message":         map[string]any{"role": "user", "content": text},
	})
}

func (s *session) Close() error {
	s.writeMu.Lock()
	_ = s.stdin.Close() // EOF lets claude finish and exit on its own
	s.writeMu.Unlock()
	s.group.Stop(s.done, 15*time.Second)
	<-s.done
	return nil
}

type streamLine struct {
	Type            string          `json:"type"`
	Subtype         string          `json:"subtype"`
	State           string          `json:"state"` // session_state_changed: running | idle | requires_action
	Result          string          `json:"result"`
	IsError         bool            `json:"is_error"`
	TerminalReason  string          `json:"terminal_reason"`
	QueuedTurnCount int             `json:"queued_turn_count"`
	RequestID       string          `json:"request_id"`
	Request         json.RawMessage `json:"request"`
	Message         *struct {
		Content []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
	} `json:"message"`
	MCPServers []struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	} `json:"mcp_servers"`
	PermissionDenials []struct {
		ToolName string `json:"tool_name"`
	} `json:"permission_denials"`
}

func (s *session) read(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 64<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		s.spec.Transcript.Write("in", line)
		var m streamLine
		if err := json.Unmarshal(line, &m); err != nil {
			continue
		}
		s.handle(&m)
	}
}

func (s *session) handle(m *streamLine) {
	switch m.Type {
	case "system":
		switch m.Subtype {
		case "init":
			for _, srv := range m.MCPServers {
				if srv.Name == s.spec.MCP.Name && srv.Status != "connected" {
					s.emit(engine.Event{Kind: engine.Notice, Text: fmt.Sprintf("whatsapp tools MCP server status: %s", srv.Status)})
				}
			}
		case "session_state_changed":
			s.mu.Lock()
			s.stateFrames = true
			s.mu.Unlock()
			switch m.State {
			case "running":
				s.markBusy()
			case "idle":
				s.finishTurn()
			}
		}
	case "assistant":
		s.markBusy()
		if m.Message == nil {
			return
		}
		for _, c := range m.Message.Content {
			switch c.Type {
			case "text":
				if strings.TrimSpace(c.Text) != "" {
					s.emit(engine.Event{Kind: engine.AssistantText, Text: c.Text})
				}
			case "tool_use":
				s.emit(engine.Event{Kind: engine.ToolCall, Text: c.Name + " " + compact(c.Input, 300)})
			}
		}
	case "user":
		s.markBusy()
	case "result":
		if len(m.PermissionDenials) > 0 {
			var names []string
			for _, d := range m.PermissionDenials {
				names = append(names, d.ToolName)
			}
			s.emit(engine.Event{Kind: engine.Notice, Text: "permission denied for: " + strings.Join(names, ", ")})
		}
		errText := ""
		if m.IsError || (m.Subtype != "" && m.Subtype != "success") {
			errText = firstNonEmpty(m.Result, m.TerminalReason, m.Subtype)
		}
		s.mu.Lock()
		stateFrames, busy := s.stateFrames, s.busy
		if errText != "" {
			s.turnErr = errText
		}
		s.mu.Unlock()
		switch {
		case stateFrames && !busy && errText != "":
			// "idle" arrived just before this result; still surface the failure.
			s.emit(engine.Event{Kind: engine.TurnCompleted, Error: errText})
		case stateFrames:
			// A result ends one turn, not the run: wait for session_state_changed=idle.
		case m.QueuedTurnCount > 0 || strings.HasPrefix(m.TerminalReason, "aborted_"):
			// More queued input will run as another turn (or this was a steering split).
		default:
			s.finishTurn()
		}
	case "control_request":
		s.answerControl(m)
	}
}

// answerControl replies to requests the CLI sends its host. The bridge runs unattended, so
// permission requests are denied instead of being left to hang the session.
func (s *session) answerControl(m *streamLine) {
	var req struct {
		Subtype  string `json:"subtype"`
		ToolName string `json:"tool_name"`
	}
	_ = json.Unmarshal(m.Request, &req)
	resp := map[string]any{"subtype": "error", "request_id": m.RequestID, "error": "not supported by bailey-cc-bridge"}
	if req.Subtype == "can_use_tool" {
		resp = map[string]any{"subtype": "success", "request_id": m.RequestID, "response": map[string]any{
			"behavior": "deny",
			"message":  "Denied: this session runs unattended from WhatsApp and nobody can approve tool use. Choose another approach or ask in the chat.",
		}}
		s.emit(engine.Event{Kind: engine.Notice, Text: "denied permission request for " + req.ToolName})
	}
	_ = s.writeLine(map[string]any{"type": "control_response", "response": resp})
}

var helpCache sync.Map

// helpText caches `claude --help` so flags can be feature-detected across CLI versions.
func helpText(bin string) string {
	if v, ok := helpCache.Load(bin); ok {
		return v.(string)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(ctx, bin, "--help").CombinedOutput()
	helpCache.Store(bin, string(out))
	return string(out)
}

// childEnv prepares the CLI's environment:
//   - drops markers that make Claude Code think it is nested in another session;
//   - drops API-key variables so the subscription login is used;
//   - asks for session_state_changed frames, the reliable "turn is over" signal.
func childEnv(spec engine.Spec) []string {
	drop := map[string]bool{
		"CLAUDECODE": true, "CLAUDE_CODE_ENTRYPOINT": true, "CLAUDE_CODE_SSE_PORT": true,
		"ANTHROPIC_API_KEY": true, "ANTHROPIC_AUTH_TOKEN": true, "CLAUDE_CODE_SIMPLE": true,
		"PWD": true,
	}
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if !drop[strings.ToUpper(k)] {
			env = append(env, kv)
		}
	}
	return append(env, "CLAUDE_CODE_SDK_READS_SESSION_STATE=1", "PWD="+spec.Workdir)
}

// SessionExists checks Claude Code's transcript store, so --resume is only tried when it can work.
func SessionExists(id string) bool {
	dir := os.Getenv("CLAUDE_CONFIG_DIR")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".claude")
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "projects", "*", id+".jsonl"))
	return len(matches) > 0
}

func compact(raw json.RawMessage, n int) string {
	s := strings.Join(strings.Fields(string(raw)), " ")
	if len(s) > n {
		s = s[:n] + "…"
	}
	return s
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
