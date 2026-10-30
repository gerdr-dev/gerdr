package webui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func w(t *testing.T, dir, rel, s string) {
	p := filepath.Join(dir, rel)
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func r(t *testing.T, dir, rel string) string {
	b, _ := os.ReadFile(filepath.Join(dir, rel))
	return string(b)
}

// 三方合并：人改的保留、gerdr 改的合进来、两边改同一处 → 冲突时不合（照旧用现在的），merge 才摆出冲突。
func TestGitMerge(t *testing.T) {
	if !hasGit() {
		t.Skip("no git")
	}
	src, dst := t.TempDir(), t.TempDir()
	w(t, src, "package.json", "{}")
	w(t, src, "src/a.tsx", "line1\nline2\nline3\n")
	w(t, src, "src/b.tsx", "b1\n")
	w(t, src, "node_modules/x.js", "skip")
	if res, err := SyncFrom(src, dst); err != nil || res.Status != "installed" {
		t.Fatalf("%+v %v", res, err)
	}
	if _, err := os.Stat(filepath.Join(dst, "node_modules")); err == nil {
		t.Fatal("node_modules copied")
	}
	// 人改 a 的第一行（不提交），gerdr 改 a 的第三行、改 b
	w(t, dst, "src/a.tsx", "LOCAL\nline2\nline3\n")
	w(t, src, "src/a.tsx", "line1\nline2\nUPSTREAM\n")
	w(t, src, "src/b.tsx", "b2\n")
	res, err := SyncFrom(src, dst)
	if err != nil || res.Status != "merged" {
		t.Fatalf("%+v %v", res, err)
	}
	if got := r(t, dst, "src/a.tsx"); got != "LOCAL\nline2\nUPSTREAM\n" {
		t.Fatalf("a: %q", got)
	}
	if r(t, dst, "src/b.tsx") != "b2\n" {
		t.Fatal("b not updated")
	}
	// 同一版再来一次：什么都不做
	if res, _ := SyncFrom(src, dst); res.Status != "up_to_date" {
		t.Fatalf("%+v", res)
	}
	// 两边都改 b：冲突，不合，目录照旧
	w(t, dst, "src/b.tsx", "mine\n")
	w(t, src, "src/b.tsx", "theirs\n")
	res, err = SyncFrom(src, dst)
	if err != nil || res.Status != "conflict" || r(t, dst, "src/b.tsx") != "mine\n" {
		t.Fatalf("%+v %v %q", res, err, r(t, dst, "src/b.tsx"))
	}
	if st := Status(dst); !st.Pending || st.Merging || len(st.PendingConflicts) != 1 || st.PendingConflicts[0] != "src/b.tsx" {
		t.Fatalf("%+v", st)
	}
	// merge：冲突摆出来
	res, err = Merge(dst)
	if err != nil || res.Status != "conflict" || !strings.Contains(r(t, dst, "src/b.tsx"), "<<<<<<<") {
		t.Fatalf("%+v %v", res, err)
	}
	if st := Status(dst); !st.Merging || len(st.Conflicts) != 1 {
		t.Fatalf("%+v", st)
	}
}

// A sync cut off after the snapshot landed on upstream but before the merge: the next sync of the same version has to
// merge it, not report up_to_date because upstream already carries that version (happened: the live UI kept old code).
func TestSyncResumesInterruptedMerge(t *testing.T) {
	if !hasGit() {
		t.Skip("no git")
	}
	src, dst := t.TempDir(), t.TempDir()
	w(t, src, "package.json", "{}")
	w(t, src, "src/a.tsx", "old\n")
	if res, err := SyncFrom(src, dst); err != nil || res.Status != "installed" {
		t.Fatalf("%+v %v", res, err)
	}
	w(t, src, "src/a.tsx", "new\n")
	files, err := ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := commitSnapshot(dst, files, manifestOf(files).Version, "test"); err != nil {
		t.Fatal(err)
	}
	if r(t, dst, "src/a.tsx") != "old\n" {
		t.Fatal("snapshot alone should not touch the working tree")
	}
	res, err := SyncFrom(src, dst)
	if err != nil || res.Status != "merged" || r(t, dst, "src/a.tsx") != "new\n" {
		t.Fatalf("%+v %v %q", res, err, r(t, dst, "src/a.tsx"))
	}
	if res, _ := SyncFrom(src, dst); res.Status != "up_to_date" {
		t.Fatalf("%+v", res)
	}
}

// 老版本（按哈希管、没改过）换成 git：直接换成新的
func TestLegacyToGit(t *testing.T) {
	if !hasGit() {
		t.Skip("no git")
	}
	dst := t.TempDir()
	old := map[string][]byte{"package.json": []byte("{}"), "src/old.tsx": []byte("x")}
	if err := write(dst, old, manifestOf(old)); err != nil {
		t.Fatal(err)
	}
	src := t.TempDir()
	w(t, src, "package.json", "{}")
	w(t, src, "src/new.tsx", "y")
	res, err := SyncFrom(src, dst)
	if err != nil || res.Status != "installed" {
		t.Fatalf("%+v %v", res, err)
	}
	if _, err := os.Stat(filepath.Join(dst, "src/old.tsx")); err == nil {
		t.Fatal("old file left")
	}
}
