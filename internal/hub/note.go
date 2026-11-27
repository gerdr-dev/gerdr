package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"strings"
	"time"

	"github.com/coder/acp-go-sdk"
	"github.com/google/uuid"

	"github.com/gerdr-dev/gerdr/acpx"
)

// Notes: something code wants a conversation's agent to hear, not worth a turn of its own, so it rides in front of
// the next message the conversation gets, whoever sends it — the person typing in the chat included (ctx.sessions.note).
//
// Why: a panel told an agent "the user ignored your question" with a send of its own the moment the person typed
// something, which landed as a second message after the person's and, when the turn had ended meanwhile, started a
// turn of its own (user report: "it should go along with what I send"). Keyed, so a note can be replaced or taken
// back before it goes. Kept in the Record, so a restart doesn't lose one.

// notePrefix starts the block. The chat draws it as a one-line notice (ui transcript.ts, NOTE); titles and "what the
// person said" skip it (injectedText).
const notePrefix = "<gerdr-note"

// Note is one waiting note (Record.Notes).
type Note struct {
	Key  string `json:"key"`
	Text string `json:"text"`
	// Fold: the line the chat shows for it; the agent gets Text.
	Fold string `json:"fold,omitempty"`
	At   int64  `json:"at"`
}

// SetNote replaces a pending note with the same key. Empty text takes it back.
func (s *Session) SetNote(key, text, fold string) {
	s.DeliverNote(context.Background(), key, text, fold, false)
}

// DeliverNote can try the active turn first. The fallback is the same persistent note as SetNote.
func (s *Session) DeliverNote(ctx context.Context, key, text, fold string, urgent bool) {
	n := Note{Key: strings.TrimSpace(key), Text: strings.TrimSpace(text), Fold: strings.TrimSpace(fold), At: now()}
	s.noteMu.Lock()
	defer s.noteMu.Unlock()
	if urgent && n.Text != "" && s.steerNote(ctx, n) {
		n.Text = ""
	}
	s.mu.Lock()
	var kept []Note
	for _, old := range s.rec.Notes {
		if old.Key != n.Key {
			kept = append(kept, old)
		}
	}
	if n.Text != "" {
		kept = append(kept, n)
	}
	s.rec.Notes = kept
	s.mu.Unlock()
	s.h.saveIndex()
}

// Only talk to the already attached host, and only if its steering protocol itself rejects a finished turn.
// Claude Code accepts a user message on stdin even if it just went idle, so its steering cannot promise this.
func (s *Session) steerNote(ctx context.Context, n Note) bool {
	s.h.mu.Lock()
	s.mu.Lock()
	running := s.running && s.attached && !s.infer && !s.shut && s.stopping == nil
	c, sid := s.h.hosts[s.rec.Agent], s.rec.AgentSessionID
	s.mu.Unlock()
	s.h.mu.Unlock()
	if !running || c == nil || !c.SteerActiveOnly() {
		return false
	}
	block := acp.TextBlock(noteText(n))
	raw, _ := json.Marshal(block)
	e := &Event{Kind: EvUser, MessageID: uuid.NewString(), Content: []json.RawMessage{raw}, Steer: true, Now: true}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	outcome, err := s.steer(ctx, c, sid, []acp.ContentBlock{block}, e, false)
	return err == nil && outcome == acpx.SteerInjected
}

// Notes: the notes waiting for the next message, oldest first.
func (s *Session) Notes() []Note {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Note(nil), s.rec.Notes...)
}

// takeNotes: the blocks for the notes waiting now, and the notes themselves (dropNotes them once the message went).
func (s *Session) takeNotes() ([]acp.ContentBlock, []Note) {
	s.noteMu.Lock()
	defer s.noteMu.Unlock()
	notes := s.Notes()
	var out []acp.ContentBlock
	for _, n := range notes {
		out = append(out, acp.TextBlock(noteText(n)))
	}
	return out, notes
}

// dropNotes removes notes that went out; one replaced meanwhile (same key, new text) stays for the next message.
func (s *Session) dropNotes(sent []Note) {
	if len(sent) == 0 {
		return
	}
	s.mu.Lock()
	var kept []Note
	for _, n := range s.rec.Notes {
		gone := false
		for _, x := range sent {
			if x == n {
				gone = true
				break
			}
		}
		if !gone {
			kept = append(kept, n)
		}
	}
	s.rec.Notes = kept
	s.mu.Unlock()
	s.h.saveIndex()
}

// noteText is the block: the fold as an attribute the chat shows, the text for the agent.
func noteText(n Note) string {
	fold := n.Fold
	if fold == "" {
		fold = firstLine([]acp.ContentBlock{acp.TextBlock(n.Text)}, 80)
	}
	return fmt.Sprintf("%s fold=\"%s\">\n%s\n</gerdr-note>", notePrefix, html.EscapeString(fold), n.Text)
}
