package server

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/gerdr-dev/gerdr/internal/config"
)

const spacesLayout = `{"version":1,"active":"w1","spaces":[{"id":"default","name":"default"},{"id":"s2","name":"other"}],"workspaces":[
 {"id":"w1","name":"a","activeTab":"t1","tabs":[{"id":"t1","focus":"p1","root":{"type":"split","id":"x","dir":"row","ratio":0.5,
   "a":{"type":"pane","id":"p1","sessionId":"A"},"b":{"type":"pane","id":"p2","sessionId":"B"}}}]},
 {"id":"w2","name":"b","space":"s2","activeTab":"t2","tabs":[{"id":"t2","focus":"p3","root":{"type":"pane","id":"p3","sessionId":"C"}}]}
]}`

func TestSessionSpaces(t *testing.T) {
	want := map[string]string{"A": "default", "B": "default", "C": "s2"}
	for name, raw := range map[string]string{"plain": spacesLayout, "wrapped": `{"data":` + spacesLayout + `}`} {
		got := SessionSpaces(json.RawMessage(raw))
		if len(got) != len(want) {
			t.Fatalf("%s: got %v, want %v", name, got, want)
		}
		for k, v := range want {
			if got[k] != v {
				t.Errorf("%s: %s in %q, want %q", name, k, got[k], v)
			}
		}
	}
}

func TestCrossSpace(t *testing.T) {
	st := config.NewStore(filepath.Join(t.TempDir(), "layout.json"), `null`)
	if err := st.Set(json.RawMessage(spacesLayout)); err != nil {
		t.Fatal(err)
	}
	s := &Server{Layout: st}
	for _, c := range []struct {
		from, to string
		ok       bool
	}{
		{"A", "B", true},  // same session
		{"A", "C", false}, // across sessions
		{"C", "A", false},
		{"A", "Z", true}, // target closed (no pane): belongs to no session
		{"", "C", true},  // not from an agent (the UI)
	} {
		if err := s.crossSpace(c.from, c.to); (err == nil) != c.ok {
			t.Errorf("%s → %s: err = %v, want ok=%v", c.from, c.to, err, c.ok)
		}
	}
}

func TestResolveSpace(t *testing.T) {
	raw := json.RawMessage(`{"version":1,"active":"w1","workspaces":[
 {"id":"w1","tabs":[{"id":"t1","root":{"type":"pane","id":"p1","sessionId":"A"}}]},
 {"id":"w2","space":"客户 A","tabs":[{"id":"t2","root":{"type":"split","a":{"type":"pane","id":"p2","sessionId":"C"},"b":{"type":"pane","id":"p3","term":{"id":"tm1","cwd":"/"}}}}]}
]}`)
	wp := func(dir string) (string, bool) {
		if dir == "/g/projects/客户 A/work" {
			return "客户 A", true
		}
		return "", false
	}
	for _, c := range []struct {
		name string
		who  Caller
		want string
	}{
		{"agent in a pane", Caller{Session: "C"}, "客户 A"},
		{"pane beats an old cwd", Caller{Session: "A", SessionCwd: "/g/projects/客户 A/work"}, "default"},
		{"Work agent: no pane, by its Work directory", Caller{Session: "W", SessionCwd: "/g/projects/客户 A/work"}, "客户 A"},
		{"terminal in a pane", Caller{Term: "tm1"}, "客户 A"},
		{"--project wins", Caller{Session: "C", Project: "ccc"}, "ccc"},
		{"outside gerdr: not scoped", Caller{}, ""},
		{"unknown terminal", Caller{Term: "gone"}, ""},
	} {
		if got := ResolveSpace(raw, c.who, wp); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	if got := TermSpaces(raw); got["tm1"] != "客户 A" {
		t.Errorf("TermSpaces = %v", got)
	}
}
