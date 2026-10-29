package native

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
	"github.com/google/uuid"

	"github.com/gerdr-dev/gerdr/internal/acpx"
	"github.com/gerdr-dev/gerdr/internal/agenthost"
)

// 假的 Chat Completions：每次请求交给 script，它看请求里的消息决定回什么。
type fakeLLM struct {
	t      *testing.T
	mu     sync.Mutex
	reqs   []map[string]any
	script func(n int, msgs []any) reply
}

// reply：一次回复。text 正文；calls 工具调用（name → args JSON）；delay 每片之间等多久（测打断）。
type reply struct {
	text  string
	calls [][2]string
	delay time.Duration
}

func (f *fakeLLM) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	b, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(b, &body)
	f.mu.Lock()
	f.reqs = append(f.reqs, body)
	n := len(f.reqs) - 1
	f.mu.Unlock()
	msgs, _ := body["messages"].([]any)
	rep := f.script(n, msgs)
	w.Header().Set("Content-Type", "text/event-stream")
	fl := w.(http.Flusher)
	chunk := func(v any) {
		bb, _ := json.Marshal(v)
		fmt.Fprintf(w, "data: %s\n\n", bb)
		fl.Flush()
	}
	for _, word := range strings.SplitAfter(rep.text, " ") {
		if word == "" {
			continue
		}
		if rep.delay > 0 {
			select {
			case <-time.After(rep.delay):
			case <-r.Context().Done():
				return
			}
		}
		chunk(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": word}}}})
	}
	for i, c := range rep.calls {
		chunk(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{
			"index": i, "id": fmt.Sprintf("call_%d_%d", n, i), "function": map[string]any{"name": c[0], "arguments": c[1]},
		}}}}}})
	}
	finish := "stop"
	if len(rep.calls) > 0 {
		finish = "tool_calls"
	}
	chunk(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{}, "finish_reason": finish}}})
	chunk(map[string]any{"choices": []any{}, "usage": map[string]any{"prompt_tokens": 100, "completion_tokens": 10}})
	fmt.Fprint(w, "data: [DONE]\n\n")
	fl.Flush()
}

func (f *fakeLLM) request(i int) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i < 0 {
		i += len(f.reqs)
	}
	return f.reqs[i]
}

func (f *fakeLLM) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.reqs)
}

// lastUser：消息列表里最后一条 user 的文字。
func lastUser(msgs []any) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i].(map[string]any)
		if m["role"] == "user" {
			if s, ok := m["content"].(string); ok {
				return s
			}
			b, _ := json.Marshal(m["content"])
			return string(b)
		}
	}
	return ""
}

func lastRole(msgs []any) string {
	if len(msgs) == 0 {
		return ""
	}
	return msgs[len(msgs)-1].(map[string]any)["role"].(string)
}

type handler struct {
	mu      sync.Mutex
	updates []acp.SessionNotification
	perm    func(acp.RequestPermissionRequest) string
	asks    int
}

func (h *handler) OnUpdate(n acp.SessionNotification) {
	h.mu.Lock()
	h.updates = append(h.updates, n)
	h.mu.Unlock()
}

func (h *handler) OnPermission(ctx context.Context, req acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	opt := "allow_once"
	if h.perm != nil {
		opt = h.perm(req)
	}
	return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{Selected: &acp.RequestPermissionOutcomeSelected{OptionId: acp.PermissionOptionId(opt)}}}, nil
}

func (h *handler) OnAsk(ctx context.Context, req acpx.AskRequest) (acpx.AskResponse, error) {
	h.mu.Lock()
	h.asks++
	h.mu.Unlock()
	return acpx.AskResponse{Answers: map[string][]string{"0": {req.Questions[0].Options[0].Label}}}, nil
}

