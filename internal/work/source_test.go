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

// Installing clones over the Work (the old one to the trash, built-in files added, the repo's own kept); a failed
// clone leaves the old one where it was.
func TestInstallAndReset(t *testing.T) {
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

	// a repository that is a Work
	src := t.TempDir()
	os.MkdirAll(filepath.Join(src, "ui", "src", "pages"), 0o755)
	os.WriteFile(filepath.Join(src, "ui", "package.json"), []byte(`{"dependencies":{}}`), 0o644)
	os.WriteFile(filepath.Join(src, "ui", "src", "pages", "index.tsx"), []byte(`export default function P() { return "board" }`), 0o644)
	os.WriteFile(filepath.Join(src, "AGENTS.md"), []byte("theirs"), 0o644)
	for _, c := range [][]string{{"init", "-q"}, {"add", "."}, {"-c", "user.email=a@b", "-c", "user.name=a", "commit", "-qm", "x"}} {
		if out, err := exec.Command("git", append([]string{"-C", src}, c...)...).CombinedOutput(); err != nil {
			t.Fatal(string(out))
		}
	}

	if _, err := r.Install(context.Background(), "p", "file://"+filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("a bad address installed")
	}
	if _, err := os.Stat(filepath.Join(dir, "mine.txt")); err != nil {
		t.Fatal("a failed install lost the old Work")
	}

	trash, err := r.Install(context.Background(), "p", "file://"+src)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(trash, "mine.txt")); string(b) != "x" {
		t.Error("the old Work isn't in the trash")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "ui", "src", "pages", "index.tsx")); string(b) != `export default function P() { return "board" }` {
		t.Errorf("repo's page replaced: %s", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "AGENTS.md")); string(b) != "theirs" {
		t.Error("the repo's own AGENTS.md was overwritten")
	}
	if _, err := os.Stat(filepath.Join(dir, "local", "ctx.d.ts")); err != nil {
		t.Error("built-in files missing after install")
	}
	if s, _ := r.Source("p"); !s.Git || s.Remote == "" {
		t.Errorf("source %+v", s)
	}

	if _, err := r.Reset("p"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "ui", "src", "pages", "index.tsx")); string(b) != string(bundled()["ui/src/pages/index.tsx"]) {
		t.Error("reset didn't bring the blank Work back")
	}
}
