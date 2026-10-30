package native

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/coder/acp-go-sdk"
	"github.com/google/uuid"
)

func systemText(req map[string]any) string {
	msgs := req["messages"].([]any)
	m := msgs[0].(map[string]any)
	if m["role"] != "system" {
		return ""
	}
	s, _ := m["content"].(string)
	return s
}

// The embedding hooks: a replaced system prompt without instruction files or memory, a caller tool that sees the
// session's _meta, bash variables per session, and the turn's usage in the prompt response.
func TestEmbedHooks(t *testing.T) {
	e := setup(t, func(n int, msgs []any) reply {
		switch n {
		case 0:
			return reply{calls: [][2]string{{"db_query", `{"sql":"select 1"}`}, {"bash", `{"command":"echo chat=$ANNULO_CHAT_ID"}`}}}
		}
		return reply{text: "done"}
	})
	os.WriteFile(filepath.Join(e.cwd, "AGENTS.md"), []byte("PROJECT-RULES"), 0o644)
	var gotMeta map[string]any
	var mu sync.Mutex
	e.ag.opt.SystemPrompt = func(sid, cwd string, meta map[string]any) string {
		return fmt.Sprintf("You are Annulo's helper for chat %v.", meta["chat"])
	}
	e.ag.opt.NoInstructionFiles = true
	e.ag.opt.NoMemory = true
	e.ag.opt.BashEnv = func(sid string, meta map[string]any) []string {
		return []string{"ANNULO_CHAT_ID=" + fmt.Sprint(meta["chat"])}
	}
	e.ag.opt.Tools = []Tool{{
		Name: "db_query", Description: "Run SQL", ReadOnly: true,
		Parameters: map[string]any{"type": "object", "properties": map[string]any{"sql": map[string]any{"type": "string"}}},
		Run: func(ctx context.Context, c ToolCall) (ToolResult, error) {
			mu.Lock()
			gotMeta = c.Meta
			mu.Unlock()
			return ToolResult{Text: "rows: 1 for " + fmt.Sprint(c.Args["sql"])}, nil
		},
	}}
	res, err := e.c.Conn.NewSession(context.Background(), acp.NewSessionRequest{Cwd: e.cwd, McpServers: []acp.McpServer{}, Meta: map[string]any{"chat": "c42", "gerdr": map[string]any{"mode": modeBypass}}})
	if err != nil {
		t.Fatal(err)
	}
	pr, err := e.prompt(res.SessionId, uuid.NewString(), "go")
	if err != nil {
		t.Fatal(err)
	}
	sys := systemText(e.llm.request(0))
	if !strings.HasPrefix(sys, "You are Annulo's helper for chat c42.") || strings.Contains(sys, "PROJECT-RULES") ||
		strings.Contains(sys, "# Memory") || strings.Contains(sys, "inside gerdr") {
		t.Fatalf("system prompt:\n%s", sys)
	}
	tools, _ := json.Marshal(e.llm.request(0)["tools"])
	if !strings.Contains(string(tools), `"db_query"`) {
		t.Fatalf("caller tool not offered: %s", tools)
	}
	msgs := e.llm.request(1)["messages"].([]any)
	var outs []string
	for _, m := range msgs {
		if mm := m.(map[string]any); mm["role"] == "tool" {
			outs = append(outs, fmt.Sprint(mm["content"]))
		}
	}
	if len(outs) != 2 || outs[0] != "rows: 1 for select 1" || !strings.Contains(outs[1], "chat=c42") {
		t.Fatalf("tool results: %q", outs)
	}
	if gotMeta["chat"] != "c42" {
		t.Fatalf("tool meta: %v", gotMeta)
	}
	if pr.Usage == nil || pr.Usage.InputTokens != 200 || pr.Usage.OutputTokens != 20 {
		t.Fatalf("usage: %+v", pr.Usage)
	}
}

// A caller tool that waits for the user returns when the turn is cancelled.
func TestCallerToolCancel(t *testing.T) {
	e := setup(t, func(n int, msgs []any) reply {
		if n == 0 {
			return reply{calls: [][2]string{{"request_user_input", `{}`}}}
		}
		return reply{text: "ok"}
	})
	started := make(chan struct{})
	e.ag.opt.Tools = []Tool{{Name: "request_user_input", ReadOnly: true, Run: func(ctx context.Context, c ToolCall) (ToolResult, error) {
		close(started)
		<-ctx.Done()
		return ToolResult{}, ctx.Err()
	}}}
	sid := e.newSession(modeBypass)
	go func() { <-started; _ = e.c.Conn.Cancel(context.Background(), acp.CancelNotification{SessionId: sid}) }()
	res, err := e.prompt(sid, uuid.NewString(), "ask me")
	if err != nil || res.StopReason != acp.StopReasonCancelled {
		t.Fatalf("res %v err %v", res.StopReason, err)
	}
}