// toolUpdates：某个 tool call 的所有更新（开始 + 后续），按到达顺序。
func (h *handler) toolStatus(id string) acp.ToolCallStatus {
	h.mu.Lock()
	defer h.mu.Unlock()
	var st acp.ToolCallStatus
	for _, n := range h.updates {
		if tc := n.Update.ToolCall; tc != nil && string(tc.ToolCallId) == id {
			st = tc.Status
		}
		if tu := n.Update.ToolCallUpdate; tu != nil && string(tu.ToolCallId) == id && tu.Status != nil {
			st = *tu.Status
		}
	}
	return st
}

type env struct {
	t   *testing.T
	llm *fakeLLM
	h   *handler
	ag  *Agent
	c   *agenthost.Host
	cwd string
}

func setup(t *testing.T, script func(n int, msgs []any) reply) *env {
	t.Helper()
	// 别读到真的 ~/.claude.json、~/.claude/skills：会把本机配的 MCP 服务器真起起来
	t.Setenv("HOME", t.TempDir())
	f := &fakeLLM{t: t, script: script}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	cwd := t.TempDir()
	cfg := Config{Providers: []Provider{{ID: "fake", BaseURL: srv.URL, APIKey: "k", Models: []Model{{ID: "fake-model", API: "chat"}}}}}
	ag := New(slog.New(slog.NewTextHandler(io.Discard, nil)), Options{Dir: dir, Config: func() Config { return cfg }})
	h := &handler{}
	c, err := agenthost.StartInProc(context.Background(), ag, h, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return &env{t: t, llm: f, h: h, ag: ag, c: c, cwd: cwd}
}

func (e *env) newSession(mode string) acp.SessionId {
	e.t.Helper()
	res, err := e.c.Conn.NewSession(context.Background(), acp.NewSessionRequest{Cwd: e.cwd, McpServers: []acp.McpServer{}, Meta: acpx.Meta(map[string]any{"mode": mode})})
	if err != nil {
		e.t.Fatal(err)
	}
	return res.SessionId
}

func (e *env) prompt(sid acp.SessionId, id, text string) (acp.PromptResponse, error) {
	return e.c.Conn.Prompt(context.Background(), acp.PromptRequest{SessionId: sid, MessageId: acp.Ptr(id), Prompt: []acp.ContentBlock{acp.TextBlock(text)}})
}

func TestToolLoop(t *testing.T) {
	e := setup(t, func(n int, msgs []any) reply {
		if lastRole(msgs) == "user" {
			return reply{text: "Let me check. ", calls: [][2]string{{"bash", `{"command":"echo hello-from-bash"}`}, {"write", `{"path":"out.txt","content":"x"}`}}}
		}
		return reply{text: "done"}
	})
	sid := e.newSession(modeBypass)
	res, err := e.prompt(sid, uuid.NewString(), "go")
	if err != nil {
		t.Fatal(err)
	}
	if res.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("stop %v", res.StopReason)
	}
	if e.llm.count() != 2 {
		t.Fatalf("want 2 model calls, got %d", e.llm.count())
	}
	msgs := e.llm.request(1)["messages"].([]any)
	// system, user, assistant(tool_calls), tool, tool
	if len(msgs) != 5 || lastRole(msgs) != "tool" {
		t.Fatalf("unexpected history: %v", msgs)
	}
	if out := msgs[3].(map[string]any)["content"].(string); !strings.Contains(out, "hello-from-bash") {
		t.Fatalf("bash output missing: %q", out)
	}
	if b, _ := os.ReadFile(filepath.Join(e.cwd, "out.txt")); string(b) != "x" {
		t.Fatalf("file not written: %q", b)
	}
	if st := e.h.toolStatus("call_0_0"); st != acp.ToolCallStatusCompleted {
		t.Fatalf("tool status %v", st)
	}
}

