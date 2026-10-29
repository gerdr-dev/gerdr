package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/coder/acp-go-sdk"

	"github.com/gerdr-dev/gerdr/internal/acpx"
)

// HandleNotification 把 app-server 的通知翻译成 ACP 的 session/update。
func (a *Agent) HandleNotification(method string, params json.RawMessage) {
	var head struct {
		ThreadID string `json:"threadId"`
		TurnID   string `json:"turnId"`
	}
	_ = json.Unmarshal(params, &head)
	if !strings.Contains(method, "elta") {
		a.log.Debug("codex notification", "method", method, "turn", head.TurnID)
	}
	if head.ThreadID == "" {
		return
	}
	a.mu.Lock()
	s := a.sessions[head.ThreadID]
	a.mu.Unlock()
	if s == nil {
		return
	}
	ctx := context.Background()
	if s.sub != nil {
		// 子 agent 的 thread：只要它干的活（item），它的轮次 / 标题 / 计划和主会话无关。
		switch method {
		case "turn/started", "turn/completed", "error", "thread/name/updated", "turn/plan/updated":
			return
		}
	}
	switch method {
	case "turn/started":
		var p struct {
			Turn turn `json:"turn"`
		}
		_ = json.Unmarshal(params, &p)
		s.tok.Reset()
		s.mu.Lock()
		if s.turnDone != nil {
			s.turnID = p.Turn.ID
		}
		s.mu.Unlock()
	case "turn/completed":
		var p struct {
			Turn turn `json:"turn"`
		}
		_ = json.Unmarshal(params, &p)
		s.mu.Lock()
		mine := s.turnID == "" || s.turnID == p.Turn.ID
		s.items = map[string]*itemState{}
		s.mu.Unlock()
		if mine {
			s.finish(turnResult{id: p.Turn.ID, status: p.Turn.Status, err: p.Turn.Error})
		}
	case "error":
		var p struct {
			Error     turnError `json:"error"`
			WillRetry bool      `json:"willRetry"`
		}
		_ = json.Unmarshal(params, &p)
		if p.WillRetry {
			a.out(ctx, s, acp.SessionUpdate{SessionInfoUpdate: &acp.SessionSessionInfoUpdate{Meta: acpx.Meta(map[string]any{
				"notice": p.Error.Message + "（codex 在重试）",
				// retry：界面原地更新同一行、codex 又出字就收起，不然每次重试都新起一行
				"noticeLevel": "retry",
			})}})
		}
	case "item/started":
		var p struct {
			Item json.RawMessage `json:"item"`
		}
		_ = json.Unmarshal(params, &p)
		a.itemStarted(ctx, s, p.Item)
	case "item/completed":
		var p struct {
			Item json.RawMessage `json:"item"`
		}
		_ = json.Unmarshal(params, &p)
		a.itemCompleted(ctx, s, p.Item, false)
	case "thread/tokenUsage/updated":
		// last 是刚结束的那次请求的用量：它的准数替掉估的，之后的片段算下一次请求
		var p struct {
			TokenUsage struct {
				Last struct {
					OutputTokens int `json:"outputTokens"`
					InputTokens  int `json:"inputTokens"`
				} `json:"last"`
				ModelContextWindow int `json:"modelContextWindow"`
			} `json:"tokenUsage"`
		}
		_ = json.Unmarshal(params, &p)
		if s.sub == nil {
			s.mu.Lock()
			key := strconv.Itoa(s.req)
			s.req++
			s.mu.Unlock()
			s.tok.Exact(key, p.TokenUsage.Last.OutputTokens)
			a.send(ctx, s.id, acp.SessionUpdate{SessionInfoUpdate: &acp.SessionSessionInfoUpdate{Meta: acpx.Progress(s.tok.Total())}})
			// 上下文用量：最近一次请求的输入（codex 的 inputTokens 已经含缓存命中的那部分）
			if p.TokenUsage.Last.InputTokens > 0 {
				a.send(ctx, s.id, acp.SessionUpdate{SessionInfoUpdate: &acp.SessionSessionInfoUpdate{Meta: acpx.ContextProgress(p.TokenUsage.Last.InputTokens, p.TokenUsage.ModelContextWindow)}})
			}
		}
	case "item/agentMessage/delta":
		var p struct {
			ItemID string `json:"itemId"`
			Delta  string `json:"delta"`
		}
		_ = json.Unmarshal(params, &p)
		s.state(p.ItemID).sawDelta = true
		a.countTokens(ctx, s, len(p.Delta))
		a.out(ctx, s, acp.SessionUpdate{AgentMessageChunk: &acp.SessionUpdateAgentMessageChunk{
			Content: acp.TextBlock(p.Delta), MessageId: acp.Ptr(p.ItemID),
		}})
	case "item/reasoning/summaryTextDelta", "item/reasoning/textDelta":
		var p struct {
			ItemID string `json:"itemId"`
			Delta  string `json:"delta"`
		}
		_ = json.Unmarshal(params, &p)
		s.state(p.ItemID).sawDelta = true
		a.countTokens(ctx, s, len(p.Delta))
		a.out(ctx, s, acp.SessionUpdate{AgentThoughtChunk: &acp.SessionUpdateAgentThoughtChunk{
			Content: acp.TextBlock(p.Delta), MessageId: acp.Ptr(p.ItemID),
		}})
	case "item/reasoning/summaryPartAdded":
		var p struct {
			ItemID string `json:"itemId"`
		}
		_ = json.Unmarshal(params, &p)
		if s.state(p.ItemID).sawDelta {
			a.out(ctx, s, acp.SessionUpdate{AgentThoughtChunk: &acp.SessionUpdateAgentThoughtChunk{
				Content: acp.TextBlock("\n\n"), MessageId: acp.Ptr(p.ItemID),
			}})
		}
	case "item/commandExecution/outputDelta":
		var p struct {
			ItemID string `json:"itemId"`
			Delta  string `json:"delta"`
		}
		_ = json.Unmarshal(params, &p)
		st := s.state(p.ItemID)
		s.mu.Lock()
		st.output.WriteString(p.Delta)
		push := time.Since(st.lastPush) > 300*time.Millisecond
		var out string
		if push {
			st.lastPush = time.Now()
			out = tail(st.output.String(), 16<<10)
		}
		s.mu.Unlock()
		if push {
			a.out(ctx, s, acp.UpdateToolCall(acp.ToolCallId(p.ItemID), acp.WithUpdateContent([]acp.ToolCallContent{acp.ToolContent(acp.TextBlock(out))})))
		}
	case "item/fileChange/patchUpdated":
		var p struct {
			ItemID  string             `json:"itemId"`
			Changes []fileUpdateChange `json:"changes"`
		}
		_ = json.Unmarshal(params, &p)
		a.out(ctx, s, acp.UpdateToolCall(acp.ToolCallId(p.ItemID), acp.WithUpdateContent(diffContent(p.Changes)), acp.WithUpdateLocations(locations(p.Changes))))
	case "turn/plan/updated":
		var p struct {
			Plan []planStep `json:"plan"`
		}
		_ = json.Unmarshal(params, &p)
		var entries []acp.PlanEntry
		for _, st := range p.Plan {
			entries = append(entries, acp.PlanEntry{Content: st.Step, Priority: acp.PlanEntryPriorityMedium, Status: planStatus(st.Status)})
		}
		a.out(ctx, s, acp.UpdatePlan(entries...))
	case "thread/name/updated":
		var p struct {
			Name *string `json:"name"`
		}
		_ = json.Unmarshal(params, &p)
		// codex 自己不起名，thread 的名字只有人改（/rename、Codex App 里改）才会变：算人改的，之后不被自动标题盖掉（hub/title.go）
		if p.Name != nil && *p.Name != "" {
			a.out(ctx, s, acp.SessionUpdate{SessionInfoUpdate: &acp.SessionSessionInfoUpdate{Title: p.Name, Meta: acpx.Meta(map[string]any{"renamed": true})}})
		}
	}
}