type memFiles struct {
	mu     sync.Mutex
	root   string
	data   map[string]string
	ver    map[string]int
	writes []string // versions writes were based on
}

func (f *memFiles) Owns(p string) bool { return strings.HasPrefix(p, f.root+"/") }
func (f *memFiles) Read(ctx context.Context, p string) ([]byte, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.data[p]
	if !ok {
		return nil, "", os.ErrNotExist
	}
	return []byte(d), fmt.Sprint(f.ver[p]), nil
}
func (f *memFiles) Write(ctx context.Context, p string, b []byte, v string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.data[p]; ok && v != fmt.Sprint(f.ver[p]) {
		return fmt.Errorf("conflict: %s changed since it was read", p)
	}
	f.writes = append(f.writes, v)
	f.data[p] = string(b)
	f.ver[p]++
	return nil
}

func TestFilesRedirect(t *testing.T) {
	e := setup(t, func(n int, msgs []any) reply {
		switch n {
		case 0:
			return reply{calls: [][2]string{{"read", `{"path":"/store/a.txt"}`}}}
		case 1:
			return reply{calls: [][2]string{{"edit", `{"path":"/store/a.txt","old_string":"hello","new_string":"bye"}`}}}
		}
		return reply{text: "done"}
	})
	fs := &memFiles{root: "/store", data: map[string]string{"/store/a.txt": "hello world"}, ver: map[string]int{"/store/a.txt": 7}}
	e.ag.opt.Files = fs
	sid := e.newSession(modeBypass)
	if _, err := e.prompt(sid, uuid.NewString(), "go"); err != nil {
		t.Fatal(err)
	}
	if fs.data["/store/a.txt"] != "bye world" || len(fs.writes) != 1 || fs.writes[0] != "7" {
		t.Fatalf("data %q writes %v", fs.data["/store/a.txt"], fs.writes)
	}
}

func TestLimitImage(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 3000, 1000))
	for y := 0; y < 1000; y++ {
		for x := 0; x < 3000; x++ {
			img.SetNRGBA(x, y, color.NRGBA{uint8(x * 7), uint8(y * 13), uint8(x ^ y), 255})
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	mime, out := limitImage("image/png", buf.Bytes(), ImageLimits{MaxEdge: 1568, MaxBytes: 768 << 10})
	cfg, _, err := image.DecodeConfig(bytes.NewReader(out))
	if err != nil || cfg.Width != 1568 || cfg.Height != 522 || len(out) > 768<<10 {
		t.Fatalf("%s %dx%d %d bytes err %v", mime, cfg.Width, cfg.Height, len(out), err)
	}
	if m, same := limitImage("image/png", buf.Bytes(), ImageLimits{}); m != "image/png" || !bytes.Equal(same, buf.Bytes()) {
		t.Fatal("no limits must pass the image through")
	}
}

func TestEffortOff(t *testing.T) {
	cases := []struct {
		model Model
		want  string
	}{
		{Model{ID: "claude-sonnet-5-5", API: "anthropic"}, "off"},
		{Model{ID: "claude-opus-5-5", API: "anthropic"}, "off"},
		{Model{ID: "deepseek-v4-pro"}, "off"},
		{Model{ID: "gpt-6.1-sol"}, "low"},
		{Model{ID: "gpt-6.1-sol", Efforts: []string{"none", "low"}}, "none"},
	}
	for _, c := range cases {
		m := resolved{Model: withDefaults(c.model)}
		if c.model.API == "anthropic" {
			m.Model = anthropicDefaults(c.model, c.model.ID)
		}
		if got := effortFor(m, "off"); got != c.want {
			t.Errorf("%s: off → %q, want %q", c.model.ID, got, c.want)
		}
		if got := effortFor(m, "bogus"); got != m.DefaultEffort {
			t.Errorf("%s: unknown effort → %q, want default %q", c.model.ID, got, m.DefaultEffort)
		}
	}
}
