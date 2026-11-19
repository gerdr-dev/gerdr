package hub

import (
	"context"
	"encoding/json"
	"testing"
)

// A message of several text blocks is one string in Claude's transcript (blocks joined with nothing between), so
// userTextSet must know it in that form too, for messages sent from here and ones replayed from the transcript.
// Seen: a message with an uploaded file counted as "continued elsewhere (1 message)" on the check after a restart.
// The file is logged as a resource_link (what the Composer sends), which the claude adapter turns into "@path".
func TestUserTextSetJoinedBlocks(t *testing.T) {
	sent := &Event{Kind: EvUser, Content: []json.RawMessage{
		json.RawMessage(`{"type":"resource_link","name":"a b.mov","uri":"file:///tmp/a b.mov"}`),
		json.RawMessage(`{"type":"text","text":"what is wrong here"}`),
	}}
	chunk := func(text string) *Event {
		b, _ := json.Marshal(map[string]any{"sessionUpdate": "user_message_chunk", "messageId": "m2", "content": map[string]string{"type": "text", "text": text}})
		return &Event{Kind: EvUpdate, Update: b}
	}
	link, _ := json.Marshal(map[string]any{"sessionUpdate": "user_message_chunk", "messageId": "m3", "content": map[string]string{"type": "resource_link", "name": "c.mov", "uri": "file:///tmp/c.mov"}})
	words, _ := json.Marshal(map[string]any{"sessionUpdate": "user_message_chunk", "messageId": "m3", "content": map[string]string{"type": "text", "text": "and this"}})
	s := &Session{loaded: true, events: []*Event{sent, chunk("[Image #1] look"), chunk("[Image #1]"), {Kind: EvUpdate, Update: link}, {Kind: EvUpdate, Update: words}}}
	got, _ := s.userTextSet()
	for _, want := range []string{"@/tmp/a b.movwhat is wrong here", "[Image #1] look[Image #1]", "@/tmp/c.movand this"} {
		if !got[normText(want)] {
			t.Errorf("%q not known; have %v", want, got)
		}
	}
}

// A message sent from here is known by its messageId (Claude stores it as the record's uuid), whatever the adapter
// made of its text; replayed messages are known by the uuid they were replayed under.
func TestUserTextSetIDs(t *testing.T) {
	sent := &Event{Kind: EvUser, MessageID: "a1", Content: []json.RawMessage{json.RawMessage(`{"type":"text","text":"hi"}`)}}
	b, _ := json.Marshal(map[string]any{"sessionUpdate": "user_message_chunk", "messageId": "b2", "content": map[string]string{"type": "text", "text": "yo"}})
	s := &Session{loaded: true, events: []*Event{sent, {Kind: EvUpdate, Update: b}}}
	_, ids := s.userTextSet()
	if !ids["a1"] || !ids["b2"] || len(ids) != 2 {
		t.Fatalf("ids = %v", ids)
	}
}

// A session started in Gerdr has no elsewhere: CatchUp returns before asking the agent anything (no hub here, so
// reaching the agent would panic).
func TestCatchUpSkipsGerdrOnly(t *testing.T) {
	s := &Session{rec: Record{GerdrOnly: true, SyncedSize: 1}}
	s.CatchUp(context.Background())
}
