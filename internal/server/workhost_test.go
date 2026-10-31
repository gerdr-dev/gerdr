package server

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/gerdr-dev/gerdr/internal/config"
	"github.com/gerdr-dev/gerdr/internal/hub"
)

// A conversation a Work function starts goes into its project's layout: the workspace for its directory (or the
// closest one above it), else a new one; the tab the user is on stays active.
func TestPlaceSession(t *testing.T) {
	place := func(project, id, cwd, ws string) (node, error) {
		st := config.NewStore(filepath.Join(t.TempDir(), "layout.json"), `null`)
		if err := st.Set(json.RawMessage(opsLayout)); err != nil {
			t.Fatal(err)
		}
		s := &Server{Layout: st}
		err := s.placeSession(project, id, cwd, cwd, ws)
		var l node
		_ = json.Unmarshal(st.Get(), &l)
		return l, err
	}
	// where is session id: workspace id, and whether its tab is that workspace's active one
	where := func(l node, id string) (string, bool, string) {
		c := &layoutCall{l: l}
		for _, w := range c.wss() {
			for _, tb := range tabsOf(w) {
				for _, p := range leaves(asNode(tb["root"])) {
					if str(p, "sessionId") == id {
						return str(w, "id"), w["activeTab"] == tb["id"], str(w, "cwd")
					}
				}
			}
		}
		return "", false, ""
	}

	l, err := place("", "N1", "/r", "")
	if ws, active, _ := where(l, "N1"); err != nil || ws != "w1" || active {
		t.Errorf("exact cwd: in %q (active %v), err %v; want w1, not active", ws, active, err)
	}
	l, _ = place("", "N2", "/r/sub/deep", "")
	if ws, _, _ := where(l, "N2"); ws != "w1" {
		t.Errorf("under a workspace's dir: in %q, want w1", ws)
	}
	l, _ = place("", "N3", "/elsewhere/proj", "")
	if ws, _, cwd := where(l, "N3"); ws == "" || ws == "w1" || ws == "w3" || cwd != "/elsewhere/proj" {
		t.Errorf("no workspace for it: in %q (cwd %q), want a new one for its dir", ws, cwd)
	}
	l, _ = place("", "N4", "/r", "w3")
	if ws, _, _ := where(l, "N4"); ws != "w3" {
		t.Errorf("explicit workspace: in %q, want w3", ws)
	}
	// w3's only tab was empty: it's replaced, not kept beside
	for _, w := range (&layoutCall{l: l}).wss() {
		if str(w, "id") == "w3" && len(tabsOf(w)) != 1 {
			t.Errorf("w3 has %d tabs, want the empty one replaced", len(tabsOf(w)))
		}
	}
	l, _ = place("", "N8", "/r", "new")
	if ws, _, cwd := where(l, "N8"); ws == "w1" || ws == "" || cwd != "/r" {
		t.Errorf(`"new" though w1 matches: in %q (cwd %q), want a new workspace`, ws, cwd)
	}
	if _, err := place("", "N5", "/r", "w2"); err == nil {
		t.Error("a workspace in another project: want an error")
	}
	// The hub keeps the resolved path (/private/tmp/…); the workspace has it as typed
	{
		st := config.NewStore(filepath.Join(t.TempDir(), "layout.json"), `null`)
		_ = st.Set(json.RawMessage(opsLayout))
		s := &Server{Layout: st}
		if err := s.placeSession("", "N7", "/elsewhere/link", "/r/real", ""); err != nil {
			t.Fatal(err)
		}
		var l node
		_ = json.Unmarshal(st.Get(), &l)
		if ws, _, _ := where(l, "N7"); ws != "w1" {
			t.Errorf("resolved path under a workspace: in %q, want w1", ws)
		}
	}
	l, _ = place("s2", "N6", "/r", "")
	if ws, _, _ := where(l, "N6"); ws == "w1" || ws == "" {
		t.Errorf("project s2: in %q, want a workspace of s2 (w1 is default's)", ws)
	}
}

// The windows take the new layout from the broadcast: it must go out as the layout object (a []byte would be
// marshalled as a base64 string, which they drop, and then write their stale copy back over it).
func TestPlaceSessionBroadcast(t *testing.T) {
	h, err := hub.New(hub.Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	st := config.NewStore(filepath.Join(t.TempDir(), "layout.json"), `null`)
	_ = st.Set(json.RawMessage(opsLayout))
	s := &Server{Layout: st, Hub: h}
	sub := h.Subscribe()
	defer h.Unsubscribe(sub)
	if err := s.placeSession("", "N1", "/new", "/new", ""); err != nil {
		t.Fatal(err)
	}
	m := <-sub.C()
	b, _ := json.Marshal(m)
	var got struct {
		T    string `json:"t"`
		Data struct {
			Data struct {
				Workspaces []map[string]any `json:"workspaces"`
			} `json:"data"`
		} `json:"data"`
	}
	if err := json.Unmarshal(b, &got); err != nil || got.T != "layout" || len(got.Data.Data.Workspaces) != 4 {
		t.Errorf("broadcast %s: %v", b[:min(len(b), 200)], err)
	}
}

func TestLastReply(t *testing.T) {
	up := func(kind, text string, meta string) *hub.Event {
		u := `{"sessionUpdate":"` + kind + `","content":{"type":"text","text":` + quote(text) + `}` + meta + `}`
		return &hub.Event{Kind: hub.EvUpdate, Update: json.RawMessage(u)}
	}
	evs := []*hub.Event{
		{Kind: hub.EvUser},
		up("agent_message_chunk", "old answer", ""),
		{Kind: hub.EvTurnEnd, StopReason: "end_turn"},
		{Kind: hub.EvUser},
		up("agent_message_chunk", "Looking", ""),
		up("agent_message_chunk", " at it.", ""),
		up("tool_call", "", ""),
		up("agent_message_chunk", "from a subagent", `,"_meta":{"gerdr":{"subagent":{}}}`),
		up("agent_message_chunk", "Done.", ""),
		{Kind: hub.EvTurnEnd, StopReason: "max_tokens"},
	}
	want := "Looking at it.\n\nDone.\n\n这一轮结束：max_tokens"
	if got := lastReply(evs); got != want {
		t.Errorf("lastReply = %q, want %q", got, want)
	}
}

func quote(s string) string { b, _ := json.Marshal(s); return string(b) }
