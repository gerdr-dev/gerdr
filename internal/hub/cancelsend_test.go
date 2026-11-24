package hub

import (
	"context"
	"errors"
	"testing"

	"github.com/coder/acp-go-sdk"
	"github.com/google/uuid"
)

// A message called off before its send got through never reaches the agent or the log; one already in can't be.
func TestCancelSend(t *testing.T) {
	s, f := startFake(t, false)
	close(f.release)
	msg := []acp.ContentBlock{acp.TextBlock("hello")}

	// Called off before the request arrived (a slow upload, a retry): refused when it comes
	early := uuid.NewString()
	if !s.CancelSend(early) {
		t.Fatal("cancel before arrival refused")
	}
	if _, err := s.SendWith(context.Background(), msg, SendOpts{ClientID: early}); !errors.Is(err, ErrSendCancelled) {
		t.Fatalf("send after cancel: %v", err)
	}
	for _, e := range s.Events() {
		if e.Kind == EvUser {
			t.Fatal("a called-off message was logged")
		}
	}

	// Called off while waiting at the commit point (here: by marking it the way a concurrent CancelSend does)
	mid := uuid.NewString()
	s.CancelSend(mid)
	if s.commitSend(mid) {
		t.Fatal("commit after cancel went through")
	}

	// In: too late
	sent := uuid.NewString()
	if _, err := s.SendWith(context.Background(), msg, SendOpts{ClientID: sent}); err != nil {
		t.Fatal(err)
	}
	waitPrompt(t, f)
	waitIdle(t, s)
	if s.CancelSend(sent) {
		t.Fatal("cancelled a message that is already in")
	}
	if len(s.sendState) != 0 {
		t.Fatalf("leftover send state: %v", s.sendState)
	}
}