func (a *Agent) countTokens(ctx context.Context, s *session, chars int) {
	if s.sub != nil {
		return
	}
	s.mu.Lock()
	key := strconv.Itoa(s.req)
	s.mu.Unlock()
	s.tok.Chars(key, chars)
	if s.tok.Due() {
		a.send(ctx, s.id, acp.SessionUpdate{SessionInfoUpdate: &acp.SessionSessionInfoUpdate{Meta: acpx.Progress(s.tok.Total())}})
	}
}

func (s *session) state(id string) *itemState {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.items[id]
	if st == nil {
		st = &itemState{}
		s.items[id] = st
	}
	return st
}

func (a *Agent) send(ctx context.Context, sid string, u acp.SessionUpdate) {
	if a.conn == nil {
		return
	}
	if err := a.conn.SessionUpdate(ctx, acp.SessionNotification{SessionId: acp.SessionId(sid), Update: u}); err != nil {
		a.log.Debug("session update failed", "err", err)
	}
}

func (a *Agent) itemStarted(ctx context.Context, s *session, raw json.RawMessage) {
	var h itemHead
	_ = json.Unmarshal(raw, &h)
	switch h.Type {
	case "commandExecution":
		var it commandItem
		_ = json.Unmarshal(raw, &it)
		title := stripShell(it.Command)
		s.state(it.ID).title = title
		a.out(ctx, s, acp.StartToolCall(acp.ToolCallId(it.ID), title,
			acp.WithStartKind(commandKind(it.CommandActions)), acp.WithStartStatus(acp.ToolCallStatusInProgress),
			acp.WithStartRawInput(map[string]any{"command": title, "cwd": it.Cwd})))
	case "fileChange":
		var it fileChangeItem
		_ = json.Unmarshal(raw, &it)
		title := fileTitle(it.Changes)
		s.state(it.ID).title = title
		a.out(ctx, s, acp.StartToolCall(acp.ToolCallId(it.ID), title,
			acp.WithStartKind(acp.ToolKindEdit), acp.WithStartStatus(acp.ToolCallStatusInProgress),
			acp.WithStartLocations(locations(it.Changes)), acp.WithStartContent(diffContent(it.Changes))))
	case "mcpToolCall":
		var it mcpToolCallItem
		_ = json.Unmarshal(raw, &it)
		var args any
		_ = json.Unmarshal(it.Arguments, &args)
		title := it.Server + " · " + it.Tool
		s.state(it.ID).title = title
		a.out(ctx, s, acp.StartToolCall(acp.ToolCallId(it.ID), title,
			acp.WithStartKind(acp.ToolKindOther), acp.WithStartStatus(acp.ToolCallStatusInProgress), acp.WithStartRawInput(args)))
	case "webSearch":
		var it webSearchItem
		_ = json.Unmarshal(raw, &it)
		title := "Web search"
		if it.Query != "" {
			title = "Search: " + it.Query
		}
		a.out(ctx, s, acp.StartToolCall(acp.ToolCallId(it.ID), title,
			acp.WithStartKind(acp.ToolKindFetch), acp.WithStartStatus(acp.ToolCallStatusInProgress), acp.WithStartRawInput(map[string]any{"query": it.Query})))
	case "subAgentActivity":
		a.subAgentActivity(ctx, s, raw)
	case "imageView", "imageGeneration", "dynamicToolCall", "collabAgentToolCall", "contextCompaction", "enteredReviewMode", "exitedReviewMode":
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		a.out(ctx, s, acp.StartToolCall(acp.ToolCallId(h.ID), genericTitle(h.Type, m),
			acp.WithStartKind(acp.ToolKindOther), acp.WithStartStatus(acp.ToolCallStatusInProgress), acp.WithStartRawInput(m)))
	}
}

