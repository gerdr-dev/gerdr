package native

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/gerdr-dev/gerdr/internal/ext"
)

// 测试二进制自己当 stdio MCP 服务器：FAKE_MCP=1 时 TestMain 直接跑服务器。
func TestMain(m *testing.M) {
	if os.Getenv("FAKE_MCP") == "1" {
		serveFakeMCP(os.Stdin, os.Stdout)
		return
	}
	os.Exit(m.Run())
}

func fakeMCPHandle(msg map[string]any) any {
	switch msg["method"] {
	case "initialize":
		return map[string]any{"protocolVersion": mcpProtocol, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "fake"}}
	case "tools/list":
		return map[string]any{"tools": []any{
			map[string]any{"name": "echo", "description": "Echo text", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}},
				"annotations": map[string]any{"readOnlyHint": true}},
		}}
	case "tools/call":
		p := msg["params"].(map[string]any)
		args, _ := p["arguments"].(map[string]any)
		return map[string]any{"content": []any{map[string]any{"type": "text", "text": fmt.Sprintf("echo: %v", args["text"])}}}
	}
	return nil
}

func serveFakeMCP(in io.Reader, out io.Writer) {
	sc := bufio.NewScanner(in)
	for sc.Scan() {
		var msg map[string]any
		if json.Unmarshal(sc.Bytes(), &msg) != nil || msg["id"] == nil {
			continue
		}
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": msg["id"], "result": fakeMCPHandle(msg)})
		fmt.Fprintf(out, "%s\n", b)
	}
}

func mcpScript(tool string) func(n int, msgs []any) reply {
	return func(n int, msgs []any) reply {
		if n == 0 {
			return reply{calls: [][2]string{{tool, `{"text":"hi"}`}}}
		}
		return reply{text: "ok"}
	}
}

// useMCP：Native 引擎只用 Gerdr 那份 MCP（ext.json），给测试的 agent 配一份。
func useMCP(t *testing.T, e *env, servers ...ext.MCPServer) {
	t.Helper()
	m := ext.New(t.TempDir())
	st := m.Settings()
	st.MCP = servers
	if err := m.Save(st); err != nil {
		t.Fatal(err)
	}
	e.ag.ext = m
}

// 以前「Agent 自己的」对 Native 来说是借 Claude Code 的：现在项目里的 .mcp.json、~/.claude.json 都不该被它用上。
func TestMCPNotBorrowedFromClaude(t *testing.T) {
	e := setup(t, mcpScript("mcp__fake__echo"))
	cfg, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"fake": map[string]any{"command": os.Args[0], "env": map[string]string{"FAKE_MCP": "1"}}}})
	os.WriteFile(filepath.Join(e.cwd, ".mcp.json"), cfg, 0o644)
	e.ag.ext = ext.New(t.TempDir())
	if got := resolveMCP(e.ag.ext, e.cwd); len(got) != 0 {
		t.Fatalf("不该借 Claude Code 的 MCP：%v", got)
	}
}

func TestMCPStdio(t *testing.T) {
	e := setup(t, mcpScript("mcp__fake__echo"))
	useMCP(t, e, ext.MCPServer{Name: "fake", Command: os.Args[0], Env: map[string]string{"FAKE_MCP": "1"}, Enabled: true})
	sid := e.newSession(modeDefault) // echo 标了只读：默认模式也不该来问
	if _, err := e.prompt(sid, uuid.NewString(), "use it"); err != nil {
		t.Fatal(err)
	}
	msgs := e.llm.request(1)["messages"].([]any)
	if c := msgs[3].(map[string]any)["content"]; c != "echo: hi" {
		t.Fatalf("mcp result %v", c)
	}
	tools := e.llm.request(0)["tools"].([]any)
	found := false
	for _, x := range tools {
		if x.(map[string]any)["function"].(map[string]any)["name"] == "mcp__fake__echo" {
			found = true
		}
	}
	if !found {
		t.Fatal("mcp tool not offered to the model")
	}
}

func TestMCPHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var msg map[string]any
		json.NewDecoder(r.Body).Decode(&msg)
		w.Header().Set("Mcp-Session-Id", "s1")
		if msg["id"] == nil {
			w.WriteHeader(202)
			return
		}
		if msg["method"] != "initialize" && r.Header.Get("Mcp-Session-Id") != "s1" {
			http.Error(w, "no session", 400)
			return
		}
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": msg["id"], "result": fakeMCPHandle(msg)})
		// 响应用 SSE 发（streamable HTTP 允许两种）
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "event: message\ndata: %s\n\n", b)
	}))
	defer srv.Close()
	e := setup(t, mcpScript("mcp__web__echo"))
	useMCP(t, e, ext.MCPServer{Name: "web", Type: "http", URL: srv.URL, Enabled: true})
	sid := e.newSession(modeBypass)
	if _, err := e.prompt(sid, uuid.NewString(), "use it"); err != nil {
		t.Fatal(err)
	}
	msgs := e.llm.request(1)["messages"].([]any)
	if c := msgs[3].(map[string]any)["content"]; !strings.Contains(fmt.Sprint(c), "echo: hi") {
		t.Fatalf("mcp result %v", c)
	}
}
