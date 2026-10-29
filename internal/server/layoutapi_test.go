package server

import (
	"encoding/json"
	"strings"
	"testing"
)

// Two sessions: default has w1 (a split tab t1 with A | B, a tab t3 with C) and w3 (empty); s2 has w2 with D.
const opsLayout = `{"version":1,"active":"w1","spaces":[{"id":"default","name":"default"},{"id":"s2","name":"other"}],"workspaces":[
 {"id":"w1","name":"a","cwd":"/r","activeTab":"t1","tabs":[
   {"id":"t1","focus":"p1","root":{"type":"split","id":"x","dir":"row","ratio":0.5,
     "a":{"type":"pane","id":"p1","sessionId":"A"},"b":{"type":"pane","id":"p2","sessionId":"B"}}},
   {"id":"t3","name":"tests","focus":"p4","root":{"type":"pane","id":"p4","sessionId":"C"}}]},
 {"id":"w2","name":"b","space":"s2","activeTab":"t2","tabs":[{"id":"t2","focus":"p3","root":{"type":"pane","id":"p3","sessionId":"D"}}]},
 {"id":"w3","name":"empty","activeTab":"t9","tabs":[{"id":"t9","focus":"p9","root":{"type":"pane","id":"p9"}}]}
]}`

var opsSessions = []LayoutSession{
	{ID: "A", Title: "me", Cwd: "/r/gerdr"}, {ID: "B", Title: "reviewer", Cwd: "/r/gerdr"}, {ID: "C", Title: "runner", Cwd: "/r/x"}, {ID: "D", Title: "other"},
}

// apply runs a chain of calls as agent A, feeding each result layout into the next.
func apply(t *testing.T, raw string, calls ...func() (string, LayoutParams)) (string, any) {
	t.Helper()
	var res any
	for _, f := range calls {
		m, p := f()
		out, r, err := ApplyLayout(json.RawMessage(raw), opsSessions, "A", m, p)
		if err != nil {
			t.Fatalf("%s %+v: %v", m, p, err)
		}
		if out != nil {
			raw = string(out)
		}
		res = r
	}
	return raw, res
}

func call(m string, p LayoutParams) func() (string, LayoutParams) {
	return func() (string, LayoutParams) { return m, p }
}

// shape: workspace name → tab split expressions, for comparing layouts.
func shape(t *testing.T, raw string) map[string][]string {
	t.Helper()
	var l node
	_ = json.Unmarshal([]byte(raw), &l)
	out := map[string][]string{}
	for _, w := range nodes(l["workspaces"]) {
		var tabs []string
		for _, tb := range tabsOf(w) {
			tabs = append(tabs, splitExpr(asNode(tb["root"])))
		}
		out[str(w, "name")] = tabs
	}
	return out
}

func eq(t *testing.T, got map[string][]string, ws string, want ...string) {
	t.Helper()
	if strings.Join(got[ws], " ; ") != strings.Join(want, " ; ") {
		t.Errorf("%s: got %v, want %v", ws, got[ws], want)
	}
}

func TestLayoutListScopedToCallersSession(t *testing.T) {
	_, res := apply(t, opsLayout, call("workspace.list", LayoutParams{}))
	b, _ := json.Marshal(res)
	if strings.Contains(string(b), `"w2"`) || !strings.Contains(string(b), `"w3"`) {
		t.Errorf("list should hold w1, w3 but not w2: %s", b)
	}
	if _, _, err := ApplyLayout(json.RawMessage(opsLayout), opsSessions, "A", "pane.move", LayoutParams{ID: "D", Workspace: "a"}); err == nil {
		t.Error("an agent reached a pane in another session")
	}
	if _, _, err := ApplyLayout(json.RawMessage(opsLayout), opsSessions, "", "pane.move", LayoutParams{ID: "p3", Workspace: "a"}); err == nil {
		t.Error("an unknown caller moved a pane across sessions")
	}
}

