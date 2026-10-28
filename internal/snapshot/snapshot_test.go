package snapshot

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTakeRestore(t *testing.T) {
	cwd := t.TempDir()
	st := New(t.TempDir())
	write := func(name, s string) {
		_ = os.MkdirAll(filepath.Dir(filepath.Join(cwd, name)), 0o755)
		if err := os.WriteFile(filepath.Join(cwd, name), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("keep.txt", "v1")
	write("gone.txt", "will be deleted by agent")
	write(".gitignore", "ignored/\n")
	write("ignored/big.bin", "x")
	c1, err := st.Take(cwd, "before")
	if err != nil {
		t.Fatal(err)
	}
	// agent 改了一个、删了一个、新建了一个（含新目录）、碰了一下被忽略的目录
	write("keep.txt", "v2")
	_ = os.Remove(filepath.Join(cwd, "gone.txt"))
	write("new/dir/added.txt", "new")
	write("ignored/big.bin", "changed")

	files, err := st.Changed(cwd, c1)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Fatalf("changed = %v", files)
	}
	safety, err := st.Restore(cwd, c1)
	if err != nil {
		t.Fatal(err)
	}
	read := func(name string) string {
		b, _ := os.ReadFile(filepath.Join(cwd, name))
		return string(b)
	}
	if read("keep.txt") != "v1" || read("gone.txt") != "will be deleted by agent" {
		t.Fatalf("not restored: keep=%q gone=%q", read("keep.txt"), read("gone.txt"))
	}
	if _, err := os.Stat(filepath.Join(cwd, "new")); !os.IsNotExist(err) {
		t.Fatalf("added dir should be removed: %v", err)
	}
	// 被 .gitignore 挡掉的东西不归快照管：恢复时不动它。
	if read("ignored/big.bin") != "changed" {
		t.Fatalf("ignored file touched: %q", read("ignored/big.bin"))
	}
	// 反悔：恢复到安全快照，回到恢复之前。
	if _, err := st.Restore(cwd, safety); err != nil {
		t.Fatal(err)
	}
	if read("keep.txt") != "v2" || read("new/dir/added.txt") != "new" {
		t.Fatalf("undo failed")
	}
}

func TestSkipHome(t *testing.T) {
	home, _ := os.UserHomeDir()
	if _, err := New(t.TempDir()).Take(home, "x"); err != ErrSkipped {
		t.Fatalf("home should be skipped, got %v", err)
	}
}
