package native

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/acp-go-sdk"
	"github.com/google/uuid"

	"github.com/gerdr-dev/gerdr/internal/agenthost"
)

// 真调模型（花钱）：NATIVE_LIVE_BASE / NATIVE_LIVE_KEY 给地址和 key，NATIVE_LIVE_MODELS 给模型 id（逗号分隔）。
//
//	NATIVE_LIVE_MODELS=gpt-6.1-sol,deepseek-v4-pro go test ./native -run Live -v
func TestLiveAgent(t *testing.T) {
	base, key := os.Getenv("NATIVE_LIVE_BASE"), os.Getenv("NATIVE_LIVE_KEY")
	if base == "" || key == "" {
		t.Skip("NATIVE_LIVE_BASE / NATIVE_LIVE_KEY not set")
	}
	for _, model := range strings.Split(os.Getenv("NATIVE_LIVE_MODELS"), ",") {
		t.Run(model, func(t *testing.T) {
			cwd := t.TempDir()
			os.WriteFile(filepath.Join(cwd, "calc.go"), []byte("package calc\n\nfunc Add(a, b int) int {\n\treturn a - b\n}\n"), 0o644)
			os.WriteFile(filepath.Join(cwd, "go.mod"), []byte("module calc\n\ngo 1.22\n"), 0o644)
			cfg := Config{Providers: []Provider{{ID: "live", BaseURL: base, APIKey: key, Models: []Model{{ID: model}}}}}
			ag := New(slog.New(slog.NewTextHandler(io.Discard, nil)), Options{Dir: t.TempDir(), Config: func() Config { return cfg }})
			h := &handler{}
			c, err := agenthost.StartInProc(context.Background(), ag, h, slog.New(slog.NewTextHandler(io.Discard, nil)))
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			ns, err := c.Conn.NewSession(context.Background(), acp.NewSessionRequest{Cwd: cwd, McpServers: []acp.McpServer{}})
			if err != nil {
				t.Fatal(err)
			}
			_, _ = c.Conn.SetSessionMode(context.Background(), acp.SetSessionModeRequest{SessionId: ns.SessionId, ModeId: modeBypass})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			start := time.Now()
			res, err := c.Conn.Prompt(ctx, acp.PromptRequest{SessionId: ns.SessionId, MessageId: acp.Ptr(uuid.NewString()), Prompt: []acp.ContentBlock{acp.TextBlock(
				"calc.go has a bug in Add. Fix it, then add a small test file for Add and run `go test ./...` to verify. Reply in one sentence.")}})
			if err != nil {
				t.Fatal(err)
			}
			b, _ := os.ReadFile(filepath.Join(cwd, "calc.go"))
			var tools []string
			var text strings.Builder
			h.mu.Lock()
			for _, n := range h.updates {
				if tc := n.Update.ToolCall; tc != nil {
					tools = append(tools, tc.Title)
				}
				if m := n.Update.AgentMessageChunk; m != nil && m.Content.Text != nil {
					text.WriteString(m.Content.Text.Text)
				}
			}
			h.mu.Unlock()
			t.Logf("%s in %s: stop=%s tools=%q\nreply: %s", model, time.Since(start).Round(time.Second), res.StopReason, tools, text.String())
			if !strings.Contains(string(b), "a + b") {
				t.Fatalf("not fixed:\n%s", b)
			}
		})
	}
}
