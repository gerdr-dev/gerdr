package hub

import (
	"context"
	"testing"

	"github.com/coder/acp-go-sdk"
	"github.com/google/uuid"
)

// What a person typed is kept (a board shows it in a worker's feed); code's and other agents' messages are not.
func TestPersonSaid(t *testing.T) {
	s, f := startFake(t, false)
	send(t, s, "from code") // no client id: a local function's ctx.sessions.send
	close(f.release)
	waitPrompt(t, f)
	waitIdle(t, s)
	if got := s.Summary().PersonSaid; got.At != 0 {
		t.Fatalf("code's message counted as a person's: %+v", got)
	}
	if _, err := s.SendFrom(context.Background(), []acp.ContentBlock{acp.TextBlock("from an agent")}, uuid.NewString(), "other", false); err != nil {
		t.Fatal(err)
	}
	waitPrompt(t, f)
	waitIdle(t, s)
	if got := s.Summary().PersonSaid; got.At != 0 {
		t.Fatalf("another agent's message counted as a person's: %+v", got)
	}
	if _, err := s.Send(context.Background(), []acp.ContentBlock{acp.TextBlock("把审批链接发给扶摇")}, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	waitPrompt(t, f)
	if got := s.Summary().PersonSaid; got.Text != "把审批链接发给扶摇" || got.At == 0 {
		t.Fatalf("person's message: %+v", got)
	}
}
