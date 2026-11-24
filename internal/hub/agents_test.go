package hub

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestAgentVariants(t *testing.T) {
	h, err := New(Options{Dir: t.TempDir(), ExtraAgents: []AgentDef{
		{ID: "claude-2", Name: "Claude 2", Base: "claude", Env: map[string]string{"CLAUDE_CONFIG_DIR": "~/.claude-2", "A": "1"}},
		{ID: "claude", Base: "codex"},       // a built-in id on another engine: dropped
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

// An entry with a built-in id edits that agent; hidden takes it off the list but keeps it usable.
func TestBuiltinOverride(t *testing.T) {
	h, err := New(Options{Dir: t.TempDir(), ExtraAgents: []AgentDef{
		{ID: "claude", Name: "Main", Prompt: "be brief", Icon: "hex"},
		{ID: "codex", Hidden: true},
		{ID: "r", Base: "claude", Hidden: true}, // only built-in ones hide
	}})
	if err != nil {
		t.Fatal(err)
	}
	d, _ := h.agentDef("claude")
	if d.Base != "claude" || d.Name != "Main" || d.Prompt != "be brief" {
		t.Fatalf("override not applied: %+v", d)
	}
	if d, ok := h.agentDef("codex"); !ok || d.Name != "Codex" || d.Base != "codex" {
		t.Fatalf("a hidden built-in one must stay usable: %+v %v", d, ok)
	}
	ids := map[string]map[string]any{}
	for _, m := range h.Agents() {
		ids[m["id"].(string)] = m
	}
	if _, ok := ids["codex"]; ok {
		t.Fatal("hidden codex listed")
	}
	if ids["claude"]["name"] != "Main" || ids["claude"]["icon"] != "hex" || ids["claude"]["base"] != nil {
		t.Fatalf("claude entry: %v", ids["claude"])
	}
	if _, ok := ids["r"]; !ok {
		t.Fatal("a role can't be hidden")
	}
	if h.Base("claude") != "claude" || h.Base("r") != "claude" {
		t.Fatal("Base")
	}
}

// A rename or UI display tweak is display-only: it must not close the running host (that cuts off every turn on it).
func TestSameProcessIgnoresName(t *testing.T) {
	a := AgentDef{ID: "r", Name: "Old", Base: "native", Prompt: "p"}
	b := a
	b.Name = "New"
	b.ShowThinking = "latest"
	if !sameProcess(a, b) {
		t.Fatal("rename / showThinking change should keep the host")
	}
	b.Prompt = "q"
	if sameProcess(a, b) {
		t.Fatal("a prompt change should restart the host")
	}
}

// agentOrder controls the display order of Agents(), mixing built-in agents and custom roles; unknown ids are
// ignored and unlisted agents follow in their default order.
func TestAgentOrder(t *testing.T) {
	h, err := New(Options{
		Dir: t.TempDir(),
		ExtraAgents: []AgentDef{
			{ID: "r1", Name: "Role 1", Base: "claude"},
			{ID: "r2", Name: "Role 2", Command: []string{"x"}},
		},
		AgentOrder: []string{"r2", "codex", "missing", "r2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	ids := func() []string {
		var out []string
		for _, m := range h.Agents() {
			out = append(out, m["id"].(string))
		}
		return out
	}
	if got := ids(); !slices.Equal(got, []string{"r2", "codex", "claude", "r1"}) {
		t.Fatalf("initial order = %v, want [r2 codex claude r1]", got)
	}
	h.SetAgentOrder([]string{"r1", "claude", "r2", "codex"})
	if got := ids(); !slices.Equal(got, []string{"r1", "claude", "r2", "codex"}) {
		t.Fatalf("updated order = %v, want [r1 claude r2 codex]", got)
	}
	h.SetAgentOrder(nil)
	if got := ids(); !slices.Equal(got, []string{"claude", "codex", "r1", "r2"}) {
		t.Fatalf("default order = %v, want [claude codex r1 r2]", got)
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