func TestPaneMoveIntoWorkspaceDropsPlaceholder(t *testing.T) {
	// By title, into the empty workspace by label: its placeholder tab goes away; A takes all of t1.
	raw, _ := apply(t, opsLayout, call("pane.move", LayoutParams{ID: "reviewer", Workspace: "empty", TabLabel: "review"}))
	s := shape(t, raw)
	eq(t, s, "a", "p1", "p4")
	eq(t, s, "empty", "p2")
	if !strings.Contains(raw, `"name":"review"`) {
		t.Error("tab label not set")
	}
}

func TestPaneMoveSplitsAndTabsTravel(t *testing.T) {
	// C (alone in t3) split below A: t3 disappears, no empty tab left behind.
	raw, _ := apply(t, opsLayout, call("pane.move", LayoutParams{ID: "C", TargetPane: "p1", Split: "down"}))
	s := shape(t, raw)
	eq(t, s, "a", "row(col(p1, p4), p2)")

	// Moving a lone pane to a new workspace takes its tab (and its label) along.
	raw, _ = apply(t, opsLayout, call("pane.move", LayoutParams{ID: "p4", NewWorkspace: true}))
	s = shape(t, raw)
	eq(t, s, "a", "row(p1, p2)")
	eq(t, s, "tests", "p4")
}

func TestTabMoveByNumberCountsOwnSessionOnly(t *testing.T) {
	// The caller sees w1 = 1 and w3 = 2 (w2 is another session's), so "2" is w3 and "3" is nothing.
	raw, _ := apply(t, opsLayout, call("tab.move", LayoutParams{ID: "t1", Workspace: "2"}))
	s := shape(t, raw)
	eq(t, s, "empty", "row(p1, p2)")
	eq(t, s, "a", "p4")
	if _, _, err := ApplyLayout(json.RawMessage(opsLayout), opsSessions, "A", "tab.move", LayoutParams{ID: "t1", Workspace: "3"}); err == nil {
		t.Error("workspace 3 shouldn't resolve")
	}
}

func TestTabMergeAndWorkspaceClose(t *testing.T) {
	raw, _ := apply(t, opsLayout,
		call("tab.move", LayoutParams{ID: "tests", Tab: "t1", Split: "left", Ratio: 0.3}),
		call("workspace.close", LayoutParams{ID: "empty"}),
	)
	s := shape(t, raw)
	eq(t, s, "a", "row(p4, row(p1, p2))")
	if _, ok := s["empty"]; ok {
		t.Error("empty workspace not closed")
	}
	if !strings.Contains(raw, `"ratio":0.3`) {
		t.Errorf("merged tab should take 0.3: %s", raw)
	}
	if _, _, err := ApplyLayout(json.RawMessage(opsLayout), opsSessions, "A", "workspace.close", LayoutParams{ID: "a"}); err == nil {
		t.Error("closed a workspace that still shows conversations")
	}
}

func TestWorkspaceCreateMoveAndSwap(t *testing.T) {
	raw, res := apply(t, opsLayout, call("workspace.create", LayoutParams{Label: "lb", Position: 1}))
	id := res.(node)["workspace"].(node)["workspace_id"].(string)
	raw, _ = apply(t, raw,
		call("tab.move", LayoutParams{ID: "t3", Workspace: id}),
		call("pane.swap", LayoutParams{ID: "p1", TargetPane: "p4"}),
	)
	var l node
	_ = json.Unmarshal([]byte(raw), &l)
	ws := nodes(l["workspaces"])
	if str(ws[0], "name") != "lb" {
		t.Errorf("new workspace should be first: %v", str(ws[0], "name"))
	}
	s := shape(t, raw)
	eq(t, s, "lb", "p1")
	eq(t, s, "a", "row(p4, p2)")
	// The swapped leaves carry their ids: p1 still shows A.
	c := layoutCall{l: l}
	_, _, p, err := c.pane("p1")
	if err != nil || str(p, "sessionId") != "A" {
		t.Errorf("p1 should still show A: %v %v", p, err)
	}
}
