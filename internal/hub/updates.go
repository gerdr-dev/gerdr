package hub

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/gerdr-dev/gerdr/internal/i18n"

	"github.com/coder/acp-go-sdk"

	"github.com/gerdr-dev/gerdr/acpx"
	"github.com/gerdr-dev/gerdr/chatlog"
	"github.com/gerdr-dev/gerdr/internal/agenthost"
)

// ---- agent 推来的 ----

func (s *Session) onUpdate(u acp.SessionUpdate) {
	raw, err := json.Marshal(u)
	if err != nil {
		return
	}
	switch {
	case u.CurrentModeUpdate != nil:
		s.mu.Lock()
		if s.modes != nil {
			s.modes.CurrentModeId = u.CurrentModeUpdate.CurrentModeId
		}
		s.mu.Unlock()
		s.syncRecFromState()
		s.h.broadcastSession(s)
	case u.AvailableCommandsUpdate != nil:
		s.mu.Lock()
		s.commands = u.AvailableCommandsUpdate.AvailableCommands
		s.mu.Unlock()
		s.h.cmds.set(s.rec.Agent, u.AvailableCommandsUpdate.AvailableCommands)
		s.h.broadcastSession(s)
		return // 不进日志：几十上百条命令，每次起进程都推一遍
	case u.ConfigOptionUpdate != nil:
		s.mu.Lock()
		s.config = u.ConfigOptionUpdate.ConfigOptions
		s.h.opts.set(s.rec.Agent, s.modes, s.config)
		s.mu.Unlock()
		s.syncRecFromState()
		s.h.broadcastSession(s)
		return
	case u.SessionInfoUpdate != nil:
		if g, ok := u.SessionInfoUpdate.Meta["gerdr"].(map[string]any); ok {
			s.onGerdrInfo(g)
		}
		if g, ok := u.SessionInfoUpdate.Meta["gerdr"].(map[string]any); ok {
			if ev := chatlog.NoticeFromInfo(g, now()); ev != nil {
				s.emit(ev)
			}
			// 插话排队 / 被读到：只记用户发过的那几条（command_lifecycle 对每条写进去的消息都报）
			if id, state, ok := chatlog.SteerFromInfo(g); ok {
				s.mu.Lock()
				_, early := s.steering[id]
				if early {
					s.steering[id] = state // 消息还没记进日志：先存着，记完再补（started 会盖掉 queued）
				}
				s.mu.Unlock()
				if !early && s.isSteer(id) {
					s.emit(&Event{Kind: EvSteerState, MessageID: id, Text: state})
				}
			}
			// agent 那边换了会话 id（claude 的 /clear）：之后回退、恢复都按新的来；对话里留一条分隔线
			if id, ok := g["sessionId"].(string); ok && id != "" {
				s.mu.Lock()
				prev := s.rec
				s.mu.Unlock()
				s.h.rekey(s, id)
				if c, _ := g["cleared"].(bool); c {
					if prev.AgentSessionID != "" && prev.AgentSessionID != id {
						s.h.splitOffCleared(s, prev)
					}
					s.mu.Lock()
					// The title belongs to what was cleared away, renamed or not (a /rename names that piece of work, not
					// the tab): drop it right away rather than leave the old name on an empty context until the next
					// message (two entries with the same name in the session list). Auto-naming starts over (title.go).
					s.rec.Title, s.rec.TitleStale, s.rec.CustomTitle = "", false, false
					s.rec.TitleKey, s.rec.PluginTitleFor = "", "" // rekey carried them over; a clear starts naming afresh
					// The usage was the cleared context's: hide it until the next request reports the new one (the
					// window stays, it is the model's), instead of showing the old percentage over an empty conversation.
					s.rec.CtxTokens = 0
					s.mu.Unlock()
					s.h.broadcastSession(s)
					s.emit(&Event{Kind: EvNotice, Level: "clear", Text: i18n.S("上下文已清空", "Context cleared")})
				}
			}
		}
		if t := u.SessionInfoUpdate.Title; t != nil && *t != "" {
			// agent 里人主动改的名（claude 的 /rename）：和在 gerdr 里改名一样，覆盖并且以后不再自动改。
			renamed := false
			if g, ok := u.SessionInfoUpdate.Meta["gerdr"].(map[string]any); ok {
				renamed, _ = g["renamed"].(bool)
			}
			s.mu.Lock()
			custom := s.rec.CustomTitle
			if renamed {
				s.rec.Title, s.rec.CustomTitle = *t, true
				custom = false
			} else if !custom {
				s.rec.Title = *t
			}
			s.mu.Unlock()
			if !custom {
				s.h.broadcastSession(s)
				s.h.saveIndex()
			}
		}
		return
	case u.UsageUpdate != nil:
		return
	}
	s.emit(&Event{Kind: EvUpdate, Update: raw})
}

