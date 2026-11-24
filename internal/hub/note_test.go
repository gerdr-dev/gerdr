package hub

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/coder/acp-go-sdk"
	"github.com/google/uuid"
)

// A note rides in front of the next message, a person's included, and goes once; a replaced one keeps only the new
// text, an emptied one is gone. The person's words stay what the hub reports they said.
func TestNoteRidesAlong(t *testing.T) {
	s, f := startFake(t, false)
	close(f.release)
	s.SetNote("p:ignored", "old", "")
	s.SetNote("p:ignored", "They ignored your question q1.", "ignored q1")
	s.SetNote("p:gone", "x", "")
	s.SetNote("p:gone", "", "")
	if _, err := s.Send(t.Context(), []acp.ContentBlock{acp.TextBlock("中文")}, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	waitPrompt(t, f)
	var users [][]json.RawMessage
	for _, e := range s.Events() {
		if e.Kind == EvUser {
			users = append(users, e.Content)
		}
	}
	if len(users) != 1 || len(users[0]) != 2 {
		t.Fatalf("want one message of two blocks, got %s", users)
	}
	var b struct{ Text string }
	json.Unmarshal(users[0][0], &b)
	if !strings.HasPrefix(b.Text, `<gerdr-note fold="ignored q1">`) || !strings.Contains(b.Text, "They ignored your question q1.") || strings.Contains(b.Text, "\nold\n") {
		t.Fatalf("note block: %q", b.Text)
	}
	if got := contentText(users[0]); got != "中文" {
		t.Errorf("the person said %q", got)
	}
	if n := s.Notes(); len(n) != 0 {
		t.Fatalf("notes left after they went: %v", n)
	}
}
