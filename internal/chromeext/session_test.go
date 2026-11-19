package chromeext

import (
	"context"
	"encoding/json"
	"testing"
)

// A page knows the agent session that opened it, and so do the pages it opens (a panel shows them beside that
// conversation: ui/src/core/SessionBrowser.tsx).
func TestTabSession(t *testing.T) {
	b := New(nil)
	send := func(context.Context, string, json.RawMessage) (json.RawMessage, error) { return nil, nil }
	page := func(opener int) int {
		return b.Adopt(Page{Project: "p", URL: "about:blank", Opener: opener, Send: send, Close: func() {}}).ID()
	}
	session := func(id int) string {
		for _, x := range b.Tabs() {
			if x.ID == id {
				return x.Session
			}
		}
		t.Fatalf("no tab %d", id)
		return ""
	}
	a := page(0)
	early := page(a) // opened during the first load, before the session was recorded
	b.SetSession(a, "s1")
	late := page(a)
	other := page(0)
	if session(a) != "s1" || session(early) != "s1" || session(late) != "s1" {
		t.Fatalf("sessions: %q %q %q", session(a), session(early), session(late))
	}
	if session(other) != "" {
		t.Fatalf("an unrelated page got %q", session(other))
	}
}