// onGerdrInfo：适配器经 session_info_update._meta.gerdr 报的后台任务和「自己开的一轮」。
func (s *Session) onGerdrInfo(g map[string]any) {
	if p, ok := g[acpx.ProgressKey].(map[string]any); ok {
		s.mu.Lock()
		if n, ok := p["outputTokens"].(float64); ok {
			s.turnTokens = int(n)
		}
		if n, ok := p["contextTokens"].(float64); ok && n > 0 {
			s.rec.CtxTokens = int(n)
		}
		if n, ok := p["contextWindow"].(float64); ok && n > 0 {
			s.rec.CtxWindow = int(n)
		}
		s.mu.Unlock()
		s.h.broadcastSession(s)
	}
	if c, ok := g[acpx.CostKey].(map[string]any); ok {
		if usd, ok := c["usd"].(float64); ok && usd > 0 {
			s.mu.Lock()
			s.turnCost += usd
			s.mu.Unlock()
		}
	}
	if v, ok := g[acpx.TokensKey]; ok {
		var t acpx.Tokens
		if b, err := json.Marshal(v); err == nil && json.Unmarshal(b, &t) == nil && (t.Input > 0 || t.Output > 0) {
			s.mu.Lock()
			s.turnUse = addTokens(s.turnUse, t)
			s.mu.Unlock()
		}
	}
	if list, ok := chatlog.TasksFromInfo(g); ok {
		s.mu.Lock()
		prev := s.tasks
		s.tasks = list
		s.mu.Unlock()
		s.h.broadcastSession(s)
		// 刚结束的（之前在跑、或者一出现就已经结束）：记进对话。子 agent 自己起的不记，在它的面板里看。
		for _, ev := range chatlog.FinishedTasks(prev, list) {
			s.emit(ev)
		}
	}
	if d, ok := g[acpx.WorkdirKey].(string); ok && d != "" {
		s.mu.Lock()
		if d == s.rec.Cwd {
			d = ""
		}
		changed := d != s.rec.Workdir
		s.rec.Workdir = d
		s.mu.Unlock()
		if changed {
			s.h.broadcastSession(s)
			s.h.saveIndex()
		}
	}
	if v, ok := g[acpx.AutoTurnKey].(bool); ok {
		var wake json.RawMessage
		if w, ok := g[acpx.AutoTurnWakeKey]; ok {
			wake, _ = json.Marshal(w)
		}
		s.mu.Lock()
		start := v && !s.running
		if v && s.running && !s.auto {
			s.autoNext, s.autoWake = true, wake
		}
		if !v {
			s.autoNext, s.autoWake = false, nil
		}
		end := !v && s.auto
		if start {
			s.running, s.auto, s.lastErr = true, true, ""
			s.turnStarted, s.turnTokens = now(), 0
			s.quiet = false
		}
		var held *deferredMsg
		if end {
			s.running, s.auto = false, false
			for id, p := range s.pending {
				close(p.ch)
				delete(s.pending, id)
			}
			held = s.popDeferredLocked()
		}
		s.mu.Unlock()
		if start {
			s.emit(&Event{Kind: EvTurnStart, Auto: true, Wake: wake})
			s.setStatus()
		}
		if end {
			s.mu.Lock()
			dur, tok := now()-s.turnStarted, s.turnTokens
			s.mu.Unlock()
			s.emit(&Event{Kind: EvTurnEnd, StopReason: string(acp.StopReasonEndTurn), Auto: true, DurationMs: dur, OutputTokens: tok})
			s.flush(true)
			s.setStatus()
			if held != nil {
				go s.sendDeferred(held)
			}
		}
	}
}

// StopTask / BackgroundTool：后台任务面板上的「停止」、运行中工具上的「转到后台」。
func (s *Session) StopTask(ctx context.Context, taskID string) error {
	c, err := s.ensureAttached(ctx)
	if err != nil {
		return err
	}
	_, err = agenthost.Call[map[string]any](ctx, c, acpx.MethodTaskStop, acpx.TaskStopRequest{SessionId: acp.SessionId(s.agentSID()), TaskId: taskID})
	return err
}

