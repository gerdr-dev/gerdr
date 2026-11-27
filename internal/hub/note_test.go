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

func TestDeliverNoteTriesActiveTurn(t *testing.T) {
	for _, tc := range []struct {
		name    string
		running bool
		safe    bool
		hold    bool
	}{
		{name: "inserted", running: true, safe: true},
		{name: "idle", safe: true},
		{name: "held by the agent", running: true, safe: true, hold: true},
		{name: "adapter cannot guarantee active turn", running: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, f := startFake(t, false)
			f.hold = tc.hold
			s.h.hosts["fake"].Info.Meta["steering"].(map[string]any)["activeOnly"] = tc.safe
			s.mu.Lock()
			s.attached, s.running = true, tc.running
			s.mu.Unlock()

			s.DeliverNote(t.Context(), "panel:ignored", "The user ignored the question.", "ignored question", true)
			inserted := tc.running && tc.safe && !tc.hold
			steers := 0
			for _, e := range s.Events() {
				if e.Kind == EvTurnStart {
					t.Fatal("note started a turn")
				}
				if e.Kind == EvUser && e.Steer && e.Now && len(e.Content) == 1 && strings.Contains(string(e.Content[0]), "The user ignored the question.") {
					steers++
				}
			}
			want := 0
			if inserted {
				want = 1
			}
			if steers != want {
				t.Fatalf("note steers: got %d, want %d", steers, want)
			}
			blocks, pending := s.takeNotes()
			if inserted {
				if len(pending) != 0 {
					t.Fatalf("inserted note still pending: %v", pending)
				}
			} else if len(blocks) != 1 || !strings.Contains(blocks[0].Text.Text, "The user ignored the question.") {
				t.Fatalf("note missing from the next message: %v", pending)
			}
		})
	}
}
