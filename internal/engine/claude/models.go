package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/daffirds/bailey-cc-bridge/internal/engine"
	"github.com/daffirds/bailey-cc-bridge/internal/proc"
)

// Models asks Claude Code which models it offers: the initialize request of its stream-json
// mode answers with the same list as /model. No conversation is started.
func (Engine) Models(ctx context.Context, bin string) ([]engine.Model, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	// No MCP servers: they would only slow the start down.
	cmd := exec.Command(bin, "-p", "--input-format=stream-json", "--output-format=stream-json", "--verbose", "--strict-mcp-config")
	cmd.Dir = os.TempDir()
	cmd.Env = childEnv(engine.Spec{Workdir: cmd.Dir})
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	g, err := proc.Start(cmd)
	if err != nil {
		return nil, fmt.Errorf("start claude: %w", err)
	}
	defer func() {
		_ = stdin.Close()
		g.Kill()
		_ = cmd.Wait()
	}()
	go func() {
		<-ctx.Done()
		g.Kill()
	}()
	if _, err := io.WriteString(stdin, `{"type":"control_request","request_id":"models","request":{"subtype":"initialize"}}`+"\n"); err != nil {
		return nil, err
	}
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64*1024), 16<<20)
	for sc.Scan() {
		if models, ok, err := parseModels(sc.Bytes()); ok {
			return models, err
		}
	}
	if ctx.Err() != nil {
		return nil, errors.New("claude did not list its models in time")
	}
	return nil, errors.New("claude exited without listing its models")
}

// parseModels reads the answer to the initialize request from one stdout line; ok is false for
// any other line.
func parseModels(line []byte) (models []engine.Model, ok bool, err error) {
	var m struct {
		Type     string `json:"type"`
		Response struct {
			RequestID string `json:"request_id"`
			Error     string `json:"error"`
			Response  struct {
				Models []struct {
					Value                 string   `json:"value"`
					DisplayName           string   `json:"displayName"`
					Description           string   `json:"description"`
					SupportedEffortLevels []string `json:"supportedEffortLevels"`
				} `json:"models"`
			} `json:"response"`
		} `json:"response"`
	}
	if json.Unmarshal(line, &m) != nil || m.Type != "control_response" || m.Response.RequestID != "models" {
		return nil, false, nil
	}
	if m.Response.Error != "" {
		return nil, true, errors.New("claude: " + m.Response.Error)
	}
	for _, c := range m.Response.Response.Models {
		id := c.Value
		if id == "default" { // what the CLI picks when no model is set
			id = ""
		}
		models = append(models, engine.Model{ID: id, Name: c.DisplayName, Description: c.Description, Efforts: c.SupportedEffortLevels})
	}
	return models, true, nil
}