func TestPermissionDenied(t *testing.T) {
	e := setup(t, func(n int, msgs []any) reply {
		if lastRole(msgs) == "user" {
			return reply{calls: [][2]string{{"write", `{"path":"nope.txt","content":"x"}`}, {"bash", `{"command":"ls"}`}}}
		}
		return reply{text: "ok"}
	})
	var asked []string
	e.h.perm = func(r acp.RequestPermissionRequest) string {
		asked = append(asked, *r.ToolCall.Title)
		return "reject_once"
	}
	sid := e.newSession(modeDefault)
	if _, err := e.prompt(sid, uuid.NewString(), "go"); err != nil {
		t.Fatal(err)
	}
	if len(asked) != 1 || !strings.Contains(asked[0], "nope.txt") {
		t.Fatalf("want one permission prompt for the write (ls is read-only), got %v", asked)
	}
	if _, err := os.Stat(filepath.Join(e.cwd, "nope.txt")); err == nil {
		t.Fatal("file written despite rejection")
	}
	msgs := e.llm.request(1)["messages"].([]any)
	if c := msgs[3].(map[string]any)["content"].(string); !strings.Contains(c, "declined") {
		t.Fatalf("model not told about the rejection: %q", c)
	}
}

func TestSteerDuringTool(t *testing.T) {
	e := setup(t, func(n int, msgs []any) reply {
		switch {
		case n == 0:
			return reply{calls: [][2]string{{"bash", `{"command":"sleep 1"}`}}}
		case strings.Contains(lastUser(msgs), "also this") && lastRole(msgs) == "user":
			return reply{text: "got the steer"}
		}
		return reply{text: "first answer"}
	})
	sid := e.newSession(modeBypass)
	done := make(chan acp.PromptResponse)
	go func() {
		res, err := e.prompt(sid, uuid.NewString(), "start")
		if err != nil {
			t.Error(err)
		}
		done <- res
	}()
	time.Sleep(300 * time.Millisecond) // 在 sleep 1 里面
	sr, err := agenthost.Call[acpx.SteerResponse](context.Background(), e.c, acpx.MethodSteer, acpx.SteerRequest{SessionId: sid, MessageId: uuid.NewString(), Prompt: []acp.ContentBlock{acp.TextBlock("also this")}})
	if err != nil || sr.Outcome != acpx.SteerInjected {
		t.Fatalf("steer: %v %v", sr, err)
	}
	<-done
	// 第二次请求：工具结果之后紧跟插的话（同一次请求带上），模型回 got the steer
	msgs := e.llm.request(1)["messages"].([]any)
	if lastRole(msgs) != "user" || !strings.Contains(lastUser(msgs), "also this") {
		t.Fatalf("steer not appended after tool result: %v", msgs)
	}
	if e.llm.count() != 2 {
		t.Fatalf("want 2 calls, got %d", e.llm.count())
	}
	// 没在跑的时候插话 → promptRequired
	sr, _ = agenthost.Call[acpx.SteerResponse](context.Background(), e.c, acpx.MethodSteer, acpx.SteerRequest{SessionId: sid, Prompt: []acp.ContentBlock{acp.TextBlock("x")}})
	if sr.Outcome != acpx.SteerPromptRequired {
		t.Fatalf("idle steer: %v", sr.Outcome)
	}
}

func TestSteerNowInterruptsGeneration(t *testing.T) {
	e := setup(t, func(n int, msgs []any) reply {
		if n == 0 {
			return reply{text: strings.Repeat("blah ", 50), delay: 40 * time.Millisecond}
		}
		return reply{text: "answering the steer"}
	})
	sid := e.newSession(modeBypass)
	done := make(chan struct{})
	go func() { e.prompt(sid, uuid.NewString(), "talk"); close(done) }()
	time.Sleep(300 * time.Millisecond)
	agenthost.Call[acpx.SteerResponse](context.Background(), e.c, acpx.MethodSteer, acpx.SteerRequest{SessionId: sid, MessageId: uuid.NewString(), Now: true, Prompt: []acp.ContentBlock{acp.TextBlock("stop and do X")}})
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("turn did not finish")
	}
	msgs := e.llm.request(1)["messages"].([]any)
	// 已经说出来的半截留着（assistant），后面是插的话
	if len(msgs) != 4 || msgs[2].(map[string]any)["role"] != "assistant" || !strings.Contains(lastUser(msgs), "stop and do X") {
		t.Fatalf("unexpected: %v", msgs)
	}
}

