package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/coder/acp-go-sdk"
	"github.com/google/uuid"

	"github.com/gerdr-dev/gerdr/acpx"
	"github.com/gerdr-dev/gerdr/chatlog"
	"github.com/gerdr-dev/gerdr/internal/agenthost"
)

// ---- 说话 ----

type SendResult struct {
	MessageID string `json:"messageId"`
	Steered   bool   `json:"steered"`
}

// Send 发一条消息：有一轮在跑就插进去（steer），没有就开一轮。这正是 TUI 那边做不好的事：
// 人不用管 agent 此刻忙不忙，说就是了。
// Send：clientID 是 client 自己生成的消息 id（UUID）。网络不好时 client 会拿同一个 id 重发 ——
// 第一次其实已经到了、只是响应丢了 —— 这里认出来直接回上次的结果，不会发出两条。
func (s *Session) Send(ctx context.Context, content []acp.ContentBlock, clientID string) (SendResult, error) {
	return s.SendFrom(ctx, content, clientID, "", false)
}

// SendFrom：from 是发送方的会话 id（另一个 agent 经 `gerdr session send` 发来的），记在日志里给界面标出来。
// urgent：在跑时立即插话（截断当前这次生成），见 acpx.SteerRequest.Now；不在跑时没区别。
func (s *Session) SendFrom(ctx context.Context, content []acp.ContentBlock, clientID, from string, urgent bool) (SendResult, error) {
	// Nothing typed is still a message when `!` output is waiting to go along (shell.go): sending it on its own is how
	// the user hands the agent a command's result without having to say something
	if len(content) == 0 {
		s.mu.Lock()
		n := len(s.pendingShellLocked())
		s.mu.Unlock()
		if n == 0 {
			return SendResult{}, errors.New("empty message")
		}
	}
	msgID := uuid.NewString()
	if _, err := uuid.Parse(clientID); err == nil {
		msgID = clientID
		s.mu.Lock()
		if s.sending[msgID] {
			s.mu.Unlock()
			return SendResult{}, errors.New("这条消息正在发送")
		}
		s.loadLocked()
		for _, e := range s.events {
			if e.Kind == EvUser && e.MessageID == msgID {
				s.mu.Unlock()
				return SendResult{MessageID: msgID, Steered: e.Steer}, nil
			}
		}
		if s.sending == nil {
			s.sending = map[string]bool{}
		}
		s.sending[msgID] = true
		s.mu.Unlock()
		defer func() {
			s.mu.Lock()
			delete(s.sending, msgID)
			s.mu.Unlock()
		}()
	}
	c, err := s.ensureAttached(ctx)
	if err != nil {
		// codex：别处（终端 / Codex App）开着这个 thread 时接不回来 —— 它自己防住了分叉，但光秃秃一句英文报错人不知道怎么办
		if strings.Contains(err.Error(), "active writer") {
			return SendResult{}, &HeldError{Holders: []acpx.Holder{{Name: "另一个 Codex（终端或 Codex App）"}}}
		}
		return SendResult{}, err
	}
	// 别处（herdr / 终端里的交互式 claude）还开着这个会话：先不发，让人挑（另起一份 / 关掉那边）。
	// 在记下这条消息之前拦 —— 处理完 client 拿同一个 id 重发，对话里不会多出一条。
	if hs := s.heldElsewhere(ctx, c); len(hs) > 0 {
		return SendResult{}, &HeldError{Holders: hs}
	}
	s.mu.Lock()
	running := s.running
	agentSID := s.rec.AgentSessionID
	var asks []string
	for id, p := range s.pending {
		if p.kind == "ask" {
			asks = append(asks, id)
		}
	}
	s.mu.Unlock()
	// A slash command mid-turn that the agent can't take as a steer: hold it until the turn ends (deferred.go).
	// Before answering a pending question with it and before the `!` output goes in front: both happen when it's sent.
	if running && s.holdsCommand(c, content) {
		raw := make([]json.RawMessage, 0, len(content))
		for _, b := range content {
			j, _ := json.Marshal(b)
			raw = append(raw, j)
		}
		if s.deferCommand(msgID, from, content, raw) {
			return SendResult{MessageID: msgID, Steered: true}, nil
		}
		s.mu.Lock()
		running = s.running
		s.mu.Unlock()
	}
	// 提问还挂着，人却直接打字说话了：照 Claude Code，这句话就是回答 —— 先把提问收掉（告诉 agent「他直接在对话里回了」），
	// 再把话插进去。不收的话 agent 一直卡在等提问，插进去的话要等它超时 / 被关掉才轮得到。
	if len(asks) > 0 {
		var said []string
		for _, b := range content {
			if b.Text != nil {
				said = append(said, b.Text.Text)
			}
		}
		resp, _ := json.Marshal(map[string]any{"cancelled": true, "reason": "replied", "text": strings.Join(said, "\n")})
		for _, id := range asks {
			_ = s.Answer(id, resp)
		}
	}

	// 人在这条之前用 `!` 跑过的命令：输出接在最前面一起发过去（shell.go）。日志里记的也是发出去的原样 ——
	// agent 的记录里就是这样，回头比对（CatchUp）、重新载入才对得上；界面认出这几块，不画成人说的话
	// Not in front of a slash command: agents only read a command from the first block, so `!` output ahead of a
	// /compact turned it into plain text the model answered, and nothing was compacted (user report). The output
	// stays pending and goes with the next real message (pendingShellLocked skips command messages).
	if isCmd, _ := s.agentCommand(content); !isCmd {
		s.mu.Lock()
		content = withShell(s.pendingShellLocked(), content)
		s.mu.Unlock()
	}
	// First message after taking over another agent's conversation: the earlier conversation goes in front (handoff.go)
	content = withShell(s.pendingHandoff(), content)
	raw := make([]json.RawMessage, 0, len(content))
	for _, b := range content {
		j, _ := json.Marshal(b)
		raw = append(raw, j)
	}

	if running && c.Steering() {
		// 插话的「排队中」状态（command_lifecycle → steer_state）常常比 MethodSteer 的返回先到 —— 那时这条消息还没记进日志。
		// 先登记，早到的状态暂存在这儿，记日志之后补发（见下面和 onGerdrInfo）
		s.mu.Lock()
		s.steering[msgID] = ""
		s.mu.Unlock()
		defer func() {
			s.mu.Lock()
			delete(s.steering, msgID)
			s.mu.Unlock()
		}()
		// promptRequired 有两种可能：这一轮刚好结束了（等 running 落下来，按新一轮发），或者
		// 这一轮还没在 agent 那边真正开始（比如 claude 进程还在启动）——那就隔一会儿再插一次。
		for i := 0; i < 100; i++ {
			res, err := agenthost.Call[acpx.SteerResponse](ctx, c, acpx.MethodSteer, acpx.SteerRequest{SessionId: acp.SessionId(agentSID), Prompt: content, MessageId: msgID, Now: urgent})
			if err != nil {
				return SendResult{}, err
			}
			if res.Outcome == acpx.SteerInjected {
				s.emit(&Event{Kind: EvUser, MessageID: msgID, Content: raw, Steer: true, Now: urgent, From: from})
				s.mu.Lock()
				early := s.steering[msgID]
				delete(s.steering, msgID)
				s.mu.Unlock()
				if early != "" {
					s.emit(&Event{Kind: EvSteerState, MessageID: msgID, Text: early})
				}
				s.touchTitle(content)
				go s.autoTitle()
				return SendResult{MessageID: msgID, Steered: true}, nil
			}
			if res.Outcome == acpx.SteerHold {
				// This turn can't take a steer (Codex's compact turn): hold it like a mid-turn command and send it
				// once the turn ends. It used to come back as an error and the message was gone (user report).
				if s.deferCommand(msgID, from, content, raw) {
					return SendResult{MessageID: msgID, Steered: true}, nil
				}
				break // the turn ended meanwhile: send it as a new one
			}
			time.Sleep(150 * time.Millisecond)
			s.mu.Lock()
			r := s.running
			s.mu.Unlock()
			if !r {
				break
			}
		}
	} else if running {
		return SendResult{}, errors.New("这个 agent 不支持中途插话，等这一轮结束再发")
	}

	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return SendResult{}, errors.New("这一轮还在跑")
	}
	s.running = true
	s.lastErr = ""
	s.turnMsg = msgID
	s.turnStarted, s.turnTokens = now(), 0
	s.mu.Unlock()

	s.emit(&Event{Kind: EvUser, MessageID: msgID, Content: raw, From: from})
	s.emit(&Event{Kind: EvTurnStart, MessageID: msgID})
	s.touchTitle(content)
	s.setStatus()
	// Name it as soon as the prompt is in, not only when the turn ends: a first turn can run for many minutes, and
	// until then the tab showed the raw first message (users read that as auto-rename not working)
	go s.autoTitle()

	go s.runTurn(c, agentSID, msgID, content)
	return SendResult{MessageID: msgID}, nil
}

