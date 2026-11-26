package server

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/gerdr-dev/gerdr/internal/config"
	"github.com/gerdr-dev/gerdr/internal/hub"
	"github.com/gerdr-dev/gerdr/internal/work"
)

// A panel's own panes live in its directory, apart from Code's layout.json: the workspaces an earlier version kept in
// layout.json (marked "host") move there once, the server reads both as one, and a change made on that one (`gerdr
// pane …`) goes back to the file each workspace came from. A conversation the panel starts goes into its own layout.
func TestPanelLayout(t *testing.T) {
	root := t.TempDir()
	wr := work.New(root, nil)
	key, err := wr.NewPanel(work.DefaultProject, "sa")
	if err != nil {
		t.Fatal(err)
	}
	hdir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(hdir, "sessions"), 0o700)
	b, _ := json.Marshal([]hub.Record{
		{ID: "task", Agent: "claude", Cwd: "/repo", Tags: map[string]string{"panel": key}},
		{ID: "own", Agent: "claude", Cwd: "/repo"},
		{ID: "w2", Agent: "claude", Cwd: "/repo", Tags: map[string]string{"panel": key}},
	})
	_ = os.WriteFile(filepath.Join(hdir, "sessions", "index.json"), b, 0o600)
	h, err := hub.New(hub.Options{Dir: hdir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	code := `{"version":1,"active":"wp","workspaces":[
		{"id":"wc","name":"repo","cwd":"/repo","tabs":[{"id":"t1","root":{"type":"pane","id":"p1","sessionId":"own"},"focus":"p1"}],"activeTab":"t1"},
		{"id":"wp","name":"repo","cwd":"/repo","host":"` + key + `","tabs":[{"id":"t2","root":{"type":"pane","id":"p2","sessionId":"x"},"focus":"p2"}],"activeTab":"t2"},
		{"id":"wg","name":"gone","host":"default/nope","tabs":[]},
		{"id":"wt","name":"repo","cwd":"/repo","tabs":[{"id":"t3","root":{"type":"pane","id":"p3","sessionId":"w2"},"focus":"p3"}],"activeTab":"t3"}]}`
	s := &Server{Hub: h, Work: wr, Log: slog.Default(), Layout: config.NewStore(filepath.Join(t.TempDir(), "layout.json"), code)}

	wsIDs := func(raw json.RawMessage) map[string]string {
		var l map[string]any
		_ = json.Unmarshal(raw, &l)
		out := map[string]string{}
		for _, w := range nodes(l["workspaces"]) {
			h, _ := w["host"].(string)
			out[str(w, "id")] = h
		}
		return out
	}

	s.MigrateHostWorkspaces()
	if got := wsIDs(s.Layout.Get()); len(got) != 1 || got["wc"] != "" {
		t.Fatalf("layout.json after the move: %v, want only Code's wc", got)
	}
	var cl map[string]any
	_ = json.Unmarshal(s.Layout.Get(), &cl)
	if cl["active"] != "wc" {
		t.Errorf("Code's active: %v, want wc (wp left)", cl["active"])
	}
	pls := s.PanelLayouts()
	if got := wsIDs(pls[key]); len(got) != 1 || got["wp"] != "" {
		t.Fatalf("the panel's own layout: %v, want wp, unmarked", got)
	}
	// The panel's worker that sat in Code's workspace wt (the oldest way): out of Code's, into the panel's own
	if !panelShows(pls[key], "w2") {
		t.Errorf("the panel's worker isn't in its own layout: %s", pls[key])
	}
	s.MigrateHostWorkspaces() // once: nothing left to move
	if got := wsIDs(s.PanelLayouts()[key]); len(got) != 1 {
		t.Fatalf("moved twice: %v", got)
	}

	all := s.LayoutAll()
	if got := wsIDs(all); got["wc"] != "" || got["wp"] != key {
		t.Fatalf("all panes: %v, want wc Code's and wp the panel's", got)
	}
	if sp := SessionSpaces(all); sp["x"] != work.DefaultProject && sp["x"] != "" {
		t.Errorf("a panel's pane has no project: %v", sp)
	}

	// A change on the merged layout goes back where each workspace came from
	var l map[string]any
	_ = json.Unmarshal(all, &l)
	for _, w := range nodes(l["workspaces"]) {
		w["name"] = "renamed"
	}
	out, _ := json.Marshal(l)
	if err := s.setLayoutAll(out); err != nil {
		t.Fatal(err)
	}
	if got := wsIDs(s.Layout.Get()); len(got) != 1 || got["wc"] != "" {
		t.Errorf("layout.json after a merged change: %v", got)
	}
	var pl map[string]any
	_ = json.Unmarshal(s.PanelLayouts()[key], &pl)
	if ws := nodes(pl["workspaces"]); len(ws) != 1 || ws[0]["name"] != "renamed" || ws[0]["host"] != nil {
		t.Errorf("the panel's after a merged change: %v", pl["workspaces"])
	}

	// Started by the panel: into its own layout, next to its other panes; Code's untouched
	if err := s.placeSession("", "task", "/repo", "/repo", ""); err != nil {
		t.Fatal(err)
	}
	if got := wsIDs(s.Layout.Get()); len(got) != 1 {
		t.Errorf("Code's layout got the panel's conversation: %v", got)
	}
	found := false
	_ = json.Unmarshal(s.PanelLayouts()[key], &pl)
	for _, w := range nodes(pl["workspaces"]) {
		for _, tb := range tabsOf(w) {
			for _, p := range leaves(asNode(tb["root"])) {
				found = found || str(p, "sessionId") == "task"
			}
		}
	}
	if !found {
		t.Errorf("the panel's conversation isn't in its layout: %v", pl["workspaces"])
	}
	// One of Code's: Code's layout
	if err := s.placeSession("", "own", "/repo", "/repo", ""); err != nil {
		t.Fatal(err)
	}
}

func panelShows(raw json.RawMessage, id string) bool {
	var pl map[string]any
	_ = json.Unmarshal(raw, &pl)
	for _, w := range nodes(pl["workspaces"]) {
		for _, tb := range tabsOf(w) {
			for _, p := range leaves(asNode(tb["root"])) {
				if str(p, "sessionId") == id {
					return true
				}
			}
		}
	}
	return false
}
