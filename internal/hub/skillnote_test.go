package hub

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A long-lived session is told which of its skill files changed since the last message, once, and only when on.
// Files reached through links (a panel's user/.skills tree) count by their target's time.
func TestSkillsChanged(t *testing.T) {
	h, err := New(Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	src := t.TempDir()
	root := t.TempDir()
	write := func(p string) {
		os.MkdirAll(filepath.Dir(p), 0o700)
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(root, "my-workflow", "SKILL.md"))
	write(filepath.Join(root, "my-workflow", "references", "task.md"))
	write(filepath.Join(root, ".hidden", "x.md"))
	write(filepath.Join(src, "linked.md"))
	os.MkdirAll(filepath.Join(root, "other"), 0o700)
	if err := os.Symlink(filepath.Join(src, "linked.md"), filepath.Join(root, "other", "linked.md")); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Hour)
	filepath.Walk(root, func(p string, _ os.FileInfo, _ error) error { os.Chtimes(p, past, past); return nil })
	os.Chtimes(filepath.Join(src, "linked.md"), past, past)

	s := newSession(h, Record{ID: "s1", SkillDirs: []string{root}})
	if got := s.skillsChanged(); got != nil {
		t.Fatalf("off: %v", got)
	}
	s.SetSkillUpdates(true)
	if got := s.skillsChanged(); got != nil {
		t.Fatalf("nothing changed since it was turned on: %v", got[0].Text.Text)
	}

	time.Sleep(5 * time.Millisecond)
	write(filepath.Join(root, "my-workflow", "references", "task.md"))
	write(filepath.Join(src, "linked.md"))
	write(filepath.Join(root, ".hidden", "x.md"))
	got := s.skillsChanged()
	if len(got) != 1 {
		t.Fatalf("want one block, got %d", len(got))
	}
	txt := got[0].Text.Text
	for _, want := range []string{skillsPrefix, "- my-workflow/references/task.md: " + filepath.Join(root, "my-workflow", "references", "task.md"), "- other/linked.md: ", "read each again"} {
		if !strings.Contains(txt, want) {
			t.Errorf("missing %q in:\n%s", want, txt)
		}
	}
	for _, not := range []string{"SKILL.md", ".hidden"} {
		if strings.Contains(txt, not) {
			t.Errorf("%q wasn't changed / is hidden:\n%s", not, txt)
		}
	}
	raw, _ := json.Marshal(got[0])
	if !isSkillsBlock(raw) || !injectedText(txt) {
		t.Error("the block isn't recognised")
	}
	if got := s.skillsChanged(); got != nil {
		t.Fatalf("told twice: %v", got[0].Text.Text)
	}
	if !s.rec.SkillUpdates || s.rec.SkillsSeen == 0 {
		t.Fatal("record not kept")
	}
}

func TestSkillsTextCapped(t *testing.T) {
	var fs []skillFile
	for i := 0; i < skillListMax+5; i++ {
		fs = append(fs, skillFile{rel: "a.md", path: "/x/a.md"})
	}
	txt := skillsText(fs)
	if strings.Count(txt, "\n- a.md") != skillListMax || !strings.Contains(txt, "- … and 5 more") {
		t.Fatal(txt)
	}
}
