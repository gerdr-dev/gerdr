package codex

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/coder/acp-go-sdk"
)

func TestThreadConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	os.WriteFile(filepath.Join(home, "config.toml"), []byte("[mcp_servers.mine]\ncommand = \"x\"\n"), 0o644)
	a := &Agent{log: slog.New(slog.NewTextHandler(io.Discard, nil)), opts: Options{StrictMCP: true, Network: true, NoApprovals: true,
		SessionEnv: func(sid string, meta map[string]any) []string {
			return []string{"ANNULO_CHAT_ID=" + meta["chat"].(string)}
		}}}
	cfg, _ := a.threadConfig(context.Background(), nil, "", "/w", map[string]any{"chat": "c1"},
		[]acp.McpServer{{Http: &acp.McpServerHttpInline{Name: "annulo", Type: "http", Url: "http://h/mcp", Headers: []acp.HttpHeader{{Name: "Authorization", Value: "Bearer t"}}}}})
	if cfg["mcp_servers.mine.enabled"] != false {
		t.Errorf("own server not turned off: %v", cfg)
	}
	if m, _ := cfg["mcp_servers.annulo"].(map[string]any); m["url"] != "http://h/mcp" {
		t.Errorf("client server: %v", cfg)
	}
	if env, _ := cfg["shell_environment_policy.set"].(map[string]string); env["ANNULO_CHAT_ID"] != "c1" {
		t.Errorf("env: %v", cfg)
	}
	ap, sb := a.modeParams(modeAuto)
	if ap != "never" || sb["type"] != "workspaceWrite" || sb["networkAccess"] != true {
		t.Errorf("mode: %v %v", ap, sb)
	}
	// gerdr's path: nothing extra
	plain := &Agent{log: a.log}
	if cfg, _ := plain.threadConfig(context.Background(), nil, "", "/w", nil, []acp.McpServer{}); cfg != nil {
		t.Errorf("%v", cfg)
	}
	if ap, sb := plain.modeParams(modeAuto); ap != "on-request" || sb["networkAccess"] != false {
		t.Errorf("%v %v", ap, sb)
	}
}
