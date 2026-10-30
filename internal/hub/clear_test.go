package hub

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func msg(id, text string) *Event {
	e := userEv(text)
	e.MessageID = id
	return e
}

// The /clear turn: the command, the turn, the notice the hub writes when the agent reports the new id.
func clearTurn(id string) []*Event {
	return []*Event{msg(id, "/clear"), {Kind: EvTurnStart, MessageID: id}, {Kind: EvNotice, Level: "clear"}, {Kind: EvTurnEnd}}
}

func texts(evs []*Event) []string {
	var out []string
	for _, e := range evs {
		if e.Kind == EvUser {
			out = append(out, contentText(e.Content))
		}
	}
	return out
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func byAgentID(h *Hub, id string) *Session {
	for _, s := range h.sessions {
		if s.rec.AgentSessionID == id {
			return s
		}
	}
	return nil
}

// A /clear moves the conversation before it into a session of its own (old agent id, old title); the tab keeps the
// /clear turn onwards under the new id.
func TestClearSplitsOff(t *testing.T) {
	h, err := New(Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	s := newSession(h, Record{ID: "tab", Agent: "claude", Cwd: "/w", AgentSessionID: "old", Title: "修登录页", CustomTitle: true})
	h.register(s)
	s.events = []*Event{msg("m1", "修一下登录页"), {Kind: EvTurnStart, MessageID: "m1"}, {Kind: EvTurnEnd, TS: 5}, msg("m2", "/clear"), {Kind: EvTurnStart, MessageID: "m2"}}
	s.turnMsg = "m2"
	prev := s.rec
	h.rekey(s, "new")
	h.splitOffCleared(s, prev)

	if got := texts(s.events); !eq(got, []string{"/clear"}) {
		t.Fatalf("tab kept %q", got)
	}
	o := byAgentID(h, "old")
	if o == nil || o == s {
		t.Fatal("no session for the old agent id")
	}
	if got := texts(o.events); !eq(got, []string{"修一下登录页"}) || o.rec.Title != "修登录页" || !o.rec.CustomTitle || o.rec.UpdatedAt != 5 {
		t.Fatalf("old session: %q %+v", got, o.rec)
	}
	if h.byAgent["claude:old"] != o || h.byAgent["claude:new"] != s {
		t.Fatal("byAgent not updated")
	}
	if evs, _ := readLog(o.lf.path); len(evs) != 3 {
		t.Fatalf("old log on disk has %d events", len(evs))
	}
}

// Indexes from the Record.Cleared days: the case users hit. Tab A cleared a1 then a2; a2 was later resumed (session C
// runs it); a1 was resumed into B, which replayed it and cleared it again — so a1 sat on two lists.
func TestMigrateCleared(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "sessions"), 0o700)
	write := func(id string, evs ...[]*Event) {
		f, _ := os.Create(filepath.Join(dir, "sessions", id+".jsonl"))
		defer f.Close()
		for _, part := range evs {
			for _, e := range part {
				b, _ := json.Marshal(e)
				f.Write(append(b, '\n'))
			}
		}
	}
	write("A", []*Event{msg("x0", "更早的事")}, clearTurn("c0"), []*Event{msg("x1", "第一件事")}, clearTurn("c1"),
		[]*Event{msg("x2", "第二件事")}, clearTurn("c2"), []*Event{msg("x3", "现在的事")})
	write("B", []*Event{msg("r1", "第一件事（重放）")}, clearTurn("c3"), []*Event{msg("y", "B 现在的事")})
	write("C", []*Event{msg("z", "第二件事（重放）")})
	recs := []Record{
		{ID: "A", Agent: "claude", AgentSessionID: "a3", Title: "现在", CreatedAt: 1, Cleared: []ClearedCtx{{ID: "a1", Title: "一", At: 100}, {ID: "a2", Title: "二", At: 200}}},
		{ID: "B", Agent: "claude", AgentSessionID: "b2", Title: "B", CreatedAt: 2, Cleared: []ClearedCtx{{ID: "a1", Title: "一", At: 300}}},
		{ID: "C", Agent: "claude", AgentSessionID: "a2", Title: "二", CreatedAt: 3},
	}
	b, _ := json.Marshal(recs)
	os.WriteFile(filepath.Join(dir, "sessions", "index.json"), b, 0o600)

	h, err := New(Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(h.sessions); n != 4 {
		t.Fatalf("%d sessions, want A B C + one for a1", n)
	}
	a1 := byAgentID(h, "a1")
	if a1 == nil || a1.rec.Title != "一" {
		t.Fatal("a1 didn't become a session")
	}
	if got := texts(a1.events); !eq(got, []string{"第一件事（重放）"}) {
		t.Fatalf("a1 should come from the latest clear (B): %q", got)
	}
	if got := texts(h.sessions["A"].events); !eq(got, []string{"/clear", "现在的事"}) {
		t.Fatalf("A kept %q", got)
	}
	if got := texts(h.sessions["B"].events); !eq(got, []string{"/clear", "B 现在的事"}) {
		t.Fatalf("B kept %q", got)
	}
	for _, s := range h.sessions {
		if len(s.rec.Cleared) > 0 {
			t.Fatalf("%s still has Cleared", s.rec.ID)
		}
	}

	// Saved: loading again changes nothing
	h2, err := New(Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(h2.sessions) != 4 || !eq(texts(h2.sessions["A"].events), []string{"/clear", "现在的事"}) {
		t.Fatal("second load differs")
	}
}