func TestCancelKeepsHistoryValid(t *testing.T) {
	e := setup(t, func(n int, msgs []any) reply {
		if n == 0 {
			return reply{calls: [][2]string{{"bash", `{"command":"sleep 30"}`}}}
		}
		return reply{text: "fine"}
	})
	sid := e.newSession(modeBypass)
	done := make(chan acp.PromptResponse)
	go func() {
		res, _ := e.prompt(sid, uuid.NewString(), "long")
		done <- res
	}()
	time.Sleep(500 * time.Millisecond)
	start := time.Now()
	_ = e.c.Conn.Cancel(context.Background(), acp.CancelNotification{SessionId: sid})
	res := <-done
	if res.StopReason != acp.StopReasonCancelled {
		t.Fatalf("stop %v", res.StopReason)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("cancel did not kill the command")
	}
	if _, err := e.prompt(sid, uuid.NewString(), "next"); err != nil {
		t.Fatal(err)
	}
	msgs := e.llm.request(-1)["messages"].([]any)
	// system, user, assistant(tool_call), tool(interrupted), user(next)
	if len(msgs) != 5 || msgs[3].(map[string]any)["role"] != "tool" || !strings.Contains(msgs[3].(map[string]any)["content"].(string), "nterrupted") {
		t.Fatalf("history after cancel: %v", msgs)
	}
}

