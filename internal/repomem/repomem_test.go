package repomem

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRootFindsRepositoryAndWorktreeMain(t *testing.T) {
	base := t.TempDir()
	repo := filepath.Join(base, "billing")
	must(t, os.MkdirAll(filepath.Join(repo, ".git", "worktrees", "t12"), 0o755))
	must(t, os.MkdirAll(filepath.Join(repo, "src", "pkg"), 0o755))
	if got := Root(filepath.Join(repo, "src", "pkg")); got != repo {
		t.Fatalf("subdirectory: %q", got)
	}
	// A linked worktree (a panel's task worktree) gets the main repository's notes
	wt := filepath.Join(base, "billing.worktrees", "t12")
	must(t, os.MkdirAll(wt, 0o755))
	must(t, os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+filepath.Join(repo, ".git", "worktrees", "t12")+"\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(repo, ".git", "worktrees", "t12", "commondir"), []byte("../..\n"), 0o644))
	if got := Root(wt); got != repo {
		t.Fatalf("worktree: %q, want %q", got, repo)
	}
	// A plain folder is its own
	plain := filepath.Join(t.TempDir(), "general")
	must(t, os.MkdirAll(plain, 0o755))
	if got := Root(plain); got != plain {
		t.Fatalf("plain folder: %q", got)
	}
}

func TestAddReadListWrite(t *testing.T) {
	root := t.TempDir()
	repo := "/src/billing"
	if n, added, err := Add(root, repo, "ships straight to main（2026-11-27，t1）"); err != nil || !added || n.Text != "- ships straight to main（2026-11-27，t1）" {
		t.Fatalf("add: %+v %v %v", n, added, err)
	}
	if _, added, _ := Add(root, repo, "- ships straight to main（2026-11-28，t2）"); added {
		t.Fatal("same fact added twice")
	}
	Add(root, repo, "release approved by Zhang")
	n := Read(root, repo)
	if strings.Count(n.Text, "\n") != 1 || !strings.HasSuffix(n.Path, ".md") || !strings.Contains(filepath.Base(n.Path), "billing-") {
		t.Fatalf("read: %+v", n)
	}
	l := List(root)
	if len(l) != 1 || l[0].Repo != repo || l[0].Text != n.Text {
		t.Fatalf("list: %+v", l)
	}
	if _, err := Write(root, repo, ""); err != nil || len(List(root)) != 0 {
		t.Fatalf("emptied: %v %+v", err, List(root))
	}
	if _, err := Write(root, "relative", "x"); err == nil {
		t.Fatal("relative path accepted")
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
