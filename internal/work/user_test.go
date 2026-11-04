package work

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, dir, rel, text string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A person's triggers / schedules sit in user/ beside the panel's own: the same id replaces the shared one.
func TestUserOverlayDefinitions(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "triggers/mr.json", `{"on":"gitlab/*","fn":"inbox.onEvent"}`)
	write(t, dir, "triggers/alert.json", `{"on":"grafana/*","prompt":"shared"}`)
	write(t, dir, "user/triggers/alert.json", `{"on":"grafana/*","prompt":"mine"}`)
	write(t, dir, "user/triggers/inbox-slack-dm.json", `{"on":"slack/dm","fn":"inbox.onEvent"}`)
	ts, err := ParseTriggers(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, x := range ts {
		got[x.ID] = x.On + x.Prompt
	}
	if len(ts) != 3 || got["alert"] != "grafana/*mine" || got["inbox-slack-dm"] != "slack/dm" || got["mr"] != "gitlab/*" {
		t.Fatalf("triggers: %v", got)
	}
	write(t, dir, "user/schedules/daily.json", `{"fn":"x.y","every":"1h"}`)
	ss, err := ParseSchedules(dir)
	if err != nil || len(ss) != 1 || ss[0].ID != "daily" {
		t.Fatalf("schedules: %v %v", ss, err)
	}
	// A broken personal file names itself
	write(t, dir, "user/triggers/bad.json", `{`)
	if _, err := ParseTriggers(dir); err == nil || !strings.Contains(err.Error(), "user/triggers/bad.json") {
		t.Fatalf("broken: %v", err)
	}
	if overlaySig(dir, TriggersDir) == "" {
		t.Fatal("sig")
	}
}

// Sessions get skills/ as is until the person edits one; then a tree of links with their files over the shared ones.
func TestSkillsRoot(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "skills/flow/SKILL.md", "shared skill")
	write(t, dir, "skills/flow/references/repos.md", "shared repos")
	write(t, dir, "skills/flow/references/mr.md", "shared mr")
	if got := SkillsRoot(dir); got != filepath.Join(dir, "skills") {
		t.Fatalf("no edits: %s", got)
	}
	write(t, dir, "user/skills/flow/references/repos.md", "my repos")
	root := SkillsRoot(dir)
	read := func(rel string) string {
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if root != filepath.Join(dir, "user", ".skills") || read("flow/SKILL.md") != "shared skill" || read("flow/references/repos.md") != "my repos" || read("flow/references/mr.md") != "shared mr" {
		t.Fatalf("overlay at %s", root)
	}
	// Live: an edit shows through the link; a removed shared file goes on the next call
	write(t, dir, "user/skills/flow/references/repos.md", "my repos v2")
	os.Remove(filepath.Join(dir, "skills/flow/references/mr.md"))
	SkillsRoot(dir)
	if read("flow/references/repos.md") != "my repos v2" {
		t.Fatal("not live")
	}
	if _, err := os.Lstat(filepath.Join(root, "flow/references/mr.md")); err == nil {
		t.Fatal("stale link kept")
	}
}

func TestUserDirIgnoresItself(t *testing.T) {
	r, dir := newProject(t, nil)
	_ = r
	b, err := os.ReadFile(filepath.Join(dir, "user", ".gitignore"))
	if err != nil || string(b) != userIgnore {
		t.Fatalf("%q %v", b, err)
	}
}