func TestRewindAndResume(t *testing.T) {
	e := setup(t, func(n int, msgs []any) reply { return reply{text: fmt.Sprintf("answer %d", n)} })
	sid := e.newSession(modeBypass)
	m1, m2 := uuid.NewString(), uuid.NewString()
	e.prompt(sid, m1, "one")
	e.prompt(sid, m2, "two")
	if _, err := agenthost.Call[acpx.RewindResponse](context.Background(), e.c, acpx.MethodRewind, acpx.RewindRequest{SessionId: sid, MessageId: m2}); err != nil {
		t.Fatal(err)
	}
	// 新的 agent 实例（模拟 gerdr 重启）接回会话
	ag2 := New(slog.New(slog.NewTextHandler(io.Discard, nil)), Options{Dir: e.ag.st.dir, Config: e.ag.cfg})
	h2 := &handler{}
	c2, err := agenthost.StartInProc(context.Background(), ag2, h2, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	if _, err := c2.Conn.ResumeSession(context.Background(), acp.ResumeSessionRequest{SessionId: sid, Cwd: e.cwd}); err != nil {
		t.Fatal(err)
	}
	if _, err := c2.Conn.Prompt(context.Background(), acp.PromptRequest{SessionId: sid, MessageId: acp.Ptr(uuid.NewString()), Prompt: []acp.ContentBlock{acp.TextBlock("three")}}); err != nil {
		t.Fatal(err)
	}
	msgs := e.llm.request(-1)["messages"].([]any)
	var users []string
	for _, m := range msgs {
		if m.(map[string]any)["role"] == "user" {
			users = append(users, m.(map[string]any)["content"].(string))
		}
	}
	if strings.Join(users, ",") != "one,three" {
		t.Fatalf("after rewind + resume want one,three got %v", users)
	}
	// ListSessions 看得到
	ls, _ := c2.Conn.ListSessions(context.Background(), acp.ListSessionsRequest{})
	if len(ls.Sessions) != 1 || *ls.Sessions[0].Title != "one" {
		t.Fatalf("list: %+v", ls.Sessions)
	}
}

func TestSubagent(t *testing.T) {
	e := setup(t, func(n int, msgs []any) reply {
		sys, _ := msgs[0].(map[string]any)["content"].(string)
		if strings.Contains(sys, "You are a sub-agent") {
			if lastRole(msgs) == "user" {
				return reply{calls: [][2]string{{"grep", `{"pattern":"needle"}`}}}
			}
			return reply{text: "found it in a.txt"}
		}
		if lastRole(msgs) == "user" {
			return reply{calls: [][2]string{{"task", `{"description":"find needle","prompt":"find the needle"}`}, {"task", `{"description":"again","prompt":"find the needle too"}`}}}
		}
		return reply{text: "sub-agents reported"}
	})
	os.WriteFile(filepath.Join(e.cwd, "a.txt"), []byte("needle"), 0o644)
	sid := e.newSession(modeDefault)
	if _, err := e.prompt(sid, uuid.NewString(), "search"); err != nil {
		t.Fatal(err)
	}
	msgs := e.llm.request(-1)["messages"].([]any)
	tr := msgs[3].(map[string]any)
	if tr["role"] != "tool" || tr["content"] != "found it in a.txt" {
		t.Fatalf("sub-agent report not returned: %v", tr)
	}
	// 子 agent 的工具调用挂在主会话的卡片下面
	e.h.mu.Lock()
	defer e.h.mu.Unlock()
	nested := 0
	for _, n := range e.h.updates {
		if tc := n.Update.ToolCall; tc != nil {
			if p, ok := acpx.GetMeta(tc.Meta, "parentToolCallId"); ok && strings.HasPrefix(p.(string), "call_0_") {
				nested++
			}
		}
	}
	if nested != 2 {
		t.Fatalf("want 2 nested tool calls, got %d", nested)
	}
}

func TestClear(t *testing.T) {
	e := setup(t, func(n int, msgs []any) reply { return reply{text: "hi"} })
	sid := e.newSession(modeBypass)
	e.prompt(sid, uuid.NewString(), "remember 42")
	e.prompt(sid, uuid.NewString(), "/clear")
	var nid string
	e.h.mu.Lock()
	for _, n := range e.h.updates {
		if u := n.Update.SessionInfoUpdate; u != nil {
			if v, ok := acpx.GetMeta(u.Meta, "sessionId"); ok {
				nid = v.(string)
			}
		}
	}
	e.h.mu.Unlock()
	if nid == "" || nid == string(sid) {
		t.Fatalf("clear did not announce a new id: %q", nid)
	}
	e.prompt(acp.SessionId(nid), uuid.NewString(), "what?")
	msgs := e.llm.request(-1)["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("context not cleared: %v", msgs)
	}
}

func TestReadOnlyCommand(t *testing.T) {
	for cmd, want := range map[string]bool{
		"ls -la":                      true,
		"git status && git diff":      true,
		"rg foo | head -20":           true,
		"cat a > b":                   false,
		"rm -rf x":                    false,
		"find . -name '*.go' -delete": false,
		"git push":                    false,
		"echo $(rm x)":                false,
		"go test ./...":               false,
		"git branch -D main":          false,
		"sed -n 1,10p file.go":        true,
		"sed -i '' s/a/b/ f":          false,
	} {
		if got := readOnlyCommand(cmd); got != want {
			t.Errorf("readOnlyCommand(%q) = %v, want %v", cmd, got, want)
		}
	}
}

// autoTurns：会话报过的 autoTurn（true / false）序列。
func (h *handler) autoTurns() []bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []bool
	for _, n := range h.updates {
		if u := n.Update.SessionInfoUpdate; u != nil {
			if v, ok := acpx.GetMeta(u.Meta, acpx.AutoTurnKey); ok {
				out = append(out, v.(bool))
			}
		}
	}
	return out
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestBackgroundBashAutoTurn(t *testing.T) {
	e := setup(t, func(n int, msgs []any) reply {
		switch {
		case n == 0:
			return reply{calls: [][2]string{{"bash", `{"command":"sleep 0.5; echo bg-finished","run_in_background":true}`}}}
		case strings.Contains(lastUser(msgs), "background-task-notification"):
			return reply{text: "the background job is done"}
		}
		return reply{text: "started it"}
	})
	sid := e.newSession(modeBypass)
	if _, err := e.prompt(sid, uuid.NewString(), "run it in bg"); err != nil {
		t.Fatal(err)
	}
	if e.llm.count() != 2 {
		t.Fatalf("turn should end without waiting for the command, calls=%d", e.llm.count())
	}
	// 命令跑完：自己开一轮，通知里带着输出
	waitFor(t, "auto turn", func() bool { return len(e.h.autoTurns()) == 2 })
	if got := e.h.autoTurns(); !got[0] || got[1] {
		t.Fatalf("autoTurn sequence %v", got)
	}
	if u := lastUser(e.llm.request(-1)["messages"].([]any)); !strings.Contains(u, "bg-finished") {
		t.Fatalf("notification lacks output: %q", u)
	}
}

func TestDetachForegroundBash(t *testing.T) {
	e := setup(t, func(n int, msgs []any) reply {
		switch {
		case n == 0:
			return reply{calls: [][2]string{{"bash", `{"command":"sleep 1; echo late"}`}}}
		case strings.Contains(lastUser(msgs), "background-task-notification"):
			return reply{text: "noted"}
		}
		return reply{text: "moved on"}
	})
	sid := e.newSession(modeBypass)
	done := make(chan struct{})
	go func() { e.prompt(sid, uuid.NewString(), "go"); close(done) }()
	time.Sleep(300 * time.Millisecond)
	r, err := agenthost.Call[acpx.TaskBackgroundResponse](context.Background(), e.c, acpx.MethodTaskBackground, acpx.TaskBackgroundRequest{SessionId: sid, ToolCallId: "call_0_0"})
	if err != nil || !r.Backgrounded {
		t.Fatalf("background: %v %v", r, err)
	}
	select {
	case <-done:
	case <-time.After(700 * time.Millisecond):
		t.Fatal("turn still waiting for the detached command")
	}
	waitFor(t, "notification", func() bool { return strings.Contains(lastUser(e.llm.request(-1)["messages"].([]any)), "late") })
}

func TestBackgroundSubagent(t *testing.T) {
	e := setup(t, func(n int, msgs []any) reply {
		sys, _ := msgs[0].(map[string]any)["content"].(string)
		if strings.Contains(sys, "You are a sub-agent") {
			return reply{text: "sub report 123", delay: 100 * time.Millisecond}
		}
		switch {
		case n == 0:
			return reply{calls: [][2]string{{"task", `{"description":"bg job","prompt":"do it","run_in_background":true}`}}}
		case strings.Contains(lastUser(msgs), "background-task-notification"):
			return reply{text: "got the report"}
		}
		return reply{text: "carrying on"}
	})
	sid := e.newSession(modeBypass)
	if _, err := e.prompt(sid, uuid.NewString(), "go"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "report delivered", func() bool {
		return strings.Contains(lastUser(e.llm.request(-1)["messages"].([]any)), "sub report 123")
	})
}

// 一个 agent 只用某家服务商：模型列表只剩那家的，默认模型不在里面就退回那家的第一个。
func TestConfigOnly(t *testing.T) {
	c := Config{Providers: []Provider{
		{ID: "a", BaseURL: "http://a", Models: []Model{{ID: "m1"}}},
		{ID: "b", BaseURL: "http://b", Models: []Model{{ID: "m2"}, {ID: "m3"}}},
	}, DefaultModel: "a/m1"}
	if n := len(c.Only(nil).models()); n != 3 {
		t.Fatalf("不限：%d", n)
	}
	o := c.Only([]string{"b"})
	if ms := o.models(); len(ms) != 2 || ms[0].Key != "b/m2" {
		t.Fatalf("只留 b：%v", ms)
	}
	if r, _ := o.find(""); r.Key != "b/m2" {
		t.Fatalf("默认模型不在 b 里应当退回 b 的第一个：%s", r.Key)
	}
	if len(c.Providers) != 2 {
		t.Fatal("Only 改了原来的配置")
	}
}
