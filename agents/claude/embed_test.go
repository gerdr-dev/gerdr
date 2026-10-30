package claude

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/coder/acp-go-sdk"
)

func TestClientMCPArgs(t *testing.T) {
	a := &Agent{opts: Options{StrictMCP: true}}
	s := &session{a: a}
	s.setMCP([]acp.McpServer{{Http: &acp.McpServerHttpInline{Name: "annulo", Type: "http", Url: "http://127.0.0.1:9/mcp", Headers: []acp.HttpHeader{{Name: "Authorization", Value: "Bearer t"}}}}})
	args := s.clientMCPArgs()
	i := slices.Index(args, "--mcp-config")
	if i < 0 || !slices.Contains(args, "--strict-mcp-config") || !slices.Contains(args, "mcp__annulo") {
		t.Fatalf("%q", args)
	}
	var cfg struct {
		MCPServers map[string]struct {
			Type    string            `json:"type"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(args[i+1]), &cfg); err != nil {
		t.Fatal(err)
	}
	if m := cfg.MCPServers["annulo"]; m.Type != "http" || m.URL != "http://127.0.0.1:9/mcp" || m.Headers["Authorization"] != "Bearer t" {
		t.Fatalf("%+v", cfg)
	}
	// gerdr's own path: no client servers, not strict → nothing added
	if got := (&session{a: &Agent{}}).clientMCPArgs(); len(got) != 0 {
		t.Fatalf("%q", got)
	}
}
