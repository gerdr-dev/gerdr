package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/coder/acp-go-sdk"

	"github.com/gerdr-dev/gerdr/internal/acpx"
	"github.com/gerdr-dev/gerdr/internal/sysenv"
)

// onLine 处理 claude stdout 的一行（control_response 已经在 proc 里分走了）。
func (s *session) onLine(pr *proc, m map[string]json.RawMessage, raw []byte) {
	ctx := context.Background()
	switch str(m["type"]) {
	case "stream_event":
		if !isNull(m["parent_tool_use_id"]) {
			return // 子 agent 的逐字输出不往外推，它的工具调用在整条消息里处理
		}
		s.onStreamEvent(ctx, m["event"], str(m["api_message_id"]))
	case "assistant":
		var msg struct {
			Message struct {
				ID      string            `json:"id"`
				Content []json.RawMessage `json:"content"`
			} `json:"message"`
			Parent *string `json:"parent_tool_use_id"`
			Error  string  `json:"error"`
		}
		_ = json.Unmarshal(raw, &msg)
		parent := ""
		if msg.Parent != nil {
			parent = *msg.Parent
		}
		// A failed API call comes as a synthetic assistant message (error set, the error text as its content), followed by an
		// is_error result with the same text. That result already becomes the turn's error (the retry notice / turn-end line),
		// so showing this one too printed the error once per attempt. Only dropped inside our own turn: there the result is
		// guaranteed to surface it.
		if msg.Error != "" && parent == "" {
			s.mu.Lock()
			inTurn := s.turn != nil
			s.mu.Unlock()
			if inTurn {
				return
			}
		}
		for _, c := range msg.Message.Content {
			s.onAssistantBlock(ctx, msg.Message.ID, parent, c)
		}
	case "user":
		var msg struct {
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
			ToolUseResult json.RawMessage `json:"tool_use_result"`
			Parent        *string         `json:"parent_tool_use_id"`
		}
		_ = json.Unmarshal(raw, &msg)
		var blocks []json.RawMessage
		if json.Unmarshal(msg.Message.Content, &blocks) != nil {
			return
		}
		for _, b := range blocks {
			// 子 agent 收到的任务（主 agent 写给它的 prompt）：子 agent 面板里的第一条「用户消息」。
			if msg.Parent != nil && *msg.Parent != "" {
				var tb struct {
					Type string `json:"type"`
					Text string `json:"text"`
				}
				if json.Unmarshal(b, &tb) == nil && tb.Type == "text" && tb.Text != "" {
					s.update(ctx, acp.SessionUpdate{UserMessageChunk: &acp.SessionUpdateUserMessageChunk{Content: acp.TextBlock(tb.Text), Meta: acpx.Meta(map[string]any{"subagent": *msg.Parent})}})
					continue
				}
			}
			s.onToolResult(ctx, b, msg.ToolUseResult)
		}
	case "result":
		var mu struct {
			ModelUsage map[string]struct {
				ContextWindow int `json:"contextWindow"`
			} `json:"modelUsage"`
		}
		_ = json.Unmarshal(raw, &mu)
		for _, m := range mu.ModelUsage {
			if m.ContextWindow > 0 {
				s.mu.Lock()
				s.ctxWindow = m.ContextWindow
				n := s.ctxTokens
				s.mu.Unlock()
				if n > 0 {
					s.update(ctx, acp.SessionUpdate{SessionInfoUpdate: &acp.SessionSessionInfoUpdate{Meta: acpx.ContextProgress(n, m.ContextWindow)}})
				}
				break
			}
		}
		var r struct {
			Subtype    string   `json:"subtype"`
			IsError    bool     `json:"is_error"`
			Result     string   `json:"result"`
			StopReason string   `json:"stop_reason"`
			Errors     []string `json:"errors"`
		}
		_ = json.Unmarshal(raw, &r)
		s.mu.Lock()
		t := s.turn
		sawState := s.sawState
		if t != nil {
			t.stop = stopReason(r.StopReason, r.Subtype)
			if r.IsError && !t.cancelled {
				msg := r.Result
				if msg == "" {
					msg = strings.Join(r.Errors, "\n")
				}
				if msg == "" {
					msg = r.Subtype
				}
				t.errText = s.a.loginHint(msg)
			} else {
				t.errText = ""
			}
		}
		s.msgDelta = nil
		s.mu.Unlock()
		if !sawState && t != nil {
			s.finishTurn(t.stop)
		}
	case "system":
		s.onSystem(ctx, m, raw)
	case "control_request":
		var req struct {
			RequestID string          `json:"request_id"`
			Request   json.RawMessage `json:"request"`
		}
		_ = json.Unmarshal(raw, &req)
		go s.onControlRequest(pr, req.RequestID, req.Request)
	case "command_lifecycle":
		// 插话（以及每条写进 stdin 的消息）在 CLI 队列里的进度：queued = 排着；started = 被拿进这一轮了。
		// 插话要等手上这个工具调用结束才被读到（实测 sleep 8 中途插，排了 5 秒多），这段时间界面上该显示「排队中」
		var c struct {
			CommandUUID string `json:"command_uuid"`
			State       string `json:"state"`
		}
		_ = json.Unmarshal(raw, &c)
		// 这一轮的那句被拿进去跑了 = 这一轮开始了，后面的 idle 就是它的结束。光看 running 不够：
		// --resume 一个有没跑完的后台任务的会话，CLI 一起来就先自己跑起来（报那个任务 stopped），running 到的时候
		// 这一轮还没挂上（被当成它自己开的一轮），我们这句接着在同一段 running 里跑完才 idle —— 中间不再有 running，
		// 只认 running 的话这一轮永远等不到结束，界面一直转（实测）。
		if c.State == "started" {
			s.mu.Lock()
			if t := s.turn; t != nil && (t.id == "" || t.id == c.CommandUUID) {
				t.sawRunning = true
			}
			s.mu.Unlock()
		}
		if c.CommandUUID != "" && c.State != "" {
			s.update(ctx, acp.SessionUpdate{SessionInfoUpdate: &acp.SessionSessionInfoUpdate{Meta: acpx.Meta(map[string]any{
				"steer": map[string]any{"id": c.CommandUUID, "state": c.State},
			})}})
		}
	case "control_cancel_request":
		s.cancelControl(str(m["request_id"]))
	}
}

