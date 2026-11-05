package agentwire

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/gerdr-dev/gerdr/internal/ext"
)

// 以前「Agent 自己的」对 Native 来说是借 Claude Code 的：现在项目里的 .mcp.json、~/.claude.json 都不该被它用上。
func TestMCPNotBorrowedFromClaude(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cwd := t.TempDir()
	cfg, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"fake": map[string]any{"command": "x"}}})
	os.WriteFile(filepath.Join(cwd, ".mcp.json"), cfg, 0o644)
	o := NativeOptions(t.TempDir(), ext.New(t.TempDir()), nil, "")
	if got := o.MCP(cwd, nil); len(got) != 0 {
		t.Fatalf("不该借 Claude Code 的 MCP：%v", got)
	}
}

func TestEnabledMCPOnly(t *testing.T) {
	m := ext.New(t.TempDir())
	st := m.Settings()
	st.MCP = []ext.MCPServer{{Name: "on", Command: "a", Enabled: true}, {Name: "off", Command: "b"}}
	if err := m.Save(st); err != nil {
		t.Fatal(err)
	}
	got := NativeOptions(t.TempDir(), m, nil, "").MCP("", nil)
	if len(got) != 1 || got[0].Name != "on" || got[0].Command != "a" {
		t.Fatalf("%+v", got)
	}
}

// The project in a session's _meta limits what it gets.
func TestProjectFromMeta(t *testing.T) {
	m := ext.New(t.TempDir())
	st := m.Settings()
	st.MCP = []ext.MCPServer{{Name: "a", Command: "a", Enabled: true}, {Name: "b", Command: "b", Enabled: true}}
	if err := m.Save(st); err != nil {
		t.Fatal(err)
	}
	m.SetProjectOff(func(p string) ext.Only {
		if p == "lb" {
			return ext.Only{MCP: []string{"a"}}
		}
		return ext.Only{}
	})
	o := NativeOptions(t.TempDir(), m, nil, "")
	if got := o.MCP("", map[string]any{"gerdr": map[string]any{ProjectKey: "lb"}}); len(got) != 1 || got[0].Name != "b" {
		t.Fatalf("lb: %+v", got)
	}
	if got := o.MCP("", map[string]any{"gerdr": map[string]any{ProjectKey: "default"}}); len(got) != 2 {
		t.Fatalf("default: %+v", got)
	}
	c := ClaudeExt(m)
	if c.Fingerprint(map[string]any{"gerdr": map[string]any{ProjectKey: "lb"}}) == c.Fingerprint(nil) {
		t.Fatal("claude: the project must change the fingerprint")
	}
}
