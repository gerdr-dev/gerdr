package webui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRestore(t *testing.T) {
	if !hasGit() {
		t.Skip("no git")
	}
	dir := t.TempDir()
	if _, err := initRepo(dir, map[string][]byte{"src/a.tsx": []byte("good")}, "1", ""); err != nil {
		t.Fatal(err)
	}
	h, err := History(dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	good := h[0].Sha
	// agent 改坏：改了一个、加了一个，还没提交
	w(t, dir, "src/a.tsx", "broken")
	w(t, dir, "src/b.tsx", "new")
	saved, err := Restore(dir, good)
	if err != nil {
		t.Fatal(err)
	}
	if saved == "" {
		t.Fatal("坏的那版没存档")
	}
	if got := r(t, dir, "src/a.tsx"); got != "good" {
		t.Errorf("a.tsx = %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "src/b.tsx")); !os.IsNotExist(err) {
		t.Errorf("b.tsx 应该被删掉")
	}
	// 坏的那版还在历史里，找得回来
	if out, _ := git(dir, nil, "show", saved+":src/b.tsx"); strings.TrimSpace(out) != "new" {
		t.Errorf("存档里没有 b.tsx：%q", out)
	}
	if Status(dir).Uncommited != 0 {
		t.Error("回退之后应该已经提交")
	}
	if _, err := Restore(dir, "../../etc"); err == nil {
		t.Error("乱写的版本号应该报错")
	}
}
