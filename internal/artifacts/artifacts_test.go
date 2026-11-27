package artifacts

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOwnerFolderLazyAndFoundByID(t *testing.T) {
	root := t.TempDir()
	s := New(func() string { return root })
	o := Owner{Panel: "default/board", Task: "t1"}

	p1 := s.ForOwner("default", "my", o, "任务", "首页配图")
	if p1 != filepath.Join(root, "my", "任务", "首页配图") {
		t.Fatalf("got %s", p1)
	}
	if _, err := os.Stat(p1); !os.IsNotExist(err) {
		t.Fatal("folder must not exist before something is put there")
	}
	f, err := s.Ensure(p1, o, "default")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(p1, ".gerdrignore")); err != nil {
		t.Fatal("no .gerdrignore")
	}

	// Renamed in Finder: found again by id.
	moved := filepath.Join(root, "my", "任务", "配图_v2")
	if err := os.Rename(p1, moved); err != nil {
		t.Fatal(err)
	}
	if p := s.ForOwner("default", "my", o, "任务", "首页配图"); p != moved {
		t.Fatalf("after the move: %s, want %s", p, moved)
	}
	if g, _ := Read(moved); g.ID != f.ID {
		t.Fatal("identity lost")
	}

	// The project keeps its folder name when the project is renamed.
	if p := s.ForOwner("default", "renamed", Owner{Panel: "default/board", Task: "t2"}, "任务", "x"); p != filepath.Join(root, "my", "任务", "x") {
		t.Fatalf("project folder: %s", p)
	}
}

func TestSameNameOtherOwner(t *testing.T) {
	root := t.TempDir()
	s := New(func() string { return root })
	oa, ob := Owner{Panel: "p/board", Task: "t1"}, Owner{Panel: "p/board", Task: "t2"}
	pa := s.ForOwner("p", "p", oa, "任务", "app")
	if _, err := s.Ensure(pa, oa, "p"); err != nil {
		t.Fatal(err)
	}
	if pb := s.ForOwner("p", "p", ob, "任务", "app"); pb != pa+"_2" {
		t.Fatalf("second task named app: %s", pb)
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
	if got, _ := s.Rename(Owner{Panel: "default/board", Task: "never"}, "x"); got != "" {
		t.Fatal("renaming a folder never handed out")
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

// Two blank tasks both titled the same, neither folder created yet: each has its own path. Before, both were handed
// 新会话_3, and the second adopted the first one's files.
func TestSameNameReservedBeforeCreated(t *testing.T) {
	root := t.TempDir()
	s := New(func() string { return root })
	oa, ob := Owner{Panel: "p/board", Task: "t1"}, Owner{Panel: "p/board", Task: "t2"}
	pa := s.ForOwner("p", "p", oa, "任务", "新会话")
	pb := s.ForOwner("p", "p", ob, "任务", "新会话")
	if pa == pb {
		t.Fatalf("both got %s", pa)
	}
	// The first task's agent makes its folder by itself; looked at by session (no owner known), it is still t1's
	_ = os.MkdirAll(filepath.Join(pa, "shots"), 0o755)
	f, err := s.Ensure(pa, Owner{}, "")
	if err != nil || f.Owner != oa {
		t.Fatalf("owner %+v %v", f.Owner, err)
	}
	if again := s.ForOwner("p", "p", ob, "任务", "新会话"); again != pb {
		t.Fatalf("t2 moved to %s", again)
	}
}

// A conversation keeps the path it started with; after its task was renamed, writes there land in the renamed
// folder, and a newcomer of the old name doesn't get that path.
func TestRenameKeepsOldPathWorking(t *testing.T) {
	root := t.TempDir()
	s := New(func() string { return root })
	o := Owner{Panel: "p/board", Task: "t1"}
	old := s.ForOwner("p", "p", o, "任务", "")
	if filepath.Base(old) != ".t1" {
		t.Fatalf("untitled: %s", old)
	}
	to, err := s.Rename(o, "办公室") // not created yet: created, then named
	if err != nil || to != filepath.Join(root, "p", "任务", "办公室") {
		t.Fatalf("rename: %s %v", to, err)
	}
	if err := os.WriteFile(filepath.Join(old, "a.png"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(to, "a.png")); err != nil {
		t.Fatal("a write to the old path missed the folder")
	}
	f, err := s.Ensure(old, Owner{}, "")
	if err != nil || f.Path != to || f.Owner != o {
		t.Fatalf("by the old path: %+v %v", f, err)
	}
	if p := s.ForOwner("p", "p", o, "任务", "x"); p != to {
		t.Fatalf("index after looking by the old path: %s", p)
	}
	// Named back: the link is replaced by the folder
	back, err := s.Rename(o, "办公室2")
	if err != nil {
		t.Fatal(err)
	}
	if back2, _ := s.Rename(o, "办公室"); back2 != to {
		t.Fatalf("renamed back: %s (from %s)", back2, back)
	}
}

func TestSafeNameNoSpaces(t *testing.T) {
	for in, want := range map[string]string{
		"随便做个 html 并截图":        "随便做个_html_并截图",
		"  a \t b  ":           "a_b",
		"x: y/z":               "x_y_z",
		". hidden .":           "hidden",
		"   ":                  "untitled",
		"这个 子产品是什么逻辑？":         "这个_子产品是什么逻辑",
		"币种错：UOB 是 USD，显示 HKD": "币种错_UOB_是_USD_显示_HKD",
		"https://a.b-c.com/x…": "https_a.b-c.com_x",
	} {
		if got := safeName(in); got != want {
			t.Errorf("safeName(%q) = %q, want %q", in, got, want)
		}
	}
}

// Conversations started before paths were reserved share one path (新会话_3): each finds its own task's folder.
func TestForSessionSharedPath(t *testing.T) {
	root := t.TempDir()
	s := New(func() string { return root })
	oa, ob, oc := Owner{Panel: "p/board", Task: "t1"}, Owner{Panel: "p/board", Task: "t2"}, Owner{Panel: "p/board", Task: "t3"}
	shared := filepath.Join(root, "p", "任务", "新会话_3")
	if got := s.ForSession(shared, oa, "a"); got != shared {
		t.Fatalf("nobody's path: %s", got)
	}
	if _, err := s.Ensure(shared, oa, "p"); err != nil {
		t.Fatal(err)
	}
	if got := s.ForSession(shared, oa, "a"); got != shared {
		t.Fatalf("its own: %s", got)
	}
	pb := s.ForSession(shared, ob, "办公室")
	if pb != filepath.Join(root, "p", "任务", "办公室") {
		t.Fatalf("another's: %s", pb)
	}
	if again := s.ForOwner("p", "p", ob, "任务", "whatever"); again != pb {
		t.Fatalf("panel sees %s", again)
	}
	c := s.ForOwner("p", "p", oc, "任务", "办公室")
	if c == pb {
		t.Fatal("same name reused")
	}
	if got := s.ForSession(shared, oc, "办公室"); got != c {
		t.Fatalf("known folder first: %s", got)
	}
}
