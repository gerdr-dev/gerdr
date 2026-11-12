package artifacts

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func gitInit(t *testing.T, dir string) {
	t.Helper()
	if err := exec.Command("git", "init", "-q", dir).Run(); err != nil {
		t.Skip("git not available")
	}
}

func TestForRepoSharedAcrossSubdirsAndLazy(t *testing.T) {
	root := t.TempDir()
	s := New(func() string { return root })
	repo := filepath.Join(t.TempDir(), "weave")
	gitInit(t, repo)
	sub := filepath.Join(repo, "src")
	_ = os.MkdirAll(sub, 0o755)

	p1, o1 := s.ForRepo("default", "my", repo)
	p2, _ := s.ForRepo("default", "my", sub)
	if p1 != p2 || p1 != filepath.Join(root, "my", "weave") {
		t.Fatalf("got %s and %s", p1, p2)
	}
	if _, err := os.Stat(p1); !os.IsNotExist(err) {
		t.Fatal("folder must not exist before something is put there")
	}
	f, err := s.Ensure(p1, o1, "default")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(p1, ".gerdrignore")); err != nil {
		t.Fatal("no .gerdrignore")
	}

	// Renamed in Finder: found again by id.
	moved := filepath.Join(root, "my", "weave images")
	if err := os.Rename(p1, moved); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.ForRepo("default", "my", repo); p != moved {
		t.Fatalf("after the move: %s, want %s", p, moved)
	}
	if g, _ := Read(moved); g.ID != f.ID {
		t.Fatal("identity lost")
	}

	// The project keeps its folder name when the project is renamed.
	other := filepath.Join(t.TempDir(), "knot")
	gitInit(t, other)
	if p, _ := s.ForRepo("default", "renamed", other); p != filepath.Join(root, "my", "knot") {
		t.Fatalf("project folder: %s", p)
	}
}

func TestSameNameOtherRepo(t *testing.T) {
	root := t.TempDir()
	s := New(func() string { return root })
	a := filepath.Join(t.TempDir(), "app")
	b := filepath.Join(t.TempDir(), "app")
	gitInit(t, a)
	gitInit(t, b)
	pa, oa := s.ForRepo("p", "p", a)
	if _, err := s.Ensure(pa, oa, "p"); err != nil {
		t.Fatal(err)
	}
	pb, _ := s.ForRepo("p", "p", b)
	if pb != pa+"_2" {
		t.Fatalf("second repo named app: %s", pb)
	}
}

func TestRecords(t *testing.T) {
	dir := t.TempDir()
	pr, err := Record(dir, Item{Kind: KindLink, URL: "https://git.example.com/g/r/-/merge_requests/3", Label: "MR"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Record(dir, Item{Kind: KindLink, URL: "https://git.example.com/g/r/-/merge_requests/4", Supersedes: pr.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := Record(dir, Item{Kind: KindText, Text: "account a / b"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Record(dir, Item{Kind: KindPath, Path: "relative"}); err == nil {
		t.Fatal("relative path accepted")
	}
	if _, err := Record(dir, Item{Kind: KindFile, Path: "../x"}); err == nil {
		t.Fatal("file outside the folder accepted")
	}
	items, err := Items(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 || !items[0].Superseded || items[1].Superseded {
		t.Fatalf("%+v", items)
	}
	if _, err := Record(dir, Item{Kind: KindRetire, Supersedes: items[2].ID}); err != nil {
		t.Fatal(err)
	}
	items, _ = Items(dir)
	if len(items) != 3 || !items[2].Superseded {
		t.Fatalf("retire: %+v", items)
	}
}

func TestListHidesOwnFiles(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, Meta), 0o755)
	_ = os.MkdirAll(filepath.Join(dir, "b"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "a.md"), []byte("x"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, ".DS_Store"), nil, 0o644)
	es, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(es) != 2 || es[0].Name != "b" || !es[0].Dir || es[1].Name != "a.md" {
		t.Fatalf("%+v", es)
	}
}

func TestForOwnerAndRename(t *testing.T) {
	root := t.TempDir()
	s := New(func() string { return root })
	o := Owner{Panel: "default/board", Task: "t1"}
	p := s.ForOwner("default", "my", o, "任务", "写一篇: 文章")
	if p != filepath.Join(root, "my", "任务", "写一篇_文章") {
		t.Fatalf("path: %s", p)
	}
	if got, _ := s.Rename(o, "x"); got != "" {
		t.Fatal("renaming a folder never registered")
	}
	if _, err := s.Ensure(p, o, "default"); err != nil {
		t.Fatal(err)
	}
	to, err := s.Rename(o, "新标题")
	if err != nil || to != filepath.Join(root, "my", "任务", "新标题") {
		t.Fatalf("rename: %s %v", to, err)
	}
	if again := s.ForOwner("default", "my", o, "任务", "whatever"); again != to {
		t.Fatalf("after rename: %s", again)
	}
}

func TestRecordFirstThenClaimed(t *testing.T) {
	root := t.TempDir()
	s := New(func() string { return root })
	o := Owner{Panel: "default/board", Task: "t2"}
	p := s.ForOwner("default", "my", o, "任务", "a")
	if _, err := s.Ensure(p, Owner{}, ""); err != nil { // a record before the panel looked
		t.Fatal(err)
	}
	if again := s.ForOwner("default", "my", o, "任务", "a"); again != p {
		t.Fatalf("an ownerless folder wasn't claimed: %s", again)
	}
	if _, err := s.Ensure(p, o, "default"); err != nil {
		t.Fatal(err)
	}
	if f, _ := Read(p); f.Owner != o {
		t.Fatalf("owner: %+v", f.Owner)
	}
}

func TestSafeNameNoSpaces(t *testing.T) {
	for in, want := range map[string]string{
		"随便做个 html 并截图": "随便做个_html_并截图",
		"  a \t b  ":    "a_b",
		"x: y/z":        "x_y_z",
		". hidden .":    "hidden",
		"   ":           "untitled",
	} {
		if got := safeName(in); got != want {
			t.Errorf("safeName(%q) = %q, want %q", in, got, want)
		}
	}
}
