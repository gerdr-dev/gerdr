package codex

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/coder/acp-go-sdk"

	"github.com/zbysir/gerdr/internal/acpx"
)

// 子 agent（codex 的 multi-agent / collab）。实测 0.16x：
//   - 主 thread 上出一个 subAgentActivity item：{kind: started|interacted|interrupted|completed, agentThreadId}
//   - 子 agent 是另一个 thread，它的 turn/*、item/* 通知照样推到这条连接上（threadId = 子 thread）
//   - 主 thread 上的 collabAgentToolCall（wait / sendInput……）只是主 agent 在等它 / 跟它说话
//
// 界面上的样子和 claude 的 Agent 工具一样：主会话里一张「子 agent」卡片（工具调用），子 agent 跑的
// 命令、改的文件挂在它下面（_meta.gerdr.parentToolCallId），后台任务栏里一行（acpx.Task）。
// 子 agent 自己说的话不进主对话，最后一句当成卡片的结果和任务的摘要。

type subInfo struct {
	parent   *session
	toolCall string
	lastText string
}

func subToolID(thread string) string { return "subagent-" + thread }

// subAgentActivity：主 thread 上的那个 item。
func (a *Agent) subAgentActivity(ctx context.Context, s *session, raw json.RawMessage) {
	var it struct {
		ID            string `json:"id"`
		Kind          string `json:"kind"`
		AgentThreadID string `json:"agentThreadId"`
		AgentPath     string `json:"agentPath"`
	}
	if json.Unmarshal(raw, &it) != nil || it.AgentThreadID == "" || s.sub != nil {
		return
	}
	tc := subToolID(it.AgentThreadID)
	a.mu.Lock()
	cs := a.sessions[it.AgentThreadID]
	if cs == nil && it.Kind != "completed" && it.Kind != "interrupted" {
		cs = &session{id: s.id, cwd: s.cwd, items: map[string]*itemState{}, sub: &subInfo{parent: s, toolCall: tc}}
		a.sessions[it.AgentThreadID] = cs
	}
	a.mu.Unlock()

	s.mu.Lock()
	if s.tasks == nil {
		s.tasks = map[string]*acpx.Task{}
	}
	t := s.tasks[it.AgentThreadID]
	isNew := t == nil
	if isNew {
		t = &acpx.Task{ID: it.AgentThreadID, Type: "local_agent", Subagent: it.AgentPath, ToolCallID: tc, Status: "running", StartedAt: time.Now().UnixMilli()}
		s.tasks[it.AgentThreadID] = t
	}
	switch it.Kind {
	case "completed", "interrupted":
		if t.Status == "running" {
			t.Status = map[string]string{"completed": "completed", "interrupted": "killed"}[it.Kind]
			t.EndedAt = time.Now().UnixMilli()
		}
	}
	status, summary := t.Status, ""
	if cs != nil {
		summary = cs.sub.lastText
	}
	t.Summary = oneLine(summary, 200)
	s.mu.Unlock()

	if isNew {
		a.send(ctx, s.id, acp.StartToolCall(acp.ToolCallId(tc), "Sub-agent", acp.WithStartKind(acp.ToolKindOther), acp.WithStartStatus(acp.ToolCallStatusInProgress),
			acp.WithStartRawInput(map[string]any{"agentThreadId": it.AgentThreadID})))
	}
	if status != "running" {
		st := acp.ToolCallStatusCompleted
		if status != "completed" {
			st = acp.ToolCallStatusFailed
		}
		opts := []acp.ToolCallUpdateOpt{acp.WithUpdateStatus(st)}
		if summary != "" {
			opts = append(opts, acp.WithUpdateContent([]acp.ToolCallContent{acp.ToolContent(acp.TextBlock(summary))}))
		}
		a.send(ctx, s.id, acp.UpdateToolCall(acp.ToolCallId(tc), opts...))
	}
	a.pushTasks(ctx, s)
}

