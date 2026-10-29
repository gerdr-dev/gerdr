package hub

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAgentVariants(t *testing.T) {
	h, err := New(Options{Dir: t.TempDir(), ExtraAgents: []AgentDef{
		{ID: "claude-2", Name: "Claude 2", Base: "claude", Env: map[string]string{"CLAUDE_CONFIG_DIR": "~/.claude-2", "A": "1"}},
		{ID: "claude", Base: "codex"},       // 和内置的撞名：丢掉
		{ID: "bad", Base: "gemini"},         // 不认识的底子：丢掉
		{ID: "ext", Command: []string{"x"}}, // 外部 agent
		{ID: "reviewer", Base: "native", Prompt: "只读代码、挑毛病", Skills: []string{}}, // 基于 Gerdr Agent 的角色
	}})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(h.extra()); n != 3 {
		t.Fatalf("应当留下 3 个，实际 %d", n)
	}
	if h.Base("reviewer") != "native" {
		t.Fatal("基于 native 的角色 Base 不对")
	}
	if h.Base("claude-2") != "claude" || h.Base("ext") != "ext" || h.Base("codex") != "codex" {
		t.Fatal("Base 不对")
	}
	home, _ := os.UserHomeDir()
	env := h.extra()[0].envList()
	if env[0] != "A=1" || env[1] != "CLAUDE_CONFIG_DIR="+filepath.Join(home, ".claude-2") {
		t.Fatalf("env 没排序或 ~ 没展开：%v", env)
	}
	// agent 跑的 `gerdr session current` 只知道自己是 claude：变体里的会话也要找得到
	s := &Session{rec: Record{ID: "g1", Agent: "claude-2", AgentSessionID: "abc"}, h: h}
	h.byAgent["claude-2:abc"] = s
	if h.FindByAgent("claude", "abc") != s || h.FindByAgent("claude-2", "abc") != s || h.FindByAgent("codex", "abc") != nil {
		t.Fatal("FindByAgent 没按底子找变体")
	}
	h.SetAgents(nil)
	if h.Base("claude-2") != "claude-2" {
		t.Fatal("删掉之后不该再认")
	}
}

func TestReadBranch(t *testing.T) {
	d := t.TempDir()
	_ = os.MkdirAll(filepath.Join(d, ".git"), 0o755)
	_ = os.WriteFile(filepath.Join(d, ".git", "HEAD"), []byte("ref: refs/heads/feat/x\n"), 0o644)
	sub := filepath.Join(d, "a", "b")
	_ = os.MkdirAll(sub, 0o755)
	if b := readBranch(sub); b != "feat/x" {
		t.Fatalf("子目录：%q", b)
	}
	// worktree：.git 是个文件
	wt, gd := t.TempDir(), t.TempDir()
	_ = os.WriteFile(filepath.Join(gd, "HEAD"), []byte("0123456789abcdef\n"), 0o644)
	_ = os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+gd+"\n"), 0o644)
	if b := readBranch(wt); b != "0123456" {
		t.Fatalf("worktree / detached：%q", b)
	}
	if b := readBranch(t.TempDir()); b != "" {
		t.Fatalf("不是仓库：%q", b)
	}
}
