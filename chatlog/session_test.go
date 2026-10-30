package chatlog_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/acp-go-sdk"

	"github.com/gerdr-dev/gerdr/acpx"
	"github.com/gerdr-dev/gerdr/agents/native"
	"github.com/gerdr-dev/gerdr/chatlog"
)

// client routes the agent's callbacks into the Session, as an embedder would.
type client struct{ s *chatlog.Session }

func (c *client) SessionUpdate(ctx context.Context, n acp.SessionNotification) error {
	c.s.Update(n.Update)
	return nil
}
func (c *client) RequestPermission(ctx context.Context, p acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	return c.s.RequestPermission(ctx, p)
}
func (c *client) HandleExtensionMethod(ctx context.Context, method string, params json.RawMessage) (any, error) {
	if method == acpx.MethodAsk {
		var req acpx.AskRequest
		_ = json.Unmarshal(params, &req)
		return c.s.Ask(ctx, req)
	}
	return nil, acp.NewMethodNotFound(method)
}
func (c *client) ReadTextFile(context.Context, acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	return acp.ReadTextFileResponse{}, acp.NewMethodNotFound("")
}
func (c *client) WriteTextFile(context.Context, acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	return acp.WriteTextFileResponse{}, acp.NewMethodNotFound("")
}
func (c *client) CreateTerminal(context.Context, acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	return acp.CreateTerminalResponse{}, acp.NewMethodNotFound("")
}
func (c *client) KillTerminal(context.Context, acp.KillTerminalRequest) (acp.KillTerminalResponse, error) {
	return acp.KillTerminalResponse{}, acp.NewMethodNotFound("")
}
func (c *client) TerminalOutput(context.Context, acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	return acp.TerminalOutputResponse{}, acp.NewMethodNotFound("")
}
func (c *client) ReleaseTerminal(context.Context, acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return acp.ReleaseTerminalResponse{}, acp.NewMethodNotFound("")
}
func (c *client) WaitForTerminalExit(context.Context, acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	return acp.WaitForTerminalExitResponse{}, acp.NewMethodNotFound("")
}
func (c *client) UnstableCompleteElicitation(context.Context, acp.UnstableCompleteElicitationNotification) error {
	return nil
}
func (c *client) UnstableCreateElicitation(context.Context, acp.UnstableCreateElicitationRequest) (acp.UnstableCreateElicitationResponse, error) {
	return acp.UnstableCreateElicitationResponse{}, acp.NewMethodNotFound("")
}
func (c *client) UnstableConnectMcp(context.Context, acp.UnstableConnectMcpRequest) (acp.UnstableConnectMcpResponse, error) {
	return acp.UnstableConnectMcpResponse{}, acp.NewMethodNotFound("")
}
func (c *client) UnstableDisconnectMcp(context.Context, acp.UnstableDisconnectMcpRequest) (acp.UnstableDisconnectMcpResponse, error) {
	return acp.UnstableDisconnectMcpResponse{}, acp.NewMethodNotFound("")
}

// fakeModel answers each Chat Completions request with script(n): text, and optionally one tool call.
func fakeModel(t *testing.T, script func(n int) (text, tool, args string, delay time.Duration)) string {
	var mu sync.Mutex
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		i := n
		n++
		mu.Unlock()
		text, tool, args, delay := script(i)
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		send := func(v any) {
			b, _ := json.Marshal(v)
			fmt.Fprintf(w, "data: %s\n\n", b)
			fl.Flush()
		}
		for _, word := range strings.SplitAfter(text, " ") {
			if delay > 0 {
				select {
				case <-time.After(delay):
				case <-r.Context().Done():
					return
				}
			}
			send(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": word}}}})
		}
		finish := "stop"
		if tool != "" {
			finish = "tool_calls"
			send(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": fmt.Sprintf("call_%d", i), "function": map[string]any{"name": tool, "arguments": args}}}}}}})
		}
		send(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{}, "finish_reason": finish}}})
		fmt.Fprint(w, "data: [DONE]\n\n")
		fl.Flush()
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

type env struct {
	s    *chatlog.Session
	conn *acp.ClientSideConnection
	cwd  string
	mu   sync.Mutex
	got  []*chatlog.Event
}

