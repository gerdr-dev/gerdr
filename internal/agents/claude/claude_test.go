package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/coder/acp-go-sdk"
)

func TestDescribeTool(t *testing.T) {
	ti := describeTool("Edit", map[string]any{"file_path": "/p/a.go", "old_string": "a", "new_string": "b"}, "/p")
	if ti.title != "Edit a.go" || ti.kind != acp.ToolKindEdit || len(ti.content) != 1 || ti.content[0].Diff == nil {
		t.Fatalf("edit: %+v", ti)
	}
	if ti := describeTool("Bash", map[string]any{"command": "go test ./..."}, ""); ti.title != "go test ./..." || ti.kind != acp.ToolKindExecute {
		t.Fatalf("bash: %+v", ti)
	}
	if ti := describeTool("mcp__github__create_issue", nil, ""); ti.title != "github · create_issue" {
		t.Fatalf("mcp: %+v", ti)
	}
}

func TestUserMessageUUID(t *testing.T) {
	m := userMessage([]acp.ContentBlock{acp.TextBlock("hi")}, "7b0e3f62-1111-4222-8333-944455556666", "now")
	if m["uuid"] != "7b0e3f62-1111-4222-8333-944455556666" || m["priority"] != "now" {
		t.Fatalf("%v", m)
	}
	// 不是 UUID 的 messageId 不能塞给 CLI（它要求 uuid 字段是 UUID）。
	if _, ok := userMessage(nil, "m1", "")["uuid"]; ok {
		t.Fatal("non-uuid id leaked")
	}
}

func TestSubReport(t *testing.T) {
	in := "  <tools-used-before-summary>Bash</tools-used-before-summary>\n  \n[Subagent hand-back] blah. The report follows:\n  SUBMARK\n  second line\nagentId: acb (use SendMessage)\n<usage>x</usage>"
	if got := subReport(in); got != "SUBMARK\nsecond line" {
		t.Fatalf("%q", got)
	}
	if got := subReport("plain"); got != "plain" {
		t.Fatalf("%q", got)
	}
}

func TestTitles(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	_ = os.WriteFile(p, []byte(`{"type":"user","message":{"role":"user","content":"hi"}}
{"type":"custom-title","customTitle":"first","sessionId":"x"}
{"type":"ai-title","aiTitle":"auto 1","sessionId":"x"}
{"type":"custom-title","customTitle":"second","sessionId":"x"}
{"type":"ai-title","aiTitle":"auto 2","sessionId":"x"}
`), 0o644)
	if c, ai := titles(p); c != "second" || ai != "auto 2" {
		t.Fatalf("%q %q", c, ai)
	}
}

func TestAllowRules(t *testing.T) {
	cases := map[string][]string{
		`git push --force origin main`:         {"Bash(git push --force origin main)"},
		`echo "a && b" && touch x; ls | wc -l`: {`Bash(echo "a && b")`, "Bash(touch x)", "Bash(ls)", "Bash(wc -l)"},
		"cd d\ncd d\nmake":                     {"Bash(cd d)", "Bash(make)"},
		`echo $(rm -rf x) && ls`:               {"Bash(echo $(rm -rf x) && ls)"},
		`echo 'unterminated && x`:              {"Bash(echo 'unterminated && x)"},
		`printf a\|b`:                          {`Bash(printf a\|b)`},
	}
	for cmd, want := range cases {
		if got := allowRules("Bash", map[string]any{"command": cmd}); !slices.Equal(got, want) {
			t.Errorf("%q: got %q want %q", cmd, got, want)
		}
	}
	if got := allowRules("Write", map[string]any{"file_path": "/etc/x"}); !slices.Equal(got, []string{"Edit(//etc/x)"}) {
		t.Errorf("write: %q", got)
	}
	if got := allowRules("WebFetch", map[string]any{"url": "https://a.example.com/p?q"}); !slices.Equal(got, []string{"WebFetch(domain:a.example.com)"}) {
		t.Errorf("webfetch: %q", got)
	}
	if got := allowRules("Agent", map[string]any{}); got != nil {
		t.Errorf("agent: %q", got)
	}
}

func TestAddAllowRules(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".claude", "settings.local.json")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte(`{"env":{"A":"1"},"permissions":{"allow":["Bash(ls)"],"deny":["Read(x)"]}}`), 0o644)
	if err := addAllowRules(dir, []string{"Bash(ls)", "Bash(make)"}); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Env         map[string]string `json:"env"`
		Permissions struct {
			Allow []string `json:"allow"`
			Deny  []string `json:"deny"`
		} `json:"permissions"`
	}
	b, _ := os.ReadFile(p)
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Env["A"] != "1" || !slices.Equal(doc.Permissions.Allow, []string{"Bash(ls)", "Bash(make)"}) || len(doc.Permissions.Deny) != 1 {
		t.Fatalf("%s", b)
	}
	// 坏的 JSON 不覆盖（那是用户的文件）。
	os.WriteFile(p, []byte(`{oops`), 0o644)
	if addAllowRules(dir, []string{"Bash(x)"}) == nil {
		t.Fatal("overwrote invalid settings")
	}
}
