package native

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/coder/acp-go-sdk"

	"github.com/zbysir/gerdr/internal/acpx"
	"github.com/zbysir/gerdr/internal/agents/native/llm"
)

// 子 agent：task 工具起一个新的循环（同一个模型、自己的历史），跑完把最后一句话交回给主 agent。
// 界面上和 claude 的 Agent 工具一样：主对话里一张卡片，子 agent 调的工具挂在它下面（_meta.gerdr.parentToolCallId），
// 它说的话打上 _meta.gerdr.subagent（主对话不显示、只读面板显示），后台任务栏里一行（acpx.Task）。
//
// 同一批里的几个 task 并发跑（task 算只读工具：它自己调的写操作照样各自过审批）。子 agent 不能再开子 agent、不能问人。

type subRun struct {
	toolCall string // 主会话里那张卡片的 tool call id

	mu       sync.Mutex
	lastPush time.Time
	text     strings.Builder // 当前这次生成说的话（任务栏的进度）
}

func (sr *subRun) out(r *run, u acp.SessionUpdate) {
	switch {
	case u.ToolCall != nil:
		u.ToolCall.Meta = acpx.Meta(map[string]any{"parentToolCallId": sr.toolCall})
	case u.ToolCallUpdate != nil:
	case u.AgentMessageChunk != nil:
		u.AgentMessageChunk.Meta = acpx.Meta(map[string]any{"subagent": sr.toolCall})
		if t := u.AgentMessageChunk.Content.Text; t != nil {
			sr.mu.Lock()
			sr.text.WriteString(t.Text)
			progress := sr.text.String()
			push := time.Since(sr.lastPush) > time.Second
			if push {
				sr.lastPush = time.Now()
			}
			sr.mu.Unlock()
			if push {
				r.s.mu.Lock()
				if t := r.s.tasks[sr.toolCall]; t != nil {
					t.Progress = oneLine(lastLine(progress), 120)
				}
				r.s.mu.Unlock()
				r.s.pushTasks()
			}
		}
	case u.AgentThoughtChunk != nil:
		u.AgentThoughtChunk.Meta = acpx.Meta(map[string]any{"subagent": sr.toolCall})
	case u.Plan != nil:
		return // 子 agent 的待办不盖掉主会话的
	default:
		return
	}
	r.s.send(u)
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, "\n"); i >= 0 {
		return s[i+1:]
	}
	return s
}

var taskTool = &tool{
	name: "task",
	desc: "Launch a sub-agent to handle a self-contained task autonomously (e.g. searching a large codebase, investigating how something works, " +
		"doing an independent piece of work). It has the same tools as you except task and ask_user, starts with no context " +
		"— so the prompt must contain everything it needs — and returns a single final report. " +
		"Launch several in one response to run them in parallel. Do not use it for simple lookups you can do with one or two tool calls.",
	params: obj(map[string]any{
		"description": prop("string", "A short (3-5 word) description of the task"),
		"prompt":      prop("string", "The full task for the sub-agent, including all context it needs and what to report back"),
	}, "description", "prompt"),
	readOnly: true,
	show: func(args map[string]any, cwd string) (string, acp.ToolKind, []acp.ToolCallLocation) {
		return "Sub-agent: " + oneLine(str(args, "description"), 80), acp.ToolKindOther, nil
	},
	run: func(c *call, args map[string]any) result {
		parent := c.r
		if parent.sub != nil {
			return errResult("sub-agents cannot start sub-agents")
		}
		s := parent.s
		desc, prompt := str(args, "description"), str(args, "prompt")
		if strings.TrimSpace(prompt) == "" {
			return errResult("prompt is empty")
		}
		sr := &subRun{toolCall: c.id}
		r := &run{s: s, sub: sr, model: parent.model, effort: parent.effort,
			system: systemPrompt(s.cwd(), nil, true),
			msgs:   []llm.Message{{Role: "user", Content: []llm.Part{llm.TextPart(prompt)}}}}
		for _, t := range parent.tools {
			if t.name != "task" && t.name != "ask_user" {
				r.tools = append(r.tools, t)
			}
		}
		ctx, cancel := context.WithCancel(c.ctx)
		defer cancel()
		s.mu.Lock()
		if s.tasks == nil {
			s.tasks, s.subCancel = map[string]*acpx.Task{}, map[string]context.CancelFunc{}
		}
		s.subCancel[c.id] = cancel
		task := &acpx.Task{ID: c.id, Type: "local_agent", Description: oneLine(desc, 160), ToolCallID: c.id, Status: "running", StartedAt: time.Now().UnixMilli()}
		s.tasks[c.id] = task
		s.mu.Unlock()
		s.pushTasks()
		// 子 agent 收到的任务（只读面板里第一条）
		s.send(acp.SessionUpdate{UserMessageChunk: &acp.SessionUpdateUserMessageChunk{Content: acp.TextBlock(prompt), Meta: acpx.Meta(map[string]any{"subagent": c.id})}})

		err := r.loop(ctx)

		status := "completed"
		switch {
		case err == errStopped || ctx.Err() != nil:
			status = "killed"
		case err != nil:
			status = "failed"
		}
		s.mu.Lock()
		delete(s.subCancel, c.id)
		task.Status, task.EndedAt, task.Summary = status, time.Now().UnixMilli(), oneLine(r.last, 200)
		var calls int
		for _, m := range r.msgs {
			calls += len(m.ToolCalls)
		}
		task.ToolUses = calls
		s.mu.Unlock()
		s.pushTasks()
		switch status {
		case "killed":
			return errResult("The sub-agent was interrupted.")
		case "failed":
			return errResult("The sub-agent failed: %v", err)
		}
		if strings.TrimSpace(r.last) == "" {
			return errResult("The sub-agent finished without a report.")
		}
		return result{text: r.last, display: []acp.ToolCallContent{acp.ToolContent(acp.TextBlock(r.last))}}
	},
}