func isNull(raw json.RawMessage) bool {
	return len(raw) == 0 || string(raw) == "null"
}

func stopReason(apiStop, subtype string) acp.StopReason {
	switch {
	case subtype == "error_max_turns":
		return acp.StopReasonMaxTurnRequests
	case apiStop == "max_tokens":
		return acp.StopReasonMaxTokens
	case apiStop == "refusal":
		return acp.StopReasonRefusal
	}
	return acp.StopReasonEndTurn
}

func (s *session) onSystem(ctx context.Context, m map[string]json.RawMessage, raw []byte) {
	switch sub := str(m["subtype"]); sub {
	case "session_title_changed":
		// 在会话里用了 claude 自己的 /rename：标题跟着改，而且算人改的（之后不再被自动起的标题盖掉）。
		if t := str(m["title"]); t != "" {
			s.update(ctx, acp.SessionUpdate{SessionInfoUpdate: &acp.SessionSessionInfoUpdate{Title: &t, Meta: acpx.Meta(map[string]any{"renamed": true})}})
		}
	case "api_retry":
		// CLI 自己在重试 API（网络断了、过载、5xx）：给人看一行，不然就是干等着不知道为什么。
		var r struct {
			Attempt     int    `json:"attempt"`
			MaxRetries  int    `json:"max_retries"`
			DelayMS     int    `json:"retry_delay_ms"`
			ErrorStatus *int   `json:"error_status"`
			Error       string `json:"error"`
		}
		_ = json.Unmarshal(raw, &r)
		why := "连不上 API"
		if r.ErrorStatus != nil {
			why = fmt.Sprintf("API 返回 %d", *r.ErrorStatus)
		}
		s.update(ctx, acp.SessionUpdate{SessionInfoUpdate: &acp.SessionSessionInfoUpdate{Meta: acpx.Meta(map[string]any{
			// Same wording as the hub's own retry notice: the UI counts "N 秒后自动重试" down using noticeDelayMs
			"notice": fmt.Sprintf("%s，%.0f 秒后自动重试（第 %d/%d 次）", why, float64(r.DelayMS)/1000, r.Attempt, r.MaxRetries),
			// retry：界面上重试成功（agent 又出字了）就收起这行，不然它一直挂着
			"noticeLevel":   "retry",
			"noticeDelayMs": r.DelayMS,
		})}})
	case "task_started", "task_progress", "task_updated", "task_notification", "background_tasks_changed":
		s.taskEvent(ctx, sub, raw)
	case "session_state_changed":
		state := str(m["state"])
		s.mu.Lock()
		s.sawState = true
		t := s.turn
		if t != nil && state == "running" {
			t.sawRunning = true
		}
		var stop acp.StopReason
		if t != nil {
			stop = t.stop
		}
		// 没有 session/prompt 在等、CLI 却跑起来了：它自己开的一轮（后台任务汇报）。
		autoStart := t == nil && state == "running" && !s.auto
		autoEnd := s.auto && state == "idle"
		if autoStart {
			s.auto = true
		}
		if autoEnd || t != nil {
			s.auto = false
		}
		s.mu.Unlock()
		if autoStart {
			s.tok.Reset()
			s.autoTurn(ctx, true)
		}
		if autoEnd {
			s.autoTurn(ctx, false)
		}
		if state == "idle" && t != nil && t.sawRunning {
			if stop == "" {
				stop = acp.StopReasonEndTurn
			}
			s.finishTurn(stop)
		}
	case "init":
		var init struct {
			PermissionMode string   `json:"permissionMode"`
			SessionID      string   `json:"session_id"`
			Skills         []string `json:"skills"`
		}
		_ = json.Unmarshal(raw, &init)
		s.learnSkills(ctx, init.Skills)
		if init.SessionID != "" {
			s.adoptID(ctx, init.SessionID)
		}
		s.mu.Lock()
		changed := init.PermissionMode != "" && init.PermissionMode != s.mode
		if changed {
			s.mode = init.PermissionMode
		}
		s.mu.Unlock()
		if changed {
			s.update(ctx, acp.SessionUpdate{CurrentModeUpdate: &acp.SessionCurrentModeUpdate{CurrentModeId: acp.SessionModeId(init.PermissionMode)}})
		}
	case "status":
		var st struct {
			Status         *string `json:"status"`
			PermissionMode string  `json:"permissionMode"`
		}
		_ = json.Unmarshal(raw, &st)
		if st.Status != nil && *st.Status == "compacting" {
			s.update(ctx, acp.SessionUpdate{AgentThoughtChunk: &acp.SessionUpdateAgentThoughtChunk{Content: acp.TextBlock("（正在压缩上下文…）\n")}})
		}
		if st.PermissionMode != "" {
			s.mu.Lock()
			changed := st.PermissionMode != s.mode
			s.mode = st.PermissionMode
			s.mu.Unlock()
			if changed {
				s.update(ctx, acp.SessionUpdate{CurrentModeUpdate: &acp.SessionCurrentModeUpdate{CurrentModeId: acp.SessionModeId(st.PermissionMode)}})
			}
		}
	case "compact_boundary":
		s.update(ctx, acp.SessionUpdate{AgentThoughtChunk: &acp.SessionUpdateAgentThoughtChunk{Content: acp.TextBlock("（上下文已压缩）\n")}})
	}
}

