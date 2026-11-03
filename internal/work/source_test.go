package work

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestRepoURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://github.com/o/r":     "https://github.com/o/r",
		"https://github.com/o/r.git": "https://github.com/o/r.git",
		"github.com/o/r/":            "https://github.com/o/r",
		"o/r":                        "https://github.com/o/r",
		"git@github.com:o/r.git":     "git@github.com:o/r.git",
	} {
		if got, err := repoURL(in); err != nil || got != want {
			t.Errorf("%q → %q %v, want %q", in, got, err, want)
		}
	}
	if _, err := repoURL("not a repo"); err == nil {
		t.Error("garbage accepted")
	}
}

// Installing adds a panel next to the project's Work (which stays as it was), with built-in files added and the
// repo's own kept; removing one moves it to the trash.
func TestPanels(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	root := t.TempDir()
	r := New(root, nil)
	dir, err := r.Ensure("p")
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "mine.txt"), []byte("x"), 0o644)

	src := filepath.Join(t.TempDir(), "task-board")
	os.MkdirAll(filepath.Join(src, "ui", "src", "pages"), 0o755)
	os.WriteFile(filepath.Join(src, "ui", "package.json"), []byte(`{"dependencies":{}}`), 0o644)
	os.WriteFile(filepath.Join(src, "ui", "src", "pages", "index.tsx"), []byte(`export default function P() { return "board" }`), 0o644)
	os.WriteFile(filepath.Join(src, "AGENTS.md"), []byte("theirs"), 0o644)
	for _, c := range [][]string{{"init", "-q"}, {"add", "."}, {"-c", "user.email=a@b", "-c", "user.name=a", "commit", "-qm", "x"}} {
		if out, err := exec.Command("git", append([]string{"-C", src}, c...)...).CombinedOutput(); err != nil {
			t.Fatal(string(out))
		}
	}

	if _, err := r.Install(context.Background(), "p", "file://"+filepath.Join(t.TempDir(), "nope"), ""); err == nil {
		t.Fatal("a bad address installed")
	}
	key, err := r.Install(context.Background(), "p", "file://"+src, "")
	if err != nil {
		t.Fatal(err)
	}
	if key != "p/task-board" {
		t.Errorf("key %q", key)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "mine.txt")); string(b) != "x" {
		t.Error("installing a panel touched the project's Work")
	}
	pd, _ := r.Dir(key)
	if b, _ := os.ReadFile(filepath.Join(pd, "AGENTS.md")); string(b) != "theirs" {
		t.Error("the repo's own AGENTS.md was overwritten")
	}
	if _, err := os.Stat(filepath.Join(pd, "local", "ctx.d.ts")); err != nil {
		t.Error("built-in files missing after install")
	}
	if k2, _ := r.Install(context.Background(), "p", "file://"+src, ""); k2 != "p/task-board-2" {
		t.Errorf("second install: %q", k2)
	}
	if k, ok := r.WorkKey(filepath.Join(pd, "ui", "src")); !ok || k != key {
		t.Errorf("WorkKey %q %v", k, ok)
	}
	if p, ok := r.WorkProject(pd); !ok || p != "p" {
		t.Errorf("WorkProject %q %v", p, ok)
	}
	if got := r.Projects(); len(got) != 3 || got[0] != "p" && got[1] != "p" {
		t.Errorf("Projects %v", got)
	}
	blank, err := r.NewPanel(DefaultProject, "草稿")
	if err != nil || blank != "default/草稿" {
		t.Fatal(blank, err)
	}
	bd, _ := r.Dir(blank)
	if b, _ := os.ReadFile(filepath.Join(bd, "ui", "src", "pages", "index.tsx")); string(b) != string(bundled()["ui/src/pages/index.tsx"]) {
		t.Error("a blank panel isn't the built-in Work")
	}
	// The project's own Work can go too, and stays away until restored
	if _, err := r.RemovePanel("p"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Ensure("p"); err != ErrWorkRemoved || !r.WorkRemoved("p") {
		t.Fatalf("removed Work came back: %v", err)
	}
	for _, k := range r.Projects() {
		if k == "p" {
			t.Error("removed Work still listed")
		}
	}
	if err := r.RestoreWork("p"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "ui", "package.json")); err != nil {
		t.Error("restored Work is empty")
	}
	trash, err := r.RemovePanel(key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(trash, "AGENTS.md")); err != nil {
		t.Error("removed panel isn't in the trash")
	}
}
