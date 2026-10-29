package ext

import (
	"os"
	"path/filepath"
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
