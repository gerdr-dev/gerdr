package worktree

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// repo: a repository with one commit and a pkg/ folder, cloned from an "origin" so there is a default branch to start from.
func repo(t *testing.T) string {
	t.Helper()
	base, _ := filepath.EvalSymlinks(t.TempDir())
	origin := filepath.Join(base, "origin")
	os.MkdirAll(filepath.Join(origin, "pkg"), 0o755)
	run(t, origin, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(origin, "pkg", "a.txt"), []byte("a\n"), 0o644)
	run(t, origin, "add", ".")
	run(t, origin, "commit", "-qm", "init")
	r := filepath.Join(base, "repo")
	run(t, base, "clone", "-q", origin, r)
	return r
}

func TestEnterAndRemove(t *testing.T) {
	ctx := context.Background()
	r := repo(t)
	e, err := Enter(ctx, filepath.Join(r, "pkg"), "fix/thing")
	if err != nil {
		t.Fatal(err)
	}
	wantRoot := filepath.Join(r, ".gerdr", "worktrees", "fix", "thing")
	if e.Root != wantRoot || e.Dir != filepath.Join(wantRoot, "pkg") || e.Branch != "fix/thing" || e.Base != "origin/main" || e.Reused {
		t.Fatalf("entered %+v", e)
	}
	if got := run(t, e.Root, "branch", "--show-current"); got != "fix/thing" {
		t.Fatalf("branch %q", got)
	}
	// The main checkout doesn't see it as untracked
	if s := run(t, r, "status", "--porcelain"); s != "" {
		t.Fatalf("main checkout status: %q", s)
	}
	if Home(e.Dir) != filepath.Join(r, "pkg") || RootOf(e.Dir) != e.Root || Home(r) != "" {
		t.Fatalf("Home %q RootOf %q", Home(e.Dir), RootOf(e.Dir))
	}
	// Entering from inside the worktree reuses it instead of nesting another
	again, err := Enter(ctx, e.Dir, "fix/thing")
	if err != nil || !again.Reused || again.Root != e.Root {
		t.Fatalf("again %+v %v", again, err)
	}

	// Unsaved work: refused, then removed with discard
	os.WriteFile(filepath.Join(e.Dir, "b.txt"), []byte("b\n"), 0o644)
	if err := Remove(ctx, e.Root, false); !errors.Is(err, ErrUnsaved) || !strings.Contains(err.Error(), "b.txt") {
		t.Fatalf("remove with changes: %v", err)
	}
	if err := Remove(ctx, e.Root, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(e.Root); !os.IsNotExist(err) {
		t.Fatalf("still there: %v", err)
	}
	if _, err := os.Stat(filepath.Join(r, ".gerdr", "worktrees", "fix")); !os.IsNotExist(err) {
		t.Fatalf("empty parent folder left: %v", err)
	}
	if b := run(t, r, "branch", "--list", "fix/thing"); b != "" {
		t.Fatalf("branch kept: %q", b)
	}
}

func TestRemoveRefusesUnpushedCommits(t *testing.T) {
	ctx := context.Background()
	r := repo(t)
	e, err := Enter(ctx, r, "w1")
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(e.Dir, "c.txt"), []byte("c\n"), 0o644)
	run(t, e.Dir, "add", ".")
	run(t, e.Dir, "commit", "-qm", "c")
	if err := Remove(ctx, e.Root, false); !errors.Is(err, ErrUnsaved) || !strings.Contains(err.Error(), "not pushed") {
		t.Fatalf("remove with a local commit: %v", err)
	}
	// Clean and nothing local only: removed
	e2, _ := Enter(ctx, r, "w2")
	if err := Remove(ctx, e2.Root, false); err != nil {
		t.Fatal(err)
	}
}

func TestEnterNoRemoteStartsFromHEAD(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	run(t, dir, "init", "-q")
	run(t, dir, "commit", "-q", "--allow-empty", "-m", "x")
	e, err := Enter(context.Background(), dir, "w")
	if err != nil || e.Base != "HEAD" {
		t.Fatalf("%+v %v", e, err)
	}
	if _, err := Enter(context.Background(), t.TempDir(), "w"); err == nil {
		t.Fatal("no error outside a repository")
	}
	if ValidName("../x") == nil || ValidName("a b") == nil || ValidName("ok/name-1.2") != nil {
		t.Fatal("ValidName")
	}
}