// itemCompleted：live=false 时是流式期间的结束事件；replay=true 时是 LoadSession 重放历史。
func (a *Agent) itemCompleted(ctx context.Context, s *session, raw json.RawMessage, replay bool) {
	var h itemHead
	_ = json.Unmarshal(raw, &h)
	switch h.Type {
	case "agentMessage":
		var it agentMessageItem
		_ = json.Unmarshal(raw, &it)
		st := s.state(it.ID)
		var meta map[string]any
		if len(it.Questions) > 0 {
			// codex 默认模式下没有阻塞式提问工具：它把问题写在一条 async 消息里，然后自己 sleep
			// 等回复（回复走 turn/steer 进来）。把选项挂在 _meta 上，界面画成「点一下就回」的按钮。
			var qs []map[string]any
			for _, q := range it.Questions {
				qs = append(qs, map[string]any{"question": q.Title, "options": q.Options})
			}
			meta = acpx.Meta(map[string]any{"quickReplies": qs})
		}
		if !st.sawDelta || replay {
			a.out(ctx, s, acp.SessionUpdate{AgentMessageChunk: &acp.SessionUpdateAgentMessageChunk{
				Content: acp.TextBlock(it.Text), MessageId: acp.Ptr(it.ID), Meta: meta,
			}})
		} else if meta != nil {
			a.out(ctx, s, acp.SessionUpdate{AgentMessageChunk: &acp.SessionUpdateAgentMessageChunk{
				Content: acp.TextBlock(""), MessageId: acp.Ptr(it.ID), Meta: meta,
			}})
		}
	case "reasoning":
		var it reasoningItem
		_ = json.Unmarshal(raw, &it)
		st := s.state(it.ID)
		if !st.sawDelta || replay {
			text := strings.Join(it.Summary, "\n\n")
			if text == "" {
				text = strings.Join(it.Content, "\n\n")
			}
			if text != "" {
				a.out(ctx, s, acp.SessionUpdate{AgentThoughtChunk: &acp.SessionUpdateAgentThoughtChunk{
					Content: acp.TextBlock(text), MessageId: acp.Ptr(it.ID),
				}})
			}
		}
	case "subAgentActivity":
		a.subAgentActivity(ctx, s, raw)
	case "userMessage":
		if s.sub != nil {
			a.subPrompt(ctx, s, raw)
			return
		}
		if !replay {
			return // 用户自己说的话 client 那边已经有了（发出去的时候就显示了）
		}
		var it userMessageItem
		_ = json.Unmarshal(raw, &it)
		for _, c := range it.Content {
			if c.Type == "text" {
				a.out(ctx, s, acp.SessionUpdate{UserMessageChunk: &acp.SessionUpdateUserMessageChunk{
					Content: acp.TextBlock(c.Text), MessageId: it.ClientID,
				}})
			}
		}
	case "commandExecution":
		var it commandItem
		_ = json.Unmarshal(raw, &it)
		if replay {
			a.itemStarted(ctx, s, raw)
		}
		out := ""
		if it.AggregatedOutput != nil {
			out = *it.AggregatedOutput
		} else {
			s.mu.Lock()
			if st := s.items[it.ID]; st != nil {
				out = st.output.String()
			}
			s.mu.Unlock()
		}
		status := acp.ToolCallStatusCompleted
		if it.Status == "failed" || it.Status == "declined" || (it.ExitCode != nil && *it.ExitCode != 0) {
			status = acp.ToolCallStatusFailed
		}
		rawOut := map[string]any{"status": it.Status}
		if it.ExitCode != nil {
			rawOut["exitCode"] = *it.ExitCode
		}
		if it.DurationMs != nil {
			rawOut["durationMs"] = *it.DurationMs
		}
		var content []acp.ToolCallContent
		if out != "" {
			content = []acp.ToolCallContent{acp.ToolContent(acp.TextBlock(tail(out, 64<<10)))}
		}
		a.out(ctx, s, acp.UpdateToolCall(acp.ToolCallId(it.ID), acp.WithUpdateStatus(status), acp.WithUpdateContent(content), acp.WithUpdateRawOutput(rawOut)))
	case "fileChange":
		var it fileChangeItem
		_ = json.Unmarshal(raw, &it)
		if replay {
			a.itemStarted(ctx, s, raw)
		}
		status := acp.ToolCallStatusCompleted
		if it.Status == "failed" || it.Status == "declined" {
			status = acp.ToolCallStatusFailed
		}
		a.out(ctx, s, acp.UpdateToolCall(acp.ToolCallId(it.ID), acp.WithUpdateStatus(status), acp.WithUpdateContent(diffContent(it.Changes))))
	case "mcpToolCall":
		var it mcpToolCallItem
		_ = json.Unmarshal(raw, &it)
		if replay {
			a.itemStarted(ctx, s, raw)
		}
		status := acp.ToolCallStatusCompleted
		var text string
		if it.Status == "failed" {
			status = acp.ToolCallStatusFailed
			text = string(it.Error)
		} else {
			text = mcpResultText(it.Result)
		}
		var content []acp.ToolCallContent
		if text != "" {
			content = []acp.ToolCallContent{acp.ToolContent(acp.TextBlock(tail(text, 64<<10)))}
		}
		a.out(ctx, s, acp.UpdateToolCall(acp.ToolCallId(it.ID), acp.WithUpdateStatus(status), acp.WithUpdateContent(content)))
	case "webSearch", "imageView", "imageGeneration", "dynamicToolCall", "collabAgentToolCall", "contextCompaction", "enteredReviewMode", "exitedReviewMode":
		if replay {
			a.itemStarted(ctx, s, raw)
		}
		a.out(ctx, s, acp.UpdateToolCall(acp.ToolCallId(h.ID), acp.WithUpdateStatus(acp.ToolCallStatusCompleted)))
	case "plan":
		var it struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(raw, &it)
		if replay && it.Text != "" {
			a.out(ctx, s, acp.SessionUpdate{AgentMessageChunk: &acp.SessionUpdateAgentMessageChunk{Content: acp.TextBlock(it.Text), MessageId: acp.Ptr(h.ID)}})
		}
	}
}

