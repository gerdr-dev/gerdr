package native

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
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

// useMCP gives the test agent these MCP servers (Options.MCP).
func useMCP(t *testing.T, e *env, servers ...MCPServer) {
	t.Helper()
	e.ag.opt.MCP = func(string, map[string]any) []MCPServer { return servers }
}

func TestMCPStdio(t *testing.T) {
	e := setup(t, mcpScript("mcp__fake__echo"))
	useMCP(t, e, MCPServer{Name: "fake", Command: os.Args[0], Env: map[string]string{"FAKE_MCP": "1"}})
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

// /mcp answers from the agent itself: no model call, nothing added to the history.
func TestMCPCommand(t *testing.T) {
	e := setup(t, mcpScript("mcp__fake__echo"))
	useMCP(t, e,
		MCPServer{Name: "fake", Command: os.Args[0], Env: map[string]string{"FAKE_MCP": "1"}},
		MCPServer{Name: "broken", Command: "/nonexistent/mcp-server"},
	)
	sid := e.newSession(modeDefault)
	say := func(text string) string {
		t.Helper()
		e.h.mu.Lock()
		from := len(e.h.updates)
		e.h.mu.Unlock()
		if _, err := e.prompt(sid, uuid.NewString(), text); err != nil {
			t.Fatal(err)
		}
		e.h.mu.Lock()
		defer e.h.mu.Unlock()
		var b strings.Builder
		for _, n := range e.h.updates[from:] {
			if c := n.Update.AgentMessageChunk; c != nil && c.Content.Text != nil {
				b.WriteString(c.Content.Text.Text)
			}
		}
		return b.String()
	}
	out := say("/mcp")
	for _, want := range []string{"**fake**", "已连上，1 个工具", "`echo`", "**broken**", "没连上", "/nonexistent/mcp-server"} {
		if !strings.Contains(out, want) {
			t.Fatalf("/mcp output lacks %q:\n%s", want, out)
		}
	}
	if out := say("/mcp fake"); !strings.Contains(out, "Echo text") || !strings.Contains(out, "只读") || strings.Contains(out, "broken") {
		t.Fatalf("/mcp fake:\n%s", out)
	}
	if out := say("/mcp reconnect fake"); !strings.Contains(out, "已重连") || !strings.Contains(out, "已连上") {
		t.Fatalf("/mcp reconnect fake:\n%s", out)
	}
	if out := say("/mcp nope"); !strings.Contains(out, "没有叫 `nope`") {
		t.Fatalf("/mcp nope:\n%s", out)
	}
	if n := e.llm.count(); n != 0 {
		t.Fatalf("/mcp called the model %d times", n)
	}
	s, _ := e.ag.session(sid)
	if msgs := s.contextMessages(); len(msgs) != 0 {
		t.Fatalf("/mcp went into the history: %v", msgs)
	}
	// The reconnected server is used by the next real turn.
	if _, err := e.prompt(sid, uuid.NewString(), "use it"); err != nil {
		t.Fatal(err)
	}
	if c := e.llm.request(1)["messages"].([]any)[3].(map[string]any)["content"]; c != "echo: hi" {
		t.Fatalf("mcp result after reconnect %v", c)
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
	useMCP(t, e, MCPServer{Name: "web", Type: "http", URL: srv.URL})
	sid := e.newSession(modeBypass)
	if _, err := e.prompt(sid, uuid.NewString(), "use it"); err != nil {
		t.Fatal(err)
	}
	msgs := e.llm.request(1)["messages"].([]any)
	if c := msgs[3].(map[string]any)["content"]; !strings.Contains(fmt.Sprint(c), "echo: hi") {
		t.Fatalf("mcp result %v", c)
	}
}