func start(t *testing.T, url string) *env {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	cfg := native.Config{Providers: []native.Provider{{ID: "f", BaseURL: url, APIKey: "k", Models: []native.Model{{ID: "m", API: "chat"}}}}}
	ag := native.New(slog.New(slog.DiscardHandler), native.Options{Dir: t.TempDir(), Config: func() native.Config { return cfg }, NoMemory: true, NoInstructionFiles: true})
	t.Cleanup(ag.Close)
	toAgentR, toAgentW := io.Pipe()
	toClientR, toClientW := io.Pipe()
	ag.SetConn(acp.NewAgentSideConnection(ag, toClientW, toAgentR))
	e := &env{cwd: t.TempDir()}
	c := &client{}
	e.conn = acp.NewClientSideConnection(c, toAgentW, toClientR)
	ctx := context.Background()
	if _, err := e.conn.Initialize(ctx, acp.InitializeRequest{ProtocolVersion: acp.ProtocolVersionNumber}); err != nil {
		t.Fatal(err)
	}
	s, err := chatlog.New(e.conn, "", chatlog.Options{
		File: filepath.Join(t.TempDir(), "log.jsonl"), Steering: true, SteerRewind: true,
		OnEvents: func(evs []*chatlog.Event, replace bool) {
			e.mu.Lock()
			if replace {
				e.got = nil
			}
			e.got = append(e.got, evs...)
			e.mu.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	c.s, e.s = s, s
	ns, err := e.conn.NewSession(ctx, acp.NewSessionRequest{Cwd: e.cwd, McpServers: []acp.McpServer{}})
	if err != nil {
		t.Fatal(err)
	}
	s.SetSessionID(string(ns.SessionId))
	s.SetOptions(ns.Modes, ns.ConfigOptions)
	if len(s.State().Commands) == 0 {
		t.Fatal("commands sent during session/new were lost")
	}
	return e
}

func (e *env) waitIdle(t *testing.T) {
	t.Helper()
	for i := 0; i < 300; i++ {
		if e.s.State().Status == chatlog.StatusIdle {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("still %s", e.s.State().Status)
}

func kinds(evs []*chatlog.Event) string {
	var k []string
	for _, e := range evs {
		k = append(k, e.Kind)
	}
	return strings.Join(k, ",")
}

func TestSessionTurnPermissionRewind(t *testing.T) {
	url := fakeModel(t, func(n int) (string, string, string, time.Duration) {
		if n == 0 {
			return "Writing it. ", "write", `{"path":"a.txt","content":"x"}`, 0
		}
		return "done", "", "", 0
	})
	e := start(t, url)
	ctx := context.Background()
	res, err := e.s.Send(ctx, []acp.ContentBlock{acp.TextBlock("make a.txt")}, "", false)
	if err != nil {
		t.Fatal(err)
	}
	// default mode: the write asks
	var p []chatlog.Pending
	for i := 0; i < 200 && len(p) == 0; i++ {
		time.Sleep(20 * time.Millisecond)
		p = e.s.Pending()
	}
	if len(p) != 1 || p[0].Kind != "permission" || e.s.State().Status != chatlog.StatusWaiting {
		t.Fatalf("pending %v status %s", p, e.s.State().Status)
	}
	if err := e.s.Answer(p[0].ReqID, json.RawMessage(`{"optionId":"allow_once"}`)); err != nil {
		t.Fatal(err)
	}
	e.waitIdle(t)
	if b, _ := os.ReadFile(filepath.Join(e.cwd, "a.txt")); string(b) != "x" {
		t.Fatalf("file %q", b)
	}
	k := kinds(e.s.Events())
	for _, want := range []string{"user,turn_start", "permission", "permission_done", "turn_end"} {
		if !strings.Contains(k, want) {
			t.Fatalf("events %s missing %s", k, want)
		}
	}
	// what OnEvents delivered folds into the same log
	e.mu.Lock()
	live := len(e.got)
	e.mu.Unlock()
	if live < len(e.s.Events()) {
		t.Fatalf("live %d < log %d", live, len(e.s.Events()))
	}
	content, err := e.s.Rewind(ctx, res.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	if len(content) != 1 || !strings.Contains(string(content[0]), "make a.txt") || len(e.s.Events()) != 0 {
		t.Fatalf("content %s events %s", content, kinds(e.s.Events()))
	}
}

func TestSessionSteerAndStop(t *testing.T) {
	url := fakeModel(t, func(n int) (string, string, string, time.Duration) {
		return "one two three four five six seven eight nine ten", "", "", 80 * time.Millisecond
	})
	e := start(t, url)
	ctx := context.Background()
	if _, err := e.s.Send(ctx, []acp.ContentBlock{acp.TextBlock("count")}, "", false); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	r, err := e.s.Send(ctx, []acp.ContentBlock{acp.TextBlock("and more")}, "", false)
	if err != nil || !r.Steered {
		t.Fatalf("steer %+v %v", r, err)
	}
	stop, err := e.s.Stop(ctx)
	if err != nil {
		t.Fatal(err)
	}
	e.waitIdle(t)
	evs := e.s.Events()
	last := evs[len(evs)-1]
	if last.Kind != chatlog.EvTurnEnd || last.StopReason != "cancelled" {
		t.Fatalf("last %+v (%s), stop %+v", last, kinds(evs), stop)
	}
	// the interjection was either withdrawn (back to the composer) or already read
	steered := false
	for _, ev := range evs {
		steered = steered || (ev.Kind == chatlog.EvUser && ev.Steer)
	}
	if steered == (stop.Withdrawn == 1) {
		t.Fatalf("withdrawn %d but steer in log %v", stop.Withdrawn, steered)
	}
}