func (a *Agent) replayItem(ctx context.Context, s *session, raw json.RawMessage) {
	a.itemCompleted(ctx, s, raw, true)
}

func commandKind(actions []commandAction) acp.ToolKind {
	if len(actions) == 0 {
		return acp.ToolKindExecute
	}
	kind := ""
	for _, ac := range actions {
		k := ""
		switch ac.Type {
		case "read":
			k = "read"
		case "listFiles", "search":
			k = "search"
		default:
			return acp.ToolKindExecute
		}
		if kind != "" && kind != k {
			return acp.ToolKindExecute
		}
		kind = k
	}
	if kind == "read" {
		return acp.ToolKindRead
	}
	return acp.ToolKindSearch
}

func fileTitle(ch []fileUpdateChange) string {
	if len(ch) == 0 {
		return "Edit files"
	}
	verb := "Edit"
	switch ch[0].Kind.Type {
	case "add":
		verb = "Create"
	case "delete":
		verb = "Delete"
	}
	if len(ch) == 1 {
		return verb + " " + ch[0].Path
	}
	return fmt.Sprintf("Edit %d files", len(ch))
}

func locations(ch []fileUpdateChange) []acp.ToolCallLocation {
	var out []acp.ToolCallLocation
	for _, c := range ch {
		out = append(out, acp.ToolCallLocation{Path: c.Path})
	}
	return out
}