var retryWaits = chatlog.RetryWaits

func retryable(msg string) bool { return chatlog.Retryable(msg) }

func (s *Session) runTurn(c *agenthost.Host, agentSID, msgID string, content []acp.ContentBlock) {
	res, err := c.Conn.Prompt(context.Background(), acp.PromptRequest{SessionId: acp.SessionId(agentSID), MessageId: &msgID, Prompt: content})
	// 网络出错：等一会儿让 agent 从断的地方接着做（发一句「继续」，不进对话记录 —— 界面上是一行提示）。
	for attempt := 0; err != nil && attempt < len(retryWaits) && retryable(errText(err)) && !s.isShut(); attempt++ {
		wait := retryWaits[attempt]
		s.emit(&Event{Kind: EvNotice, Level: "retry", Until: now() + wait.Milliseconds(),
			Text: fmt.Sprintf("出错了：%s。%d 秒后自动重试（第 %d/%d 次），按停止可以取消。", oneLine(errText(err), 200), int(wait.Seconds()), attempt+1, len(retryWaits))})
		if !s.sleepUnlessCancelled(wait) || s.isShut() {
			break
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		c2, aerr := s.ensureAttached(ctx)
		cancel()
		if aerr != nil {
			err = aerr
			continue
		}
		c, agentSID = c2, s.agentSID()
		rid := uuid.NewString()
		res, err = c.Conn.Prompt(context.Background(), acp.PromptRequest{SessionId: acp.SessionId(agentSID), MessageId: &rid, Prompt: []acp.ContentBlock{
			acp.TextBlock("继续。上一次请求因为网络错误中断了，请从中断的地方接着做。"),
		}})
	}
	ev := &Event{Kind: EvTurnEnd, MessageID: msgID}
	s.mu.Lock()
	ev.DurationMs, ev.OutputTokens = now()-s.turnStarted, s.turnTokens
	s.mu.Unlock()
	if err != nil && s.isShut() {
		err, ev.StopReason = nil, "cancelled" // 人关了 pane、进程是我们杀的：和按停止一样
	} else if err != nil {
		ev.Error = errText(err)
		ev.StopReason = "error"
	} else {
		ev.StopReason = string(res.StopReason)
	}
	// 这一轮结束了，还挂着的审批 / 提问全部作废（agent 那边已经不等了）。
	s.mu.Lock()
	for id, p := range s.pending {
		close(p.ch)
		delete(s.pending, id)
	}
	s.running = false
	s.turnMsg = ""
	go s.markSynced(10 * time.Second) // 这一轮是我们写的：记下文件现在多大（claude 在一轮之后还会补写标题之类，等一会儿再量）
	if err != nil {
		s.lastErr = ev.Error
	}
	next := s.autoNext && err == nil
	wake := s.autoWake
	s.autoNext, s.autoWake = false, nil
	// agent 自己又接着开了一轮（后台任务回来了）就等那一轮也跑完。出错了、人按了停止就不关了：人要看一眼 / 改主意了
	closeNow := s.closeAfter && !next && err == nil && ev.StopReason != "cancelled"
	if !next {
		s.closeAfter = false
	}
	if next {
		s.running, s.auto = true, true
		s.turnStarted, s.turnTokens = now(), 0
	}
	// A slash command held back while this turn ran goes next (deferred.go); after the agent's own turn if one follows
	var held *deferredMsg
	if !next {
		held = s.popDeferredLocked()
	}
	s.mu.Unlock()
	s.emit(ev)
	go s.autoTitle()
	if next {
		s.emit(&Event{Kind: EvTurnStart, Auto: true, Wake: wake})
	}
	s.flush(true)
	s.setStatus()
	s.h.saveIndex()
	if closeNow {
		go s.h.CloseUI(context.Background(), s.rec.ID)
	}
	if held != nil {
		go s.sendDeferred(held)
	}
}

func errText(err error) string {
	var re *acp.RequestError
	if errors.As(err, &re) {
		if m, ok := re.Data.(map[string]any); ok {
			if msg, ok := m["message"].(string); ok && msg != "" {
				return msg
			}
			if msg, ok := m["error"].(string); ok && msg != "" {
				return msg
			}
		}
		if re.Message != "" {
			return re.Message
		}
	}
	return err.Error()
}

func (s *Session) touchTitle(content []acp.ContentBlock) {
	s.mu.Lock()
	if s.rec.Title != "" && !(s.rec.TitleStale && !s.rec.CustomTitle) {
		s.mu.Unlock()
		return
	}
	for _, b := range content {
		if b.Text != nil && strings.TrimSpace(b.Text.Text) != "" && !strings.HasPrefix(strings.TrimSpace(b.Text.Text), "/") && !strings.HasPrefix(b.Text.Text, shellPrefix) && !strings.HasPrefix(b.Text.Text, handoffPrefix) {
			t := strings.Join(strings.Fields(b.Text.Text), " ")
			if utf8.RuneCountInString(t) > 60 {
				t = string([]rune(t)[:60]) + "…"
			}
			s.rec.Title, s.rec.TitleStale = t, false
			break
		}
	}
	s.mu.Unlock()
	s.h.broadcastSession(s)
	s.h.saveIndex()
}

func (s *Session) isShut() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.shut
}

// sleepUnlessCancelled：自动重试前的等待；人按了停止就提前返回 false。
func (s *Session) sleepUnlessCancelled(d time.Duration) bool {
	s.mu.Lock()
	s.retryCancel = make(chan struct{})
	ch := s.retryCancel
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.retryCancel = nil
		s.mu.Unlock()
	}()
	select {
	case <-time.After(d):
		return true
	case <-ch:
		return false
	}
}

func oneLine(t string, n int) string {
	t = strings.Join(strings.Fields(t), " ")
	if utf8.RuneCountInString(t) > n {
		t = string([]rune(t)[:n]) + "…"
	}
	return t
}

func (s *Session) Cancel(ctx context.Context) error {
	s.mu.Lock()
	if s.retryCancel != nil {
		close(s.retryCancel)
		s.retryCancel = nil
	}
	running := s.running
	sid := s.rec.AgentSessionID
	s.mu.Unlock()
	if !running {
		return nil
	}
	c, err := s.h.host(ctx, s.rec.Agent)
	if err != nil {
		return err
	}
	return c.Conn.Cancel(ctx, acp.CancelNotification{SessionId: acp.SessionId(sid)})
}
