package work

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// A renamed panel goes by its new key everywhere, and its old key and path still lead to it: agents that were running
// for it hold both (rename.go).
func TestRenamePanel(t *testing.T) {
	root := t.TempDir()
	r := New(root, nil)
	key, err := r.NewPanel("lb", "board")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.NewPanel("lb", "notes"); err != nil {
		t.Fatal(err)
	}
	if err := r.MovePanel(key, 0); err != nil {
		t.Fatal(err)
	}
	oldDir, _ := r.Dir(key)
	logs := r.logDir(key)
	if err := os.MkdirAll(logs, 0o755); err != nil {
		t.Fatal(err)
	}
	state := r.stateFile(key)
	_ = os.MkdirAll(filepath.Dir(state), 0o755)
	if err := os.WriteFile(state, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}

	nkey, err := r.RenamePanel("lb/board", "tasks")
	if err != nil || nkey != "lb/tasks" {
		t.Fatalf("rename: %q %v", nkey, err)
	}
	newDir, _ := r.Dir(nkey)
	if fi, err := os.Lstat(newDir); err != nil || !fi.IsDir() {
		t.Fatalf("new dir: %v", err)
	}
	if got := r.Panels("lb"); !slices.Equal(got, []string{"notes", "tasks"}) {
		t.Errorf("panels = %v: the old name's symlink must not be a panel", got)
	}
	if got := r.RailOrder("lb"); got[0] != "tasks" {
		t.Errorf("rail order = %v: the panel keeps its place", got)
	}
	if r.Canon("lb/board") != "lb/tasks" || r.Canon("lb/notes") != "lb/notes" || r.Canon("lb") != "lb" {
		t.Error("Canon")
	}
	if d, _ := r.Dir("lb/board"); d != newDir {
		t.Errorf("Dir(old) = %s", d)
	}
	if k, _ := r.WorkKey(filepath.Join(oldDir, "skills", "x.md")); k != "lb/tasks" {
		t.Errorf("WorkKey(old path) = %s", k)
	}
	if b, err := os.ReadFile(filepath.Join(oldDir, "ui", "ui.json")); err != nil || len(b) == 0 {
		t.Errorf("old path doesn't read the panel's files: %v", err)
	}
	if _, err := os.Stat(r.logDir(nkey)); err != nil {
		t.Error("logs not moved")
	}
	if _, err := os.Stat(r.stateFile(nkey)); err != nil {
		t.Error("schedule state not moved")
	}
	if m := r.Renamed("lb"); m["board"] != "tasks" {
		t.Errorf("Renamed = %v", m)
	}

	// Taken names are refused; renaming back reuses the old name's link
	if _, err := r.RenamePanel("lb/tasks", "notes"); err == nil {
		t.Error("renamed over another panel")
	}
	if k, err := r.RenamePanel("lb/board", "board"); err != nil || k != "lb/board" {
		t.Fatalf("rename back: %q %v", k, err)
	}
	if fi, err := os.Lstat(oldDir); err != nil || !fi.IsDir() {
		t.Fatal("rename back: not a directory again")
	}
	if r.Canon("lb/tasks") != "lb/board" || r.Canon("lb/board") != "lb/board" {
		t.Errorf("Canon after rename back: %s %s", r.Canon("lb/tasks"), r.Canon("lb/board"))
	}
	if _, err := r.RenamePanel("lb", "x"); err == nil {
		t.Error("a project's own panel has no directory name to rename")
	}
}
