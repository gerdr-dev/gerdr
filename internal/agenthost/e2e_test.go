package agenthost_test

// 对着真的 claude / codex 跑的端到端测试。花钱、要登录，所以默认跳过：
//
//	GERDR_E2E=codex go test ./internal/agenthost/ -run E2E -v
//	GERDR_E2E=claude go test ./internal/agenthost/ -run E2E -v

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/acp-go-sdk"
	"github.com/google/uuid"

	"github.com/gerdr-dev/gerdr/internal/acpx"
	"github.com/gerdr-dev/gerdr/internal/agenthost"
	"github.com/gerdr-dev/gerdr/internal/agents/claude"
	"github.com/gerdr-dev/gerdr/internal/agents/codex"
	"github.com/gerdr-dev/gerdr/internal/ext"
)

type rec struct {
	mu      sync.Mutex
	t       *testing.T
	text    strings.Builder
	tools   []string
	perms   int
	asks    int
	allowed bool
	cmds    []string
	tasks   []acpx.Task
	autos   []bool
	subText map[string]string // subagent 标记 → 它说的话
	cleared string            // /clear 之后 agent 报的新会话 id（sessionId + cleared）
}

func (r *rec) OnUpdate(n acp.SessionNotification) {
	r.mu.Lock()
	defer r.mu.Unlock()
	u := n.Update
	switch {
	case u.AgentMessageChunk != nil && u.AgentMessageChunk.Content.Text != nil && u.AgentMessageChunk.Meta == nil:
		r.text.WriteString(u.AgentMessageChunk.Content.Text.Text)
	case u.SessionInfoUpdate != nil:
		if g, ok := u.SessionInfoUpdate.Meta["gerdr"].(map[string]any); ok {
			if v, ok := g["tasks"]; ok {
				b, _ := json.Marshal(v)
				r.tasks = nil
				_ = json.Unmarshal(b, &r.tasks)
			}
			if v, ok := g[acpx.AutoTurnKey].(bool); ok {
				r.autos = append(r.autos, v)
				r.t.Logf("autoTurn %v", v)
			}
			if id, _ := g["sessionId"].(string); id != "" {
				if c, _ := g["cleared"].(bool); c {
					r.cleared = id
				}
			}
		}
	case u.AgentMessageChunk != nil && u.AgentMessageChunk.Meta != nil:
		if g, ok := u.AgentMessageChunk.Meta["gerdr"].(map[string]any); ok {
			if k, ok := g["subagent"].(string); ok && u.AgentMessageChunk.Content.Text != nil {
				if r.subText == nil {
					r.subText = map[string]string{}
				}
				r.subText[k] += u.AgentMessageChunk.Content.Text.Text
				return
			}
		}
		if u.AgentMessageChunk.Content.Text != nil {
			r.text.WriteString(u.AgentMessageChunk.Content.Text.Text)
		}
	case u.AvailableCommandsUpdate != nil:
		r.cmds = nil
		for _, c := range u.AvailableCommandsUpdate.AvailableCommands {
			r.cmds = append(r.cmds, c.Name)
		}
	case u.ToolCall != nil:
		r.tools = append(r.tools, u.ToolCall.Title)
		r.t.Logf("tool_call %s %s", u.ToolCall.Kind, u.ToolCall.Title)
	case u.ToolCallUpdate != nil && u.ToolCallUpdate.Status != nil:
		r.t.Logf("tool_update %s %s", u.ToolCallUpdate.ToolCallId, *u.ToolCallUpdate.Status)
	}
}

func (r *rec) OnPermission(ctx context.Context, req acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	r.mu.Lock()
	r.perms++
	r.mu.Unlock()
	title := ""
	if req.ToolCall.Title != nil {
		title = *req.ToolCall.Title
	}
	r.t.Logf("permission: %s", title)
	return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeSelected("allow_once")}, nil
}

func (r *rec) OnAsk(ctx context.Context, req acpx.AskRequest) (acpx.AskResponse, error) {
	r.mu.Lock()
	r.asks++
	r.mu.Unlock()
	ans := map[string][]string{}
	for _, q := range req.Questions {
		r.t.Logf("ask: %s %v", q.Question, q.Options)
		if len(q.Options) > 1 {
			ans[q.Id] = []string{q.Options[1].Label}
		} else {
			ans[q.Id] = []string{"blue"}
		}
	}
	return acpx.AskResponse{Answers: ans}, nil
}

func (r *rec) reset() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.text.String()
	r.text.Reset()
	return s
}

func start(t *testing.T, which string, r *rec) *agenthost.Host {
	return startExt(t, which, r, nil)
}

