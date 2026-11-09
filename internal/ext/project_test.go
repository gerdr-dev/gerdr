package ext

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProjectSkills(t *testing.T) {
	m := New(t.TempDir())
	if err := m.WriteProjectSkill("lb", "deploy", "---\nname: deploy\ndescription: how lb deploys\n---\nbody"); err != nil {
		t.Fatal(err)
	}
	got := m.ProjectSkills("lb")
	if len(got) != 1 || got[0].Name != "deploy" || got[0].Description != "how lb deploys" || got[0].Origin != OriginProject {
		t.Fatalf("ProjectSkills = %+v", got)
	}
	if len(m.ProjectSkills("default")) != 0 {
		t.Fatal("another project sees lb's skill")
	}
	if c, err := m.ReadProjectSkill("lb", "deploy"); err != nil || c == "" {
		t.Fatalf("read: %q %v", c, err)
	}
	for _, p := range []string{"", "..", "a/b", "x.y"} {
		if err := m.WriteProjectSkill(p, "s", "x"); err == nil {
			t.Errorf("project %q accepted", p)
		}
	}
	if err := m.WriteProjectSkill("lb", "../evil", "x"); err == nil {
		t.Error("bad skill name accepted")
	}
	if err := m.DeleteProjectSkill("lb", "deploy"); err != nil || len(m.ProjectSkills("lb")) != 0 {
		t.Fatalf("delete: %v", err)
	}
}

// A session's own skill folder wins over a same-named skill in gerdr's pool, and a skill coming or going changes the
// fingerprint (so a running session picks it up), an edit doesn't.
func TestProjectSkillWinsAndFingerprint(t *testing.T) {
	dir := t.TempDir()
	m := New(dir)
	if err := m.WriteSkill("deploy", "---\nname: deploy\n---\nglobal"); err != nil {
		t.Fatal(err)
	}
	if err := m.Save(Settings{SkillSource: SourceBoth, MCPSource: SourceAgent}); err != nil {
		t.Fatal(err)
	}
	root, _ := m.ProjectSkillsDir("lb")
	fp0 := RootsFingerprint([]string{root})
	if err := m.WriteProjectSkill("lb", "deploy", "---\nname: deploy\n---\nproject"); err != nil {
		t.Fatal(err)
	}
	fp1 := RootsFingerprint([]string{root})
	if fp0 == fp1 {
		t.Error("adding a skill didn't change the fingerprint")
	}
	_ = m.WriteProjectSkill("lb", "deploy", "---\nname: deploy\n---\nedited")
	if RootsFingerprint([]string{root}) != fp1 {
		t.Error("editing a skill changed the fingerprint")
	}
	shell, err := m.ClaudeAddDir("s1", true, root)
	if err != nil {
		t.Fatal(err)
	}
	link, err := os.Readlink(filepath.Join(shell, ".claude", "skills", "deploy"))
	if err != nil {
		t.Fatal(err)
	}
	if link != filepath.Join(root, "deploy") {
		t.Errorf("deploy links to %s, want the project's", link)
	}
}
