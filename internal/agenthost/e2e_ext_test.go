package agenthost_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/acp-go-sdk"
	"github.com/google/uuid"

	"github.com/gerdr-dev/gerdr/internal/acpx"
	"github.com/gerdr-dev/gerdr/internal/ext"
)

// 测试二进制自己兼一个最小的 stdio MCP 服务（GERDR_TEST_MCP=1 时）：一个工具 secret_word，返回 PAPAYA。
func TestMain(m *testing.M) {
	if os.Getenv("GERDR_TEST_MCP") == "1" {
		serveMCP()
		return
	}
	os.Exit(m.Run())
}

func serveMCP() {
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 1<<20), 1<<20)
	for in.Scan() {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if json.Unmarshal(in.Bytes(), &req) != nil || req.ID == nil {
			continue
		}
		var res any
		switch req.Method {
		case "initialize":
			res = map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "gerdrtest", "version": "1"}}
		case "tools/list":
			res = map[string]any{"tools": []any{map[string]any{"name": "secret_word", "description": "Returns today's secret word.", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}}}}
		case "tools/call":
			res = map[string]any{"content": []any{map[string]any{"type": "text", "text": "PAPAYA"}}}
		default:
			res = map[string]any{}
		}
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": res})
		fmt.Println(string(b))
	}
}

// TestE2EExt：skill / MCP 只用 gerdr 的。gerdr 的 skill 和 MCP 能用、agent 自己的 skill 关掉了。
func TestE2EExt(t *testing.T) {
	which := os.Getenv("GERDR_E2E")
	if which == "" {
		t.Skip("set GERDR_E2E=codex|claude")
	}
	dir := t.TempDir()
	em := ext.New(dir)
	if err := em.WriteSkill("gerdrprobe", "---\nname: gerdrprobe\ndescription: Use when the user asks about the gerdr probe.\n---\nAnswer PROBE.\n"); err != nil {
		t.Fatal(err)
	}
	self, _ := os.Executable()
	if err := em.Save(ext.Settings{SkillSource: ext.SourceGerdr, MCPSource: ext.SourceGerdr, MCP: []ext.MCPServer{
		{Name: "gerdrtest", Command: self, Env: map[string]string{"GERDR_TEST_MCP": "1"}, Enabled: true},
	}}); err != nil {
		t.Fatal(err)
	}
	var agentSkill string
	if which == "claude" {
		for _, sk := range ext.ClaudeSkills("") {
			if sk.Origin == "claude" {
				agentSkill = sk.Name
				break
			}
		}
	} else if l := ext.CodexSkills(); len(l) > 0 {
		agentSkill = l[0].Name
	}
	t.Logf("agent skill to check: %q", agentSkill)

	r := &rec{t: t}
	h := startExt(t, which, r, em)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	meta := acpx.Meta(map[string]any{"mode": "full-access"})
	if which == "claude" {
		meta = acpx.Meta(map[string]any{"mode": "bypassPermissions", "model": "haiku"})
	}
	ns, err := h.Conn.NewSession(ctx, acp.NewSessionRequest{Cwd: t.TempDir(), McpServers: []acp.McpServer{}, Meta: meta})
	if err != nil {
		t.Fatal(err)
	}
	ask := func(q string) string {
		t.Helper()
		if _, err := h.Conn.Prompt(ctx, acp.PromptRequest{SessionId: ns.SessionId, MessageId: acp.Ptr(uuid.NewString()), Prompt: []acp.ContentBlock{acp.TextBlock(q)}}); err != nil {
			t.Fatal(err)
		}
		return r.reset()
	}
	if which == "claude" {
		// 新会话的进程在后台起（NewSession 不等它），斜杠命令要等进程 initialize 完才推过来
		var cmds string
		for deadline := time.Now().Add(60 * time.Second); ; time.Sleep(200 * time.Millisecond) {
			r.mu.Lock()
			cmds = strings.Join(r.cmds, " ")
			r.mu.Unlock()
			if strings.Contains(" "+cmds+" ", " gerdrprobe ") || time.Now().After(deadline) {
				break
			}
		}
		if !strings.Contains(" "+cmds+" ", " gerdrprobe ") {
			t.Errorf("gerdr skill missing from commands")
		}
		if agentSkill != "" && strings.Contains(" "+cmds+" ", " "+agentSkill+" ") {
			t.Errorf("agent skill %q still present", agentSkill)
		}
	}
	got := ask(fmt.Sprintf("Without using tools: are skills named exactly 'gerdrprobe' and '%s' in your available skills list? Answer exactly like: gerdrprobe=YES/NO other=YES/NO", agentSkill))
	t.Logf("skills: %q", got)
	if !strings.Contains(got, "gerdrprobe=YES") || (agentSkill != "" && !strings.Contains(got, "other=NO")) {
		t.Errorf("skills: %q", got)
	}
	got = ask("Call the secret_word tool (from the gerdrtest MCP server) and reply with exactly the word it returns.")
	t.Logf("mcp: %q tools=%v", got, r.tools)
	if !strings.Contains(got, "PAPAYA") {
		t.Errorf("mcp: %q", got)
	}
}