func startExt(t *testing.T, which string, r *rec, em *ext.Manager) *agenthost.Host {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	var ag agenthost.InProcAgent
	switch which {
	case "codex":
		ag = codex.New(log, em)
	case "claude":
		ag = claude.New(log, claude.Options{Ext: em})
	}
	h, err := agenthost.StartInProc(context.Background(), ag, r, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	return h
}

func TestE2E(t *testing.T) {
	which := os.Getenv("GERDR_E2E")
	if which == "" {
		t.Skip("set GERDR_E2E=codex|claude")
	}
	r := &rec{t: t}
	h := start(t, which, r)
	if !h.Steering() {
		t.Fatal("steering not advertised")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	dir := t.TempDir()
	meta := acpx.Meta(map[string]any{"mode": map[string]string{"codex": "ask", "claude": "default"}[which]})
	if which == "claude" {
		meta = acpx.Meta(map[string]any{"mode": "default", "model": "haiku"})
	}
	ns, err := h.Conn.NewSession(ctx, acp.NewSessionRequest{Cwd: dir, McpServers: []acp.McpServer{}, Meta: meta})
	if err != nil {
		t.Fatal(err)
	}
	sid := ns.SessionId
	m2 := uuid.NewString()
	t.Logf("session %s modes=%v", sid, ns.Modes != nil)

	// 1. 一轮普通对话
	res, err := h.Conn.Prompt(ctx, acp.PromptRequest{SessionId: sid, MessageId: acp.Ptr(uuid.NewString()), Prompt: []acp.ContentBlock{acp.TextBlock("Reply with exactly: PONG")}})
	if err != nil {
		t.Fatal(err)
	}
	if got := r.reset(); !strings.Contains(got, "PONG") || res.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("turn1: stop=%s text=%q", res.StopReason, got)
	}

	// 2. 写文件（走审批）+ 中途插话
	done := make(chan acp.PromptResponse, 1)
	go func() {
		res, err := h.Conn.Prompt(ctx, acp.PromptRequest{SessionId: sid, MessageId: acp.Ptr(m2), Prompt: []acp.ContentBlock{acp.TextBlock(
			"Run the shell command `sleep 5; echo one > a.txt`, then run `sleep 5; echo two > b.txt`, then summarize.")}})
		if err != nil {
			t.Error(err)
		}
		done <- res
	}()
	time.Sleep(8 * time.Second)
	sr, err := agenthost.Call[acpx.SteerResponse](ctx, h, acpx.MethodSteer, acpx.SteerRequest{SessionId: sid, MessageId: uuid.NewString(),
		Prompt: []acp.ContentBlock{acp.TextBlock("Change of plan: do NOT create b.txt. Just reply with the word BANANA after a.txt is done.")}})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("steer outcome: %s", sr.Outcome)
	res = <-done
	text := r.reset()
	t.Logf("turn2 stop=%s text=%q", res.StopReason, text)
	if _, err := os.Stat(filepath.Join(dir, "a.txt")); err != nil {
		t.Errorf("a.txt missing: %v", err)
	}
	if sr.Outcome == acpx.SteerInjected {
		// 模型可能把两条命令并成一条，b.txt 在插话到达前就写了 —— 那是模型的选择，只看它认没认插话。
		if _, err := os.Stat(filepath.Join(dir, "b.txt")); err == nil && which == "codex" {
			t.Errorf("b.txt exists although steered away")
		}
		if !strings.Contains(strings.ToUpper(text), "BANANA") {
			t.Errorf("steer not honoured: %q", text)
		}
	}

	// 3. 打断
	go func() {
		res, _ := h.Conn.Prompt(ctx, acp.PromptRequest{SessionId: sid, MessageId: acp.Ptr(uuid.NewString()), Prompt: []acp.ContentBlock{acp.TextBlock("Run this exact shell command and wait for it: python3 -c 'import time; time.sleep(40)'")}})
		done <- res
	}()
	time.Sleep(10 * time.Second)
	if err := h.Conn.Cancel(ctx, acp.CancelNotification{SessionId: sid}); err != nil {
		t.Fatal(err)
	}
	select {
	case res = <-done:
		if res.StopReason != acp.StopReasonCancelled {
			t.Errorf("cancel: stop=%s", res.StopReason)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("cancel did not end the turn")
	}
	r.reset()

	// 4. 回退到 m2 之前，问它记不记得 a.txt
	rw, err := agenthost.Call[acpx.RewindResponse](ctx, h, acpx.MethodRewind, acpx.RewindRequest{SessionId: sid, MessageId: m2})
	if err != nil {
		t.Fatal(err)
	}
	sid = rw.SessionId
	res, err = h.Conn.Prompt(ctx, acp.PromptRequest{SessionId: sid, MessageId: acp.Ptr(uuid.NewString()), Prompt: []acp.ContentBlock{acp.TextBlock(
		"Without using any tools: in this conversation so far, did I ever mention a file named a.txt? Answer YES or NO only.")}})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.ToUpper(r.reset()); !strings.Contains(got, "NO") {
		t.Errorf("rewind not effective: %q", got)
	}
	r.mu.Lock()
	t.Logf("perms=%d asks=%d tools=%v", r.perms, r.asks, r.tools)
	r.mu.Unlock()
}

// TestE2EClear：/clear 两边都是真清空（codex 是另开一个 thread），报出新会话 id，换过去之后不记得之前的事。
func TestE2EClear(t *testing.T) {
	which := os.Getenv("GERDR_E2E")
	if which == "" {
		t.Skip("set GERDR_E2E=codex|claude")
	}
	r := &rec{t: t}
	h := start(t, which, r)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	meta := acpx.Meta(map[string]any{"mode": "read-only"})
	if which == "claude" {
		meta = acpx.Meta(map[string]any{"mode": "default", "model": "haiku"})
	}
	ns, err := h.Conn.NewSession(ctx, acp.NewSessionRequest{Cwd: t.TempDir(), McpServers: []acp.McpServer{}, Meta: meta})
	if err != nil {
		t.Fatal(err)
	}
	sid := ns.SessionId
	ask := func(text string) string {
		t.Helper()
		res, err := h.Conn.Prompt(ctx, acp.PromptRequest{SessionId: sid, MessageId: acp.Ptr(uuid.NewString()), Prompt: []acp.ContentBlock{acp.TextBlock(text)}})
		if err != nil {
			t.Fatal(err)
		}
		if res.StopReason != acp.StopReasonEndTurn {
			t.Fatalf("stop=%s", res.StopReason)
		}
		return r.reset()
	}
	ask("Remember this code word: ZEBRA42. Reply with just OK.")
	r.mu.Lock()
	cmds := r.cmds
	r.mu.Unlock()
	if !slices.Contains(cmds, "clear") {
		t.Errorf("clear not in advertised commands: %v", cmds)
	}
	ask("/clear")
	r.mu.Lock()
	nid := r.cleared
	r.mu.Unlock()
	if nid == "" || nid == string(sid) {
		t.Fatalf("/clear 没报新会话 id：%q（旧的 %s）", nid, sid)
	}
	sid = acp.SessionId(nid) // 和 hub 一样，之后按新 id 发
	if got := ask("Without using tools: did I ask you to remember a code word? If yes reply with it, otherwise reply NONE."); strings.Contains(got, "ZEBRA42") {
		t.Errorf("清空之后还记得：%q", got)
	}
}

// TestE2ECompactImage：/compact 走 agent 自己的压缩、压完还记得之前的事；图片能发进去；
// 回退到 /compact 那条消息上也行。
func TestE2ECompactImage(t *testing.T) {
	which := os.Getenv("GERDR_E2E")
	if which == "" {
		t.Skip("set GERDR_E2E=codex|claude")
	}
	r := &rec{t: t}
	h := start(t, which, r)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	meta := acpx.Meta(map[string]any{"mode": "read-only"})
	if which == "claude" {
		meta = acpx.Meta(map[string]any{"mode": "default", "model": "haiku"})
	}
	ns, err := h.Conn.NewSession(ctx, acp.NewSessionRequest{Cwd: t.TempDir(), McpServers: []acp.McpServer{}, Meta: meta})
	if err != nil {
		t.Fatal(err)
	}
	sid := ns.SessionId
	ask := func(id string, blocks ...acp.ContentBlock) string {
		t.Helper()
		res, err := h.Conn.Prompt(ctx, acp.PromptRequest{SessionId: sid, MessageId: acp.Ptr(id), Prompt: blocks})
		if err != nil {
			t.Fatal(err)
		}
		if res.StopReason != acp.StopReasonEndTurn {
			t.Fatalf("stop=%s", res.StopReason)
		}
		return r.reset()
	}
	ask(uuid.NewString(), acp.TextBlock("Remember this code word: ZEBRA42. Reply with just OK."))

	r.mu.Lock()
	cmds := r.cmds
	r.mu.Unlock()
	if !slices.Contains(cmds, "compact") {
		t.Errorf("compact not in advertised commands: %v", cmds)
	}

	compactID := uuid.NewString()
	ask(compactID, acp.TextBlock("/compact"))

	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for x := 0; x < 64; x++ {
		for y := 0; y < 64; y++ {
			img.Set(x, y, color.RGBA{220, 20, 20, 255})
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	got := ask(uuid.NewString(), acp.TextBlock("What single color fills this image? Answer with one English word."),
		acp.ImageBlock(base64.StdEncoding.EncodeToString(buf.Bytes()), "image/png"))
	if !strings.Contains(strings.ToLower(got), "red") {
		t.Errorf("image: %q", got)
	}

	if got := ask(uuid.NewString(), acp.TextBlock("Without using tools: what code word did I ask you to remember? Reply with just the word.")); !strings.Contains(got, "ZEBRA42") {
		t.Errorf("after compact: %q", got)
	}

	rw, err := agenthost.Call[acpx.RewindResponse](ctx, h, acpx.MethodRewind, acpx.RewindRequest{SessionId: sid, MessageId: compactID})
	if err != nil {
		t.Fatalf("rewind to /compact: %v", err)
	}
	sid = rw.SessionId
	if got := ask(uuid.NewString(), acp.TextBlock("Without using tools: did I send you an image in this conversation? Answer YES or NO only.")); !strings.Contains(strings.ToUpper(got), "NO") {
		t.Errorf("after rewind: %q", got)
	}
}
