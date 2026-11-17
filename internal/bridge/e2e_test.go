package bridge

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/daffirds/bailey-cc-bridge/internal/config"
	"github.com/daffirds/bailey-cc-bridge/internal/tools"
	"github.com/daffirds/bailey-cc-bridge/internal/whatsapp"
)

// fakeWA stands in for WhatsApp: it records what the agent sends.
type fakeWA struct {
	seq  atomic.Int64
	sent chan string
}

func (f *fakeWA) id() string { return fmt.Sprintf("OUT%d", f.seq.Add(1)) }

func (f *fakeWA) SendText(_ context.Context, _, text string, o whatsapp.TextOptions) (string, error) {
	f.sent <- "text:" + text
	return f.id(), nil
}

func (f *fakeWA) SendMedia(_ context.Context, _ string, m whatsapp.OutMedia) (string, error) {
	f.sent <- "media:" + m.Kind
	return f.id(), nil
}

func (f *fakeWA) React(_ context.Context, id, emoji string) (string, error) {
	f.sent <- "react:" + id + ":" + emoji
	return f.id(), nil
}

func (f *fakeWA) ChatInfo(_ context.Context, chat string) (*whatsapp.ChatInfo, error) {
	return &whatsapp.ChatInfo{JID: chat}, nil
}

func (f *fakeWA) Download(context.Context, string, string) error { return errors.New("no media here") }
func (f *fakeWA) Events() <-chan whatsapp.Event                  { return nil }
func (f *fakeWA) Done() <-chan struct{}                          { return nil }
func (f *fakeWA) Err() error                                     { return nil }

// TestAgentRoundTrip drives a real CLI through the bridge: a message goes in, the agent answers
// through the MCP tools, a second message is steered in while it works. It spends a little of the
// CLI's plan, so it only runs with BRIDGE_E2E=claude or BRIDGE_E2E=codex.
func TestAgentRoundTrip(t *testing.T) {
	engineName := os.Getenv("BRIDGE_E2E")
	if engineName == "" {
		t.Skip("set BRIDGE_E2E=claude or BRIDGE_E2E=codex to run a real agent")
	}
	t.Setenv("BAILEY_CC_BRIDGE_HOME", t.TempDir())
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	host := tools.NewHost(log)
	srv := httptest.NewServer(host)
	defer srv.Close()

	me := &whatsapp.Me{ID: "628880000000@s.whatsapp.net", Name: "Owner"}
	agent := config.Agent{Engine: engineName, Autonomy: config.AutonomyAuto}
	if engineName == config.EngineClaude {
		agent.Model = "haiku"
	}
	cfg := &config.Config{Chats: []config.Chat{{JID: me.ID, Name: "Me", Agent: agent}}}
	fake := &fakeWA{sent: make(chan string, 64)}
	b := New(cfg, "", fake, me, host, srv.URL, log)
	defer b.stop()

	msg := func(id, text string) *whatsapp.Message {
		return &whatsapp.Message{ID: id, Chat: me.ID, Sender: me.ID, FromMe: true, Kind: "text", Text: text, Timestamp: time.Now().Unix()}
	}
	b.route(msg("IN1", "Count from 1 to 5: send each number as its own message, one send_message call each. Then react 👍 to this message."))

	deadline := time.After(4 * time.Minute)
	var got []string
	steered := false
	for !(steered && contains(got, "text:DONE") && contains(got, "react:IN1:👍")) {
		select {
		case s := <-fake.sent:
			t.Logf("agent sent %s", s)
			got = append(got, s)
			if !steered && strings.HasPrefix(s, "text:") {
				// Mid-task, like someone typing while it works.
				b.route(msg("IN2", "Also: when you are done counting, send the single word DONE."))
				steered = true
			}
		case <-deadline:
			t.Fatalf("timed out; the agent sent %q", got)
		}
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if strings.TrimSpace(s) == want {
			return true
		}
	}
	return false
}
