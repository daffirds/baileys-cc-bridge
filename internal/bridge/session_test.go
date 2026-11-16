package bridge

import (
	"context"
	"testing"
	"time"

	"github.com/daffirds/bailey-cc-bridge/internal/whatsapp"
)

func TestCollectWaitsForABurstWhenIdle(t *testing.T) {
	s := &session{inbox: make(chan *whatsapp.Message, 8), ctx: context.Background()}
	go func() {
		for _, id := range []string{"2", "3"} {
			time.Sleep(400 * time.Millisecond)
			s.inbox <- &whatsapp.Message{ID: id}
		}
	}()
	start := time.Now()
	if batch := s.collect(&whatsapp.Message{ID: "1"}); len(batch) != 3 {
		t.Fatalf("idle: got %d messages, want the whole burst of 3", len(batch))
	}
	if took := time.Since(start); took < 800*time.Millisecond+settle {
		t.Errorf("returned after %v, before the chat went quiet", took)
	}
}

func TestCollectSteersRightAwayMidTurn(t *testing.T) {
	s := &session{inbox: make(chan *whatsapp.Message, 8), ctx: context.Background(), busy: true}
	s.inbox <- &whatsapp.Message{ID: "2"}
	start := time.Now()
	if batch := s.collect(&whatsapp.Message{ID: "1"}); len(batch) != 2 {
		t.Fatalf("busy: got %d messages, want 2", len(batch))
	}
	if took := time.Since(start); took > 100*time.Millisecond {
		t.Errorf("busy collect waited %v", took)
	}
}
