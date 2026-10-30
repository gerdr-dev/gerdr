package agenthost_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/acp-go-sdk"
	"github.com/google/uuid"

	"github.com/gerdr-dev/gerdr/acpx"
)

// TestE2ETasks：子 agent 的话带 subagent 标记推出来（不混进主对话）、任务列表里有它；
// claude 还测后台命令：任务列表里有、跑完之后 CLI 自己开一轮（autoTurn true → false）。
func TestE2ETasks(t *testing.T) {
	which := os.Getenv("GERDR_E2E")
	if which == "" {
		t.Skip("set GERDR_E2E=codex|claude")
	}
	r := &rec{t: t}
	h := start(t, which, r)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	meta := acpx.Meta(map[string]any{"mode": "full-access"})
	prompt := "Spawn one sub-agent whose task is: run `echo SUBMARK` and reply with exactly the output. Wait for it, then reply DONE."
	if which == "claude" {
		meta = acpx.Meta(map[string]any{"mode": "bypassPermissions", "model": "haiku"})
		prompt = "Do two things: (1) start the shell command `sleep 30; echo BGMARK` with run_in_background=true; " +
			"(2) launch a subagent (Agent tool, general-purpose, foreground) that runs `echo SUBMARK` via Bash and replies with exactly its output. " +
			"After the subagent returns reply STARTED. When the background command finishes later, tell me its output."
	}
	ns, err := h.Conn.NewSession(ctx, acp.NewSessionRequest{Cwd: t.TempDir(), McpServers: []acp.McpServer{}, Meta: meta})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Conn.Prompt(ctx, acp.PromptRequest{SessionId: ns.SessionId, MessageId: acp.Ptr(uuid.NewString()), Prompt: []acp.ContentBlock{acp.TextBlock(prompt)}}); err != nil {
		t.Fatal(err)
	}
	main := r.reset()
	r.mu.Lock()
	t.Logf("main=%q", main)
	for k, v := range r.subText {
		t.Logf("sub %s: %q", k, v)
	}
	var agentTask bool
	for _, x := range r.tasks {
		if x.Type == "local_agent" && x.ToolCallID != "" {
			agentTask = true
			if !strings.Contains(r.subText[x.ToolCallID], "SUBMARK") {
				t.Errorf("sub-agent text not tagged with its tool call: %v", r.subText)
			}
		}
	}
	r.mu.Unlock()
	if !agentTask {
		t.Errorf("no local_agent task")
	}
	if strings.Contains(main, "SUBMARK") && which == "codex" {
		// codex 主 agent 可能自己复述，不算错；只记一笔。
		t.Logf("main text mentions SUBMARK")
	}
	if which != "claude" {
		return
	}
	// 后台命令：等它跑完、CLI 自己开的那一轮结束。
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		n := len(r.autos)
		done := n >= 2 && !r.autos[n-1]
		r.mu.Unlock()
		if done {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.autos) < 2 || !r.autos[0] || r.autos[len(r.autos)-1] {
		t.Fatalf("auto turn not seen: %v", r.autos)
	}
	var bash bool
	for _, x := range r.tasks {
		if x.Type == "local_bash" {
			bash = true
			if x.Status == "running" {
				t.Errorf("bash task still running: %+v", x)
			}
		}
	}
	if !bash {
		t.Errorf("no local_bash task: %+v", r.tasks)
	}
	if got := r.text.String(); !strings.Contains(got, "BGMARK") {
		t.Errorf("auto turn text missing BGMARK: %q", got)
	}
}
