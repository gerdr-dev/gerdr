package hub

import (
	"context"
	"testing"

	"github.com/coder/acp-go-sdk"
)

// A sender's fold summary is logged on the user event (the chat draws the message folded to it); the agent still
// gets the whole text.
func TestSendFold(t *testing.T) {
	s, f := startFake(t, false)
	close(f.release)
	if _, err := s.SendWith(context.Background(), []acp.ContentBlock{acp.TextBlock("[消息 c21] a long routing input")}, SendOpts{Fold: "  Slack · 林刚：好  "}); err != nil {
		t.Fatal(err)
	}
	waitPrompt(t, f)
	waitIdle(t, s)
	var fold string
	for _, e := range s.Events() {
		if e.Kind == EvUser {
			fold = e.Fold
		}
	}
	if fold != "Slack · 林刚：好" {
		t.Fatalf("fold on the event: %q", fold)
	}
}