func (s *session) onStreamEvent(ctx context.Context, raw json.RawMessage, apiID string) {
	var ev struct {
		Type         string `json:"type"`
		Index        int    `json:"index"`
		ContentBlock struct {
			Type  string         `json:"type"`
			ID    string         `json:"id"`
			Name  string         `json:"name"`
			Input map[string]any `json:"input"`
		} `json:"content_block"`
		Delta struct {
			Type        string `json:"type"`
			Text        string `json:"text"`
			Thinking    string `json:"thinking"`
			PartialJSON string `json:"partial_json"`
		} `json:"delta"`
		Usage struct {
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
		Message struct {
			Usage struct {
				Input       int `json:"input_tokens"`
				CacheRead   int `json:"cache_read_input_tokens"`
				CacheCreate int `json:"cache_creation_input_tokens"`
			} `json:"usage"`
		} `json:"message"`
	}
	if json.Unmarshal(raw, &ev) != nil {
		return
	}
	// 上下文用量 = 这次请求送进去的输入（含缓存读写）。窗口大小在每轮的 result 里（modelUsage.contextWindow）。
	if ev.Type == "message_start" {
		if n := ev.Message.Usage.Input + ev.Message.Usage.CacheRead + ev.Message.Usage.CacheCreate; n > 0 {
			s.mu.Lock()
			s.ctxTokens = n
			w := s.ctxWindow
			s.mu.Unlock()
			s.update(ctx, acp.SessionUpdate{SessionInfoUpdate: &acp.SessionSessionInfoUpdate{Meta: acpx.ContextProgress(n, w)}})
		}
	}
	// 状态行的 token 数：流式片段按字数估，message_delta 报准数（acpx.TokenMeter）
	switch ev.Type {
	case "content_block_delta":
		s.tok.Chars(apiID, len(ev.Delta.Text)+len(ev.Delta.Thinking)+len(ev.Delta.PartialJSON))
		s.pushTokens(ctx, false)
	case "message_delta":
		if ev.Usage.OutputTokens > 0 {
			s.tok.Exact(apiID, ev.Usage.OutputTokens)
			s.pushTokens(ctx, true)
		}
	}
	switch ev.Type {
	case "content_block_start":
		if ev.ContentBlock.Type == "tool_use" {
			// 工具一开始写参数就先露个头，参数写完（整条 assistant 消息）再补全标题和内容。
			s.mu.Lock()
			_, seen := s.tools[ev.ContentBlock.ID]
			if !seen {
				s.tools[ev.ContentBlock.ID] = &toolState{name: ev.ContentBlock.Name}
			}
			s.mu.Unlock()
			if !seen && !hiddenTool(ev.ContentBlock.Name) {
				ti := describeTool(ev.ContentBlock.Name, nil, s.cwd)
				s.update(ctx, acp.StartToolCall(acp.ToolCallId(ev.ContentBlock.ID), ti.title, acp.WithStartKind(ti.kind), acp.WithStartStatus(acp.ToolCallStatusPending)))
			}
		}
	case "content_block_delta":
		switch ev.Delta.Type {
		case "text_delta":
			if ev.Delta.Text == "" {
				return
			}
			s.markDelta(apiID)
			s.update(ctx, acp.SessionUpdate{AgentMessageChunk: &acp.SessionUpdateAgentMessageChunk{Content: acp.TextBlock(ev.Delta.Text), MessageId: msgID(apiID)}})
		case "thinking_delta":
			if ev.Delta.Thinking == "" {
				return
			}
			s.markDelta(apiID)
			s.update(ctx, acp.SessionUpdate{AgentThoughtChunk: &acp.SessionUpdateAgentThoughtChunk{Content: acp.TextBlock(ev.Delta.Thinking), MessageId: msgID(apiID)}})
		}
	}
}

func msgID(id string) *string {
	if id == "" {
		return nil
	}
	return &id
}

func (s *session) pushTokens(ctx context.Context, force bool) {
	if !force && !s.tok.Due() {
		return
	}
	s.update(ctx, acp.SessionUpdate{SessionInfoUpdate: &acp.SessionSessionInfoUpdate{Meta: acpx.Progress(s.tok.Total())}})
}

func (s *session) markDelta(apiID string) {
	s.mu.Lock()
	if s.msgDelta == nil {
		s.msgDelta = map[string]bool{}
	}
	s.msgDelta[apiID] = true
	s.mu.Unlock()
}

func (s *session) onAssistantBlock(ctx context.Context, apiID, parent string, raw json.RawMessage) {
	var b struct {
		Type     string         `json:"type"`
		Text     string         `json:"text"`
		Thinking string         `json:"thinking"`
		ID       string         `json:"id"`
		Name     string         `json:"name"`
		Input    map[string]any `json:"input"`
	}
	if json.Unmarshal(raw, &b) != nil {
		return
	}
	switch b.Type {
	case "text", "thinking":
		if parent != "" {
			// 子 agent 说的话：不进主对话，带上 subagent 标记给子 agent 面板（它的逐字输出没推，这里是整段）。
			meta := acpx.Meta(map[string]any{"subagent": parent})
			if b.Type == "text" && b.Text != "" {
				s.update(ctx, acp.SessionUpdate{AgentMessageChunk: &acp.SessionUpdateAgentMessageChunk{Content: acp.TextBlock(b.Text), MessageId: msgID(apiID), Meta: meta}})
			}
			if b.Type == "thinking" && b.Thinking != "" {
				s.update(ctx, acp.SessionUpdate{AgentThoughtChunk: &acp.SessionUpdateAgentThoughtChunk{Content: acp.TextBlock(b.Thinking), MessageId: msgID(apiID), Meta: meta}})
			}
			return
		}
		s.mu.Lock()
		streamed := s.msgDelta[apiID]
		s.mu.Unlock()
		if streamed {
			return
		}
		if b.Type == "text" && b.Text != "" {
			s.update(ctx, acp.SessionUpdate{AgentMessageChunk: &acp.SessionUpdateAgentMessageChunk{Content: acp.TextBlock(b.Text), MessageId: msgID(apiID)}})
		}
		if b.Type == "thinking" && b.Thinking != "" {
			s.update(ctx, acp.SessionUpdate{AgentThoughtChunk: &acp.SessionUpdateAgentThoughtChunk{Content: acp.TextBlock(b.Thinking), MessageId: msgID(apiID)}})
		}
	case "tool_use":
		s.mu.Lock()
		st, seen := s.tools[b.ID]
		if !seen {
			st = &toolState{name: b.Name}
			s.tools[b.ID] = st
		}
		st.input, st.parent = b.Input, parent
		cwd := s.cwd
		s.mu.Unlock()
		if b.Name == "TodoWrite" {
			if entries := todoPlan(b.Input); entries != nil {
				s.update(ctx, acp.UpdatePlan(entries...))
			}
			return
		}
		if b.Name == "SubagentHandback" && parent != "" {
			// 子 agent 的最终汇报：给它的只读面板当最后一条消息。输入的字段名没有文档，取第一段像正文的字符串。
			for _, k := range []string{"report", "message", "text", "summary", "content", "result"} {
				if v, ok := b.Input[k].(string); ok && strings.TrimSpace(v) != "" {
					s.update(ctx, acp.SessionUpdate{AgentMessageChunk: &acp.SessionUpdateAgentMessageChunk{Content: acp.TextBlock(v), Meta: acpx.Meta(map[string]any{"subagent": parent})}})
					break
				}
			}
		}
		if hiddenTool(b.Name) {
			return
		}
		ti := describeTool(b.Name, b.Input, cwd)
		var meta map[string]any
		if parent != "" {
			meta = acpx.Meta(map[string]any{"parentToolCallId": parent})
		}
		if !seen {
			u := acp.StartToolCall(acp.ToolCallId(b.ID), ti.title, acp.WithStartKind(ti.kind), acp.WithStartStatus(acp.ToolCallStatusInProgress),
				acp.WithStartRawInput(b.Input), acp.WithStartLocations(ti.locations), acp.WithStartContent(ti.content))
			u.ToolCall.Meta = meta
			s.update(ctx, u)
		} else {
			u := acp.UpdateToolCall(acp.ToolCallId(b.ID), acp.WithUpdateTitle(ti.title), acp.WithUpdateKind(ti.kind), acp.WithUpdateStatus(acp.ToolCallStatusInProgress),
				acp.WithUpdateRawInput(b.Input), acp.WithUpdateLocations(ti.locations), acp.WithUpdateContent(ti.content))
			u.ToolCallUpdate.Meta = meta
			s.update(ctx, u)
		}
	}
}

func (s *session) onToolResult(ctx context.Context, raw json.RawMessage, structured json.RawMessage) {
	var b struct {
		Type      string          `json:"type"`
		ToolUseID string          `json:"tool_use_id"`
		Content   json.RawMessage `json:"content"`
		IsError   bool            `json:"is_error"`
	}
	if json.Unmarshal(raw, &b) != nil || b.Type != "tool_result" {
		return
	}
	s.mu.Lock()
	st := s.tools[b.ToolUseID]
	s.mu.Unlock()
	if st == nil || hiddenTool(st.name) || st.name == "TodoWrite" {
		return
	}
	status := acp.ToolCallStatusCompleted
	if b.IsError {
		status = acp.ToolCallStatusFailed
	}
	text := resultText(b.Content)
	if isAgentTool(st.name) {
		text = subReport(text)
		// 后台子 agent 的工具结果是 CLI 写给模型看的一大段内部说明（agentId、「别告诉用户」……），人不用看。
		if strings.HasPrefix(text, "Async agent launched") {
			text = "已在后台启动，跑完会自动通知。"
		}
	}
	opts := []acp.ToolCallUpdateOpt{acp.WithUpdateStatus(status)}
	// 改文件的工具：正文里那句「File created successfully」没信息量，留着调用时的 diff 就好。
	if !(isEditTool(st.name) && !b.IsError) && text != "" {
		opts = append(opts, acp.WithUpdateContent([]acp.ToolCallContent{acp.ToolContent(acp.TextBlock(clip(text, 64<<10)))}))
	}
	if len(structured) > 0 && string(structured) != "null" && len(structured) < 256<<10 {
		var v any
		if json.Unmarshal(structured, &v) == nil {
			opts = append(opts, acp.WithUpdateRawOutput(v))
		}
	}
	u := acp.UpdateToolCall(acp.ToolCallId(b.ToolUseID), opts...)
	if b.IsError && strings.Contains(text, classifierDenied) {
		if rules := allowRules(st.name, st.input); len(rules) > 0 {
			u.ToolCallUpdate.Meta = acpx.Meta(map[string]any{acpx.AutoDeniedKey: rules})
		}
	}
	s.update(ctx, u)
	// 前台子 agent 最后那句话不单独发消息，只在 Agent 工具的结果里 —— 给子 agent 面板补成它的最后一条。
	// （后台子 agent 的结果是「Async agent launched」，它的话等 task_notification 的 summary。）
	if isAgentTool(st.name) && !b.IsError && text != "" && !s.backgroundTask(b.ToolUseID) {
		s.update(ctx, acp.SessionUpdate{AgentMessageChunk: &acp.SessionUpdateAgentMessageChunk{Content: acp.TextBlock(text), Meta: acpx.Meta(map[string]any{"subagent": b.ToolUseID})}})
	}
}

func isAgentTool(name string) bool { return name == "Agent" || name == "Task" }

// subReport 把 Agent 工具的结果剥成子 agent 自己的汇报。CLI（2.1.x）给模型看的是：
//
//	<tools-used-before-summary>…</tools-used-before-summary>
//	[Subagent hand-back] …（一段防注入的说明）… The report follows:
//	  <汇报，每行缩进两格>
//	agentId: … (use SendMessage …)
//	<usage>…</usage>
//
// 认不出这个格式就原样返回（换了版本也不至于丢内容）。
func subReport(t string) string {
	const mark = "The report follows:\n"
	i := strings.Index(t, mark)
	if i < 0 {
		return t
	}
	body := t[i+len(mark):]
	if j := strings.Index(body, "\nagentId: "); j >= 0 {
		body = body[:j]
	} else if j := strings.Index(body, "\n<usage>"); j >= 0 {
		body = body[:j]
	}
	lines := strings.Split(body, "\n")
	for k, l := range lines {
		lines[k] = strings.TrimPrefix(l, "  ")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func resultText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) == nil {
		var b strings.Builder
		for _, p := range parts {
			if p.Type == "text" {
				b.WriteString(p.Text)
			}
		}
		return b.String()
	}
	return ""
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n…"
}

// ---- 它问我们的：can_use_tool ----

var cancelMu sync.Mutex
var cancels = map[string]context.CancelFunc{}

func (s *session) cancelControl(reqID string) {
	cancelMu.Lock()
	c := cancels[reqID]
	cancelMu.Unlock()
	if c != nil {
		c()
	}
}

func (s *session) onControlRequest(pr *proc, reqID string, raw json.RawMessage) {
	var req struct {
		Subtype     string            `json:"subtype"`
		ToolName    string            `json:"tool_name"`
		Input       map[string]any    `json:"input"`
		ToolUseID   string            `json:"tool_use_id"`
		Suggestions []json.RawMessage `json:"permission_suggestions"`
		Title       string            `json:"title"`
		Description string            `json:"description"`
		Reason      string            `json:"decision_reason"`
	}
	_ = json.Unmarshal(raw, &req)
	if req.Subtype != "can_use_tool" {
		pr.respond(reqID, nil, "gerdr does not handle "+req.Subtype)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancelMu.Lock()
	cancels[reqID] = cancel
	cancelMu.Unlock()
	defer func() {
		cancel()
		cancelMu.Lock()
		delete(cancels, reqID)
		cancelMu.Unlock()
	}()
	s.mu.Lock()
	s.openInputs++
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.openInputs--
		s.mu.Unlock()
	}()

	deny := func(msg string) {
		pr.respond(reqID, map[string]any{"behavior": "deny", "message": msg, "toolUseID": req.ToolUseID}, "")
	}
	allow := func(input map[string]any, perms []json.RawMessage) {
		resp := map[string]any{"behavior": "allow", "updatedInput": input, "toolUseID": req.ToolUseID}
		if len(perms) > 0 {
			resp["updatedPermissions"] = perms
		}
		pr.respond(reqID, resp, "")
	}

	// gerdr 自己的 session / ui 命令（多个会话协作）：直接放行，见 sysenv.SafeGerdrCLI。
	if req.ToolName == "Bash" {
		if c, _ := req.Input["command"].(string); sysenv.SafeGerdrCLI(c) {
			allow(req.Input, nil)
			return
		}
	}
	switch req.ToolName {
	case "AskUserQuestion":
		qs := askQuestions(req.Input)
		if len(qs) == 0 {
			deny("AskUserQuestion called with no valid questions.")
			return
		}
		raw, err := s.a.conn.CallExtension(ctx, acpx.MethodAsk, acpx.AskRequest{SessionId: acp.SessionId(s.currentID()), ToolCallId: req.ToolUseID, Questions: qs})
		var res acpx.AskResponse
		if err == nil {
			err = json.Unmarshal(raw, &res)
		}
		if err != nil || res.Cancelled {
			deny(acpx.AskDeclined(res.Reason, res.Text))
			return
		}
		answers := map[string]any{}
		for _, q := range qs {
			if v := res.Answers[q.Id]; len(v) > 0 {
				answers[q.Question] = strings.Join(v, ", ")
			}
		}
		input := map[string]any{}
		for k, v := range req.Input {
			input[k] = v
		}
		input["answers"] = answers
		allow(input, nil)
		return
	case "ExitPlanMode":
		plan, _ := req.Input["plan"].(string)
		opt, err := s.askPermission(ctx, req.ToolUseID, "Ready to code?", acp.ToolKindSwitchMode, req.Input, plan, []acp.PermissionOption{
			{OptionId: "acceptEdits", Name: "开始做，改文件不用问", Kind: acp.PermissionOptionKindAllowAlways},
			{OptionId: "default", Name: "开始做，改文件前问我", Kind: acp.PermissionOptionKindAllowOnce},
			{OptionId: "plan", Name: "接着规划", Kind: acp.PermissionOptionKindRejectOnce},
		})
		if err != nil || opt == "plan" || opt == "" {
			deny("The user wants to keep planning. Ask what to change in the plan.")
			return
		}
		setMode, _ := json.Marshal(map[string]any{"type": "setMode", "mode": opt, "destination": "session"})
		allow(req.Input, []json.RawMessage{setMode})
		s.mu.Lock()
		s.mode = opt
		s.mu.Unlock()
		s.update(context.Background(), acp.SessionUpdate{CurrentModeUpdate: &acp.SessionCurrentModeUpdate{CurrentModeId: acp.SessionModeId(opt)}})
		return
	}

	s.mu.Lock()
	cwd := s.cwd
	s.mu.Unlock()
	ti := describeTool(req.ToolName, req.Input, cwd)
	title := ti.title
	if req.Title != "" {
		title = req.Title
	}
	opts := []acp.PermissionOption{{OptionId: "allow_once", Name: "允许", Kind: acp.PermissionOptionKindAllowOnce}}
	if len(req.Suggestions) > 0 {
		opts = append(opts, acp.PermissionOption{OptionId: "allow_always", Name: "允许，以后不再问", Kind: acp.PermissionOptionKindAllowAlways})
	}
	opts = append(opts, acp.PermissionOption{OptionId: "reject_once", Name: "拒绝", Kind: acp.PermissionOptionKindRejectOnce})
	detail := req.Description
	if detail == "" {
		detail = req.Reason
	}
	opt, err := s.askPermission(ctx, req.ToolUseID, title, ti.kind, req.Input, detail, opts)
	switch {
	case err != nil:
		deny("The user cancelled this action.")
	case opt == "allow_once":
		allow(req.Input, nil)
	case opt == "allow_always":
		allow(req.Input, req.Suggestions)
	default:
		deny("The user rejected this action. Do not retry it; ask the user how to proceed.")
	}
}

func (s *session) currentID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.id
}

