package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/gerdr-dev/gerdr/internal/config"
	"github.com/gerdr-dev/gerdr/internal/hub"
	"github.com/gerdr-dev/gerdr/internal/work"
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
	// By label or number too (`gerdr session new --workspace`), counted within the project like `gerdr workspace list`
	l, _ = place("", "N10", "/r", "empty")
	if ws, _, _ := where(l, "N10"); ws != "w3" {
		t.Errorf("by label: in %q, want w3", ws)
	}
	l, _ = place("", "N11", "/elsewhere", "1")
	if ws, _, _ := where(l, "N11"); ws != "w1" {
		t.Errorf("by number: in %q, want w1", ws)
	}
	if _, err := place("", "N12", "/r", "b"); err == nil {
		t.Error("the label of a workspace in another project: want an error")
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
	if got := lastStop(evs); got != "max_tokens" {
		t.Errorf("lastStop = %q, want max_tokens", got)
	}
	// A message sent since the last turn ended: that ending no longer says how things stand
	if got := lastStop(append(evs, &hub.Event{Kind: hub.EvUser})); got != "" {
		t.Errorf("lastStop after a new message = %q, want none", got)
	}
}

func quote(s string) string { b, _ := json.Marshal(s); return string(b) }

// "work" in send / reportTo is the project's Work agent conversation: a local function has no id for it.
func TestWorkTarget(t *testing.T) {
	h := workHost{&Server{Config: config.NewStore(filepath.Join(t.TempDir(), "config.json"), `{"workSessions":{"p1":"w1"}}`)}}
	if id, err := h.target("p1", "work"); err != nil || id != "w1" {
		t.Fatalf("work in p1 = %q, %v", id, err)
	}
	if id, _ := h.target("p1", "abc"); id != "abc" {
		t.Fatalf("a session id must pass through, got %q", id)
	}
	if _, err := h.target("p2", "work"); err == nil {
		t.Fatal("a project without a Work conversation must be an error")
	}
}

// Attachments go as the composer sends them: an image inline, anything else a file link, the text last.
func TestWithAttachments(t *testing.T) {
	dir := t.TempDir()
	png := filepath.Join(dir, "shot.png")
	log := filepath.Join(dir, "build.log")
	_ = os.WriteFile(png, []byte("\x89PNG fake"), 0o600)
	_ = os.WriteFile(log, []byte("error"), 0o600)
	b, err := withAttachments("look", []string{png, log})
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != 3 || b[0].Image == nil || b[0].Image.MimeType != "image/png" || b[1].ResourceLink == nil || b[1].ResourceLink.Uri != "file://"+log || b[2].Text == nil {
		t.Fatalf("blocks %+v", b)
	}
	if _, err := withAttachments("x", []string{"relative.png"}); err == nil {
		t.Error("a relative path must be refused")
	}
	if _, err := withAttachments("x", []string{filepath.Join(dir, "gone.png")}); err == nil {
		t.Error("a missing file must be refused")
	}
}

// A session started in the Work directory itself is a Work chat: not placed in Code (StartSession).
func TestInWorkDir(t *testing.T) {
	root := t.TempDir()
	s := &Server{Work: work.New(root, nil)}
	d, _ := s.Work.Dir("lb")
	_ = os.MkdirAll(d, 0o755)
	if !s.inWorkDir("lb", d) || !s.inWorkDir("lb", d+"/") {
		t.Error("the Work dir itself must count")
	}
	if s.inWorkDir("lb", filepath.Join(d, "local")) || s.inWorkDir("lb", root) || s.inWorkDir("other", d) {
		t.Error("only that project's Work dir itself counts")
	}
}

// `gerdr panel run` from a board's worker (in its own repository) runs on that board, not the project's own Work.
func TestCallerPanel(t *testing.T) {
	for _, c := range []struct{ panel, project, want string }{
		{"", "default", "default"},
		{"", "lb", "lb"},
		{"default/lb看板", "default", "default/lb看板"},
		{"default/lb看板", "", "default/lb看板"},
		{"lb/board", "lb", "lb/board"},
		{"lb/board", "default", "lb/board"},
		{"lb/board", "other", "other"},
	} {
		if got := callerPanel(c.panel, c.project); got != c.want {
			t.Errorf("callerPanel(%q, %q) = %q, want %q", c.panel, c.project, got, c.want)
		}
	}
}