func (s *Session) BackgroundTool(ctx context.Context, toolCallID string) (bool, error) {
	c, err := s.ensureAttached(ctx)
	if err != nil {
		return false, err
	}
	r, err := agenthost.Call[acpx.TaskBackgroundResponse](ctx, c, acpx.MethodTaskBackground, acpx.TaskBackgroundRequest{SessionId: acp.SessionId(s.agentSID()), ToolCallId: toolCallID})
	return r.Backgrounded, err
}

// AllowRules：工具调用上的「允许并重试」—— 把规则交给 agent 加进它的允许名单（重试那句话前端自己发）。
func (s *Session) AllowRules(ctx context.Context, rules []string) error {
	c, err := s.ensureAttached(ctx)
	if err != nil {
		return err
	}
	_, err = agenthost.Call[map[string]any](ctx, c, acpx.MethodAllowRules, acpx.AllowRulesRequest{SessionId: acp.SessionId(s.agentSID()), Rules: rules})
	return err
}

func (s *Session) agentSID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rec.AgentSessionID
}

func (s *Session) askPermission(ctx context.Context, req acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	reqID := newID()
	raw, _ := json.Marshal(req)
	p := &pending{kind: "permission", ch: make(chan json.RawMessage, 1), req: raw, at: now()}
	s.mu.Lock()
	s.pending[reqID] = p
	s.mu.Unlock()
	s.emit(&Event{Kind: EvPermission, ReqID: reqID, Request: raw})
	s.setStatus()
	cancel := func() (acp.RequestPermissionResponse, error) {
		s.resolve(reqID, EvPermissionDone, json.RawMessage(`{"cancelled":true}`))
		return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeCancelled()}, nil
	}
	select {
	case ans, ok := <-p.ch:
		if !ok {
			return cancel()
		}
		var a struct {
			OptionID  string `json:"optionId"`
			Cancelled bool   `json:"cancelled"`
		}
		_ = json.Unmarshal(ans, &a)
		if a.Cancelled || a.OptionID == "" {
			return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeCancelled()}, nil
		}
		return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeSelected(acp.PermissionOptionId(a.OptionID))}, nil
	case <-ctx.Done():
		return cancel()
	}
}

func (s *Session) ask(ctx context.Context, req acpx.AskRequest) (acpx.AskResponse, error) {
	reqID := newID()
	raw, _ := json.Marshal(req)
	p := &pending{kind: "ask", ch: make(chan json.RawMessage, 1), req: raw, at: now()}
	s.mu.Lock()
	s.pending[reqID] = p
	s.mu.Unlock()
	s.emit(&Event{Kind: EvAsk, ReqID: reqID, Request: raw})
	s.setStatus()
	select {
	case ans, ok := <-p.ch:
		if !ok {
			s.resolve(reqID, EvAskDone, json.RawMessage(`{"cancelled":true}`))
			return acpx.AskResponse{Cancelled: true}, nil
		}
		var res acpx.AskResponse
		_ = json.Unmarshal(ans, &res)
		return res, nil
	case <-ctx.Done():
		s.resolve(reqID, EvAskDone, json.RawMessage(`{"cancelled":true}`))
		return acpx.AskResponse{Cancelled: true}, nil
	}
}

// resolve 记一条「已回答 / 已作废」并把它从等待里拿掉（不往等待的那头送东西）。
func (s *Session) resolve(reqID, kind string, resp json.RawMessage) {
	s.mu.Lock()
	_, had := s.pending[reqID]
	delete(s.pending, reqID)
	answered := false
	s.loadLocked()
	for _, e := range s.events {
		if (e.Kind == EvPermissionDone || e.Kind == EvAskDone) && e.ReqID == reqID {
			answered = true
		}
	}
	s.mu.Unlock()
	if !answered {
		s.emit(&Event{Kind: kind, ReqID: reqID, Response: resp})
	}
	if had {
		s.setStatus()
	}
}

// Answer 回答一个审批（optionId）或提问（answers）。
func (s *Session) Answer(reqID string, resp json.RawMessage) error {
	s.mu.Lock()
	p := s.pending[reqID]
	if p != nil {
		delete(s.pending, reqID)
	}
	s.mu.Unlock()
	if p == nil {
		return errors.New(i18n.S("这个请求已经过期了", "This request has expired"))
	}
	kind := EvPermissionDone
	if p.kind == "ask" {
		kind = EvAskDone
	}
	s.emit(&Event{Kind: kind, ReqID: reqID, Response: resp})
	p.ch <- resp
	s.setStatus()
	return nil
}