// ---- ask_user ----

var askTool = &tool{
	name: "ask_user",
	desc: "Ask the user one or more multiple-choice questions and wait for the answers. Use it only for decisions that are genuinely the user's " +
		"(preferences, ambiguous requirements, choosing between real trade-offs) — not to confirm things you can decide or verify yourself. " +
		"2-4 options per question; the user can always type their own answer.",
	params: obj(map[string]any{
		"questions": map[string]any{"type": "array", "minItems": 1, "maxItems": 4, "items": obj(map[string]any{
			"question": prop("string", "The full question"),
			"header":   prop("string", "Very short label (max 12 chars)"),
			"options": map[string]any{"type": "array", "items": obj(map[string]any{
				"label":       prop("string", "Short option text"),
				"description": prop("string", "What choosing it means"),
			}, "label")},
			"multi_select": prop("boolean", "Allow choosing several options"),
		}, "question", "options")},
	}, "questions"),
	readOnly: true,
	hidden:   true, // 界面上是提问卡片，不需要再来一行工具调用
	show: func(args map[string]any, cwd string) (string, acp.ToolKind, []acp.ToolCallLocation) {
		return "Ask the user", acp.ToolKindOther, nil
	},
	run: func(c *call, args map[string]any) result {
		raw, _ := json.Marshal(args["questions"])
		var qs []struct {
			Question    string `json:"question"`
			Header      string `json:"header"`
			MultiSelect bool   `json:"multi_select"`
			Options     []struct {
				Label       string `json:"label"`
				Description string `json:"description"`
			} `json:"options"`
		}
		if err := json.Unmarshal(raw, &qs); err != nil || len(qs) == 0 {
			return errResult("questions must be a non-empty array of {question, options}")
		}
		req := acpx.AskRequest{SessionId: acp.SessionId(c.r.s.currentID()), ToolCallId: c.id}
		for i, q := range qs {
			aq := acpx.AskQuestion{Id: fmt.Sprint(i), Header: q.Header, Question: q.Question, MultiSelect: q.MultiSelect, AllowOther: true}
			for _, o := range q.Options {
				aq.Options = append(aq.Options, acpx.AskOption{Label: o.Label, Description: o.Description})
			}
			req.Questions = append(req.Questions, aq)
		}
		rawRes, err := c.r.s.a.conn.CallExtension(c.ctx, acpx.MethodAsk, req)
		if err != nil {
			if c.ctx.Err() != nil {
				return result{}
			}
			return errResult("asking the user failed: %v", err)
		}
		var res acpx.AskResponse
		_ = json.Unmarshal(rawRes, &res)
		if res.Cancelled {
			return errResult("%s", acpx.AskDeclined(res.Reason, res.Text))
		}
		var b strings.Builder
		for i, q := range qs {
			ans := res.Answers[fmt.Sprint(i)]
			fmt.Fprintf(&b, "Q: %s\nA: %s\n", q.Question, strings.Join(ans, ", "))
		}
		return result{text: strings.TrimSpace(b.String())}
	},
}