func (a *Agent) pushTasks(ctx context.Context, s *session) {
	s.mu.Lock()
	list := make([]acpx.Task, 0, len(s.tasks))
	for _, t := range s.tasks {
		list = append(list, *t)
	}
	s.mu.Unlock()
	sort.Slice(list, func(i, j int) bool { return list[i].StartedAt < list[j].StartedAt })
	a.send(ctx, s.id, acp.SessionUpdate{SessionInfoUpdate: &acp.SessionSessionInfoUpdate{Meta: acpx.Meta(map[string]any{"tasks": list})}})
}

// out：所有往 client 推的 update 都过这里。子 agent 的 update 改挂到主会话那张卡片下面，
// 它的正文 / 思考 / 计划不进主对话（只记下最后一句话当摘要）。
func (a *Agent) out(ctx context.Context, s *session, u acp.SessionUpdate) {
	sub := s.sub
	if sub == nil {
		a.send(ctx, s.id, u)
		return
	}
	switch {
	case u.ToolCall != nil:
		u.ToolCall.Meta = acpx.Meta(map[string]any{"parentToolCallId": sub.toolCall})
	case u.ToolCallUpdate != nil:
	case u.AgentMessageChunk != nil:
		u.AgentMessageChunk.Meta = acpx.Meta(map[string]any{"subagent": sub.toolCall})
		// 逐字的 delta 和最后整条（没推过 delta 时）都会走到这里；摘要只在整条时记。
		if tb := u.AgentMessageChunk.Content.Text; tb != nil && tb.Text != "" && u.AgentMessageChunk.MessageId != nil {
			st := s.state(*u.AgentMessageChunk.MessageId)
			s.mu.Lock()
			if st.sawDelta {
				st.output.WriteString(tb.Text)
			} else {
				st.output.Reset()
				st.output.WriteString(tb.Text)
			}
			text := st.output.String()
			push := time.Since(st.lastPush) > time.Second
			if push {
				st.lastPush = time.Now()
			}
			s.mu.Unlock()
			p := sub.parent
			p.mu.Lock()
			sub.lastText = text
			if t := p.tasks[s.threadID()]; t != nil {
				t.Progress = oneLine(text, 120)
			}
			p.mu.Unlock()
			if push {
				a.pushTasks(ctx, p)
			}
		}
	case u.SessionInfoUpdate != nil:
		return
	case u.AgentThoughtChunk != nil:
		u.AgentThoughtChunk.Meta = acpx.Meta(map[string]any{"subagent": sub.toolCall})
	default:
		return
	}
	a.send(ctx, s.id, u)
}

// threadID：子 agent 的伪会话里 id 是主会话的（往那儿推），自己的 thread id 记在 sessions 的 key 上。
func (s *session) threadID() string {
	if s.sub == nil {
		return s.id
	}
	for id, t := range s.sub.parent.tasks {
		if t.ToolCallID == s.sub.toolCall {
			return id
		}
	}
	return ""
}

// subPrompt：子 agent 收到的第一条 userMessage 就是主 agent 给它的任务，当成任务描述。
func (a *Agent) subPrompt(ctx context.Context, s *session, raw json.RawMessage) {
	var it userMessageItem
	_ = json.Unmarshal(raw, &it)
	var text string
	for _, c := range it.Content {
		if c.Type == "text" {
			text += c.Text
		}
	}
	a.send(ctx, s.id, acp.SessionUpdate{UserMessageChunk: &acp.SessionUpdateUserMessageChunk{Content: acp.TextBlock(text), Meta: acpx.Meta(map[string]any{"subagent": s.sub.toolCall})}})
	p := s.sub.parent
	p.mu.Lock()
	t := p.tasks[s.threadID()]
	changed := t != nil && t.Description == "" && text != ""
	if changed {
		t.Description = oneLine(text, 160)
	}
	p.mu.Unlock()
	if changed {
		a.send(ctx, p.id, acp.UpdateToolCall(acp.ToolCallId(s.sub.toolCall), acp.WithUpdateTitle("Sub-agent: "+oneLine(text, 80)),
			acp.WithUpdateRawInput(map[string]any{"prompt": text})))
		a.pushTasks(ctx, p)
	}
}
