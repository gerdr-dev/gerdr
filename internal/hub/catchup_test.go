package hub

import (
	"encoding/json"
	"testing"
)

// A message of several text blocks is one string in Claude's transcript (blocks joined with nothing between), so
// userTextSet must know it in that form too, for messages sent from here and ones replayed from the transcript.
// Seen: a message with an uploaded file ("@path" block + the words) counted as "continued elsewhere (1 message)"
// on the check after a restart.
func TestUserTextSetJoinedBlocks(t *testing.T) {
	sent := &Event{Kind: EvUser, Content: []json.RawMessage{
		json.RawMessage(`{"type":"text","text":"@/tmp/a.mp4"}`),
		json.RawMessage(`{"type":"text","text":"what is wrong here"}`),
	}}
	chunk := func(text string) *Event {
		b, _ := json.Marshal(map[string]any{"sessionUpdate": "user_message_chunk", "messageId": "m2", "content": map[string]string{"type": "text", "text": text}})
		return &Event{Kind: EvUpdate, Update: b}
	}
	s := &Session{loaded: true, events: []*Event{sent, chunk("[Image #1] look"), chunk("[Image #1]")}}
	got := s.userTextSet()
	for _, want := range []string{"@/tmp/a.mp4what is wrong here", "[Image #1] look[Image #1]"} {
		if !got[normText(want)] {
			t.Errorf("%q not known; have %v", want, got)
		}
	}
}
