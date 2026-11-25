package server

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gerdr-dev/gerdr/internal/artifacts"
	"github.com/gerdr-dev/gerdr/internal/config"
	"github.com/gerdr-dev/gerdr/internal/hub"
	"github.com/gerdr-dev/gerdr/internal/work"
)

// Renaming a panel's directory moves what Gerdr keeps under its key or path outside the Runner: sessions, inbox,
// badges, config.json workSessions, the layout and artifacts owners.
func TestRenamePanelDir(t *testing.T) {
	root := t.TempDir()
	wr := work.New(root, nil)
	key, err := wr.NewPanel(work.DefaultProject, "fusion")
	if err != nil {
		t.Fatal(err)
	}
	dir, _ := wr.Dir(key)

	hdir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(hdir, "sessions"), 0o700)
	recs := []hub.Record{
		{ID: "w1", Agent: "claude", Tags: map[string]string{"panel": key, "task": "t1"}, Cwd: "/repo", SystemFile: filepath.Join(dir, "user", "data", "workers", "t1.md"), SkillDirs: []string{filepath.Join(dir, "skills"), "/other"}},
		{ID: "own", Agent: "claude", Cwd: dir},
		{ID: "x", Agent: "claude", Cwd: "/elsewhere", Tags: map[string]string{"panel": "default/fusion2"}},
	}
	b, _ := json.Marshal(recs)
	_ = os.WriteFile(filepath.Join(hdir, "sessions", "index.json"), b, 0o600)
	h, err := hub.New(hub.Options{Dir: hdir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)

	arts := t.TempDir()
	s := &Server{
		Dir: t.TempDir(), Hub: h, Work: wr, Log: slog.Default(),
		Config:    config.NewStore(filepath.Join(t.TempDir(), "config.json"), `{"workSessions":{"default/fusion":"own","lb":"z"}}`),
		Layout:    config.NewStore(filepath.Join(t.TempDir(), "layout.json"), `{"workspaces":[{"id":"w","cwd":"`+dir+`"},{"id":"v","cwd":"`+dir+`x"}]}`),
		Artifacts: artifacts.New(func() string { return arts }),
	}
	if err := s.SetInbox(key, []work.InboxItem{{ID: "a", Title: "waiting"}}); err != nil {
		t.Fatal(err)
	}
	p := s.Artifacts.ForOwner("default", "default", artifacts.Owner{Panel: key, Task: "t1"}, "", "t1")
	if _, err := s.Artifacts.Ensure(p, artifacts.Owner{Panel: key, Task: "t1"}, "default"); err != nil {
		t.Fatal(err)
	}

	out, err := s.renamePanelDir("fusion", "superagent")
	if err == nil {
		t.Fatal("a bare name isn't a key; the CLI makes it one")
	}
	out, err = s.renamePanelDir(key, "superagent")
	if err != nil || out["key"] != "default/superagent" || out["sessions"] != 2 {
		t.Fatalf("rename: %v %v", out, err)
	}
	ndir, _ := wr.Dir("default/superagent")

	w1, _ := h.Session("w1")
	sum := w1.Summary()
	if sum.Tags["panel"] != "default/superagent" || sum.Tags["task"] != "t1" {
		t.Errorf("tags = %v", sum.Tags)
	}
	if own, _ := h.Session("own"); own.Summary().Cwd != ndir {
		t.Errorf("own cwd = %s", own.Summary().Cwd)
	}
	if x, _ := h.Session("x"); x.Summary().Tags["panel"] != "default/fusion2" {
		t.Error("another panel's session changed")
	}
	idx, _ := os.ReadFile(filepath.Join(hdir, "sessions", "index.json"))
	if strings.Contains(string(idx), dir+"/") || !strings.Contains(string(idx), filepath.Join(ndir, "skills")) {
		t.Errorf("index not moved: %s", idx)
	}
	if its := s.InboxItems(); len(its) != 1 || its[0].Panel != "default/superagent" {
		t.Errorf("inbox = %+v", its)
	}
	if c := compact(s.Config.Get()); !strings.Contains(c, `"default/superagent":"own"`) || strings.Contains(c, `default/fusion"`) {
		t.Errorf("config = %s", c)
	}
	if l := compact(s.Layout.Get()); !strings.Contains(l, `"cwd":"`+ndir+`"`) || !strings.Contains(l, `"cwd":"`+dir+`x"`) {
		t.Errorf("layout = %s", l)
	}
	if got := s.Artifacts.ForOwner("default", "default", artifacts.Owner{Panel: "default/superagent", Task: "t1"}, "", "t1"); got != p {
		t.Errorf("artifacts folder = %s, want the task's own %s", got, p)
	}
}

func compact(b []byte) string {
	var v any
	_ = json.Unmarshal(b, &v)
	out, _ := json.Marshal(v)
	return string(out)
}