func (s *session) askPermission(ctx context.Context, toolID, title string, kind acp.ToolKind, input map[string]any, detail string, opts []acp.PermissionOption) (string, error) {
	tc := acp.ToolCallUpdate{ToolCallId: acp.ToolCallId(toolID), Title: acp.Ptr(title), Kind: acp.Ptr(kind), Status: acp.Ptr(acp.ToolCallStatusPending), RawInput: input}
	if detail != "" {
		tc.Meta = acpx.Meta(map[string]any{"detail": detail})
	}
	res, err := s.a.conn.RequestPermission(ctx, acp.RequestPermissionRequest{SessionId: acp.SessionId(s.currentID()), ToolCall: tc, Options: opts})
	if err != nil {
		return "", err
	}
	if res.Outcome.Selected == nil {
		return "", context.Canceled
	}
	return string(res.Outcome.Selected.OptionId), nil
}

func askQuestions(input map[string]any) []acpx.AskQuestion {
	arr, _ := input["questions"].([]any)
	var out []acpx.AskQuestion
	for i, q := range arr {
		m, ok := q.(map[string]any)
		if !ok {
			continue
		}
		text, _ := m["question"].(string)
		if text == "" {
			continue
		}
		aq := acpx.AskQuestion{Id: "q" + strconv.Itoa(i), Question: text, AllowOther: true}
		aq.Header, _ = m["header"].(string)
		aq.MultiSelect, _ = m["multiSelect"].(bool)
		opts, _ := m["options"].([]any)
		for _, o := range opts {
			om, _ := o.(map[string]any)
			label, _ := om["label"].(string)
			desc, _ := om["description"].(string)
			if label != "" {
				aq.Options = append(aq.Options, acpx.AskOption{Label: label, Description: desc})
			}
		}
		out = append(out, aq)
	}
	return out
}

// learnSkills：init 里报的 skill 有 initialize 没标出来的（自带的 /code-review 这种）—— 记下来，重推一遍命令列表
func (s *session) learnSkills(ctx context.Context, names []string) {
	s.mu.Lock()
	list := s.cmds
	s.mu.Unlock()
	s.a.mu.Lock()
	changed := false
	for _, n := range names {
		if !s.a.skills[n] {
			if s.a.skills == nil {
				s.a.skills = map[string]bool{}
			}
			s.a.skills[n] = true
			changed = true
		}
	}
	var cmds []acp.AvailableCommand
	if changed && len(list) > 0 {
		cmds = s.a.acpCommands(list)
	}
	s.a.mu.Unlock()
	if cmds != nil {
		s.update(ctx, acp.SessionUpdate{AvailableCommandsUpdate: &acp.SessionAvailableCommandsUpdate{AvailableCommands: cmds}})
	}
}
