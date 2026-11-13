package claude

import (
	"os"
	"path/filepath"
	"testing"
)

// A session moved to another folder gets its transcript (and its <id>/ folder) moved to that folder's project dir,
// where `claude --resume` looks.
func TestAdoptTranscript(t *testing.T) {
	root := t.TempDir()
	id := "0b6f2a4e-1111-2222-3333-444455556666"
	old := projectDirFor(root, "/Users/x/old/proj")
	if err := os.MkdirAll(filepath.Join(old, id, "subagents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, id+".jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := adoptTranscript(root, id, "/Users/x/new/proj"); err != nil {
		t.Fatal(err)
	}
	dst := projectDirFor(root, "/Users/x/new/proj")
	if _, err := os.Stat(filepath.Join(dst, id+".jsonl")); err != nil {
		t.Fatal("transcript not moved:", err)
	}
	if _, err := os.Stat(filepath.Join(dst, id, "subagents")); err != nil {
		t.Fatal("side folder not moved:", err)
	}
	if _, err := os.Stat(filepath.Join(old, id+".jsonl")); !os.IsNotExist(err) {
		t.Fatal("old transcript left behind")
	}
	// Already in place, or none at all: nothing happens
	if err := adoptTranscript(root, id, "/Users/x/new/proj"); err != nil {
		t.Fatal(err)
	}
	if err := adoptTranscript(root, "0b6f2a4e-9999-2222-3333-444455556666", "/Users/x/new/proj"); err != nil {
		t.Fatal(err)
	}
}
