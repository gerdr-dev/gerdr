package ext

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestFrontmatter(t *testing.T) {
	n, d := frontmatter("---\nname: a\ndescription: >\n  line one\n  line two\nother: x\n---\nbody")
	if n != "a" || d != "line one line two" {
		t.Fatalf("%q %q", n, d)
	}
	n, d = frontmatter("---\nname: \"b\"\ndescription: 'one line'\n---\n")
	if n != "b" || d != "one line" {
		t.Fatalf("%q %q", n, d)
	}
}

// claude 自己配的 MCP 变了（claude mcp add）指纹要变，~/.claude.json 里别的东西变了不能变（不然每轮都换进程）。
func TestClaudeMCPFingerprint(t *testing.T) {
	h := t.TempDir()
	t.Setenv("HOME", h)
	cwd := t.TempDir()
	write := func(s string) {
		if err := os.WriteFile(filepath.Join(h, ".claude.json"), []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var m *Manager
	write(`{"numStartups":1,"mcpServers":{}}`)
	a := m.ClaudeMCPFingerprint(cwd)
	write(`{"numStartups":2,"mcpServers":{}}`)
	if b := m.ClaudeMCPFingerprint(cwd); b != a {
		t.Fatalf("无关字段变了指纹也变了：%s → %s", a, b)
	}
	write(`{"numStartups":2,"mcpServers":{},"projects":{"` + cwd + `":{"mcpServers":{"foxden":{"type":"http","url":"https://x/mcp"}}}}}`)
	if b := m.ClaudeMCPFingerprint(cwd); b == a {
		t.Fatal("项目里加了 MCP，指纹没变")
	}
}

// 角色只挑了几个 skill / MCP：claude 自己配的里没挑的要关掉，MCP 只剩挑中的而且 strict（别的一概不读），
// 内置 skill 没挑的也不链进壳目录。
func TestClaudeArgsScoped(t *testing.T) {
	h := t.TempDir()
	t.Setenv("HOME", h)
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(h, ".claude.json"), []byte(`{"mcpServers":{"a":{"command":"x"},"b":{"command":"y"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"keep", "drop"} {
		d := filepath.Join(h, ".claude", "skills", n)
		_ = os.MkdirAll(d, 0o755)
		_ = os.WriteFile(filepath.Join(d, "SKILL.md"), []byte("---\nname: "+n+"\ndescription: d\n---\n"), 0o644)
	}
	m := New(t.TempDir()).Scoped(Only{Skills: []string{"keep", "gerdr"}, MCP: []string{"a"}})
	args := m.ClaudeArgs("s1", cwd)
	get := func(flag string) string {
		for i, a := range args {
			if a == flag && i+1 < len(args) {
				return args[i+1]
			}
		}
		return ""
	}
	var mc struct {
		MCPServers map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(get("--mcp-config")), &mc); err != nil || len(mc.MCPServers) != 1 || mc.MCPServers["a"] == nil {
		t.Fatalf("--mcp-config 应当只有 a：%v %v", mc.MCPServers, err)
	}
	if !slices.Contains(args, "--strict-mcp-config") {
		t.Fatal("限定了 MCP 要 --strict-mcp-config")
	}
	var st struct {
		SkillOverrides map[string]string `json:"skillOverrides"`
	}
	_ = json.Unmarshal([]byte(get("--settings")), &st)
	if st.SkillOverrides["drop"] != "off" || st.SkillOverrides["keep"] != "" {
		t.Fatalf("skillOverrides 不对：%v", st.SkillOverrides)
	}
	links, _ := os.ReadDir(filepath.Join(get("--add-dir"), ".claude", "skills"))
	var names []string
	for _, l := range links {
		names = append(names, l.Name())
	}
	if !slices.Equal(names, []string{"gerdr"}) {
		t.Fatalf("壳目录里应当只有挑中的内置 skill：%v", names)
	}
	// 没限定的 Manager 不带这些
	if args := New(t.TempDir()).ClaudeArgs("s2", cwd); slices.Contains(args, "--strict-mcp-config") {
		t.Fatalf("没限定不该 strict：%v", args)
	}
}

// 以前 Native 借着用的 Claude Code MCP，升级后导入到 Gerdr 那份一次；已有的同名不动，第二次不再导。
func TestMigrateNativeMCP(t *testing.T) {
	h := t.TempDir()
	t.Setenv("HOME", h)
	_ = os.WriteFile(filepath.Join(h, ".claude.json"), []byte(`{"mcpServers":{"okr":{"type":"http","url":"https://x/mcp"},"mine":{"command":"y"}}}`), 0o600)
	m := New(t.TempDir())
	st := m.Settings()
	st.MCP = []MCPServer{{Name: "mine", Command: "keep", Enabled: false}}
	_ = m.Save(st)
	added, err := m.MigrateNativeMCP()
	if err != nil || !slices.Equal(added, []string{"okr"}) {
		t.Fatalf("应当只导入 okr：%v %v", added, err)
	}
	got := m.Settings().MCP
	if len(got) != 2 || got[0].Command != "keep" || got[1].Name != "okr" || !got[1].Enabled || got[1].Type != "http" {
		t.Fatalf("导入结果不对：%+v", got)
	}
	_ = os.WriteFile(filepath.Join(h, ".claude.json"), []byte(`{"mcpServers":{"later":{"command":"z"}}}`), 0o600)
	if added, _ := m.MigrateNativeMCP(); len(added) != 0 {
		t.Fatalf("只做一次：%v", added)
	}
}