// diffContent：codex 给的是统一 diff，不是 ACP diff 要的「整份旧文本 / 新文本」。
// 所以放成一段文本，同时把原始 diff 挂在 _meta 上让界面画成 diff。
func diffContent(ch []fileUpdateChange) []acp.ToolCallContent {
	var out []acp.ToolCallContent
	for _, c := range ch {
		tc := acp.ToolContent(acp.TextBlock(c.Diff))
		tc.Content.Meta = acpx.Meta(map[string]any{"unifiedDiff": map[string]any{"path": c.Path, "kind": c.Kind.Type, "diff": c.Diff}})
		out = append(out, tc)
	}
	return out
}

func planStatus(s string) acp.PlanEntryStatus {
	switch s {
	case "completed":
		return acp.PlanEntryStatusCompleted
	case "inProgress", "in_progress":
		return acp.PlanEntryStatusInProgress
	}
	return acp.PlanEntryStatusPending
}

func genericTitle(typ string, m map[string]any) string {
	switch typ {
	case "imageView":
		if p, ok := m["path"].(string); ok {
			return "View image " + p
		}
	case "contextCompaction":
		return "Compact context"
	case "dynamicToolCall":
		if t, ok := m["tool"].(string); ok {
			return t
		}
	case "collabAgentToolCall":
		// 子 agent 本身另有一张卡片（subagents.go），这里是主 agent 对它做的动作。
		switch m["tool"] {
		case "spawnAgent":
			return "Spawn sub-agent"
		case "wait":
			return "Wait for sub-agents"
		case "sendInput", "sendMessage", "followupTask":
			return "Message sub-agent"
		case "interruptAgent", "closeAgent":
			return "Stop sub-agent"
		case "listAgents":
			return "List sub-agents"
		}
		return "Sub-agent"
	}
	return typ
}

func mcpResultText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var r struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if json.Unmarshal(raw, &r) == nil && len(r.Content) > 0 {
		var b strings.Builder
		for _, c := range r.Content {
			if c.Type == "text" {
				b.WriteString(c.Text)
				b.WriteString("\n")
			}
		}
		if b.Len() > 0 {
			return b.String()
		}
	}
	return string(raw)
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}
