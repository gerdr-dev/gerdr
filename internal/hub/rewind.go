package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gerdr-dev/gerdr/internal/i18n"

	"github.com/coder/acp-go-sdk"

	"github.com/gerdr-dev/gerdr/acpx"
	"github.com/gerdr-dev/gerdr/chatlog"
	"github.com/gerdr-dev/gerdr/internal/agenthost"
)

// ---- 回退 ----

// EscapeResult：按 Esc 的结果。Content 是撤回的话（前端放回输入框）。
type EscapeResult struct {
	Content    []json.RawMessage `json:"content"`
	RolledBack bool              `json:"rolledBack,omitempty"` // 整条消息撤回了（agent 一个字都还没回）
	Withdrawn  int               `json:"withdrawn,omitempty"`  // 收回了几条还没被处理的插话
}

// Escape：和 Claude Code 里按 Esc 一样 —— 打断这一轮，还没被 agent 用上的话放回输入框：
//   - 这一轮 agent 一个字都还没回：打断，整条消息连同后面的插话撤回（对话回到发之前），原话放回去；
//   - agent 已经在回了：最后那几条它还没处理的插话，先收回（claude 的 cancel_async_message）再打断 ——
//     先打断的话 claude 会把排着的插话留到下一轮去跑；收回的那几条从对话里删掉、放回去。
//     codex 收不回插话（turn/steer 一发就进了这一轮），只打断。
func (s *Session) Escape(ctx context.Context) (EscapeResult, error) {
	out := EscapeResult{Content: []json.RawMessage{}}
	s.escMu.Lock()
	defer s.escMu.Unlock()
	s.mu.Lock()
	running, auto, turnMsg, agentSID := s.running, s.auto, s.turnMsg, s.rec.AgentSessionID
	s.loadLocked()
	evs := append([]*Event(nil), s.events...)
	s.mu.Unlock()
	if !running {
		return out, nil
	}
	// 要收回的插话：最后一次 agent 有动静之后的用户消息，加上这一轮里还标着「排队中」的插话（chatlog.StopPlan）
	tail, quiet := chatlog.StopPlan(evs)
	untouched := quiet && !auto && turnMsg != ""

	// A stop that takes seconds was reported (codex, from the phone) but didn't reproduce: log where the time goes when
	// it's slow, so the next one shows which step it was.
	t0 := time.Now()
	var tAttach, tWithdraw, tCancel time.Duration
	defer func() {
		if d := time.Since(t0); d > time.Second {
			s.h.log.Warn("escape: slow", "session", s.ID(), "agent", s.rec.Agent, "took", d, "attach", tAttach, "withdraw", tWithdraw, "cancel", tCancel)
		}
	}()
	c, err := s.ensureAttached(ctx)
	tAttach = time.Since(t0)
	if err != nil {
		return out, err
	}
	withdrawn := map[string]bool{}
	// Slash commands the hub held for the end of this turn (deferred.go) are queued interjections like any other:
	// taken back into the composer, and taken out first, so the turn ending below doesn't send them.
	for _, id := range s.takeDeferred(nil) {
		withdrawn[id] = true
	}
	if len(tail) > 0 && !untouched {
		var ids []string
		for _, e := range tail {
			if !withdrawn[e.MessageID] {
				ids = append(ids, e.MessageID)
			}
		}
		if r, err := agenthost.Call[acpx.WithdrawResponse](ctx, c, acpx.MethodWithdraw, acpx.WithdrawRequest{SessionId: acp.SessionId(agentSID), MessageIds: ids}); err == nil {
			for _, id := range r.Withdrawn {
				withdrawn[id] = true
			}
		}
	}
	tWithdraw = time.Since(t0)
	if err := s.Cancel(ctx); err != nil {
		return out, err
	}
	tCancel = time.Since(t0)
	// 等这一轮真停下来（回退要求没在跑）
	for i := 0; i < 200; i++ {
		s.mu.Lock()
		r := s.running
		s.mu.Unlock()
		if !r {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if untouched {
		_, err := s.Rewind(ctx, turnMsg)
		// agent 那边找不到这条：打断得太快，它还没把消息记进会话（claude 还没写进记录 / codex 还没建出那一轮）——
		// 等于它压根没收到，只在 gerdr 这边把这一轮删掉就行（实测：发出去 150ms 就按 Esc 是这种）。
		if err != nil && strings.Contains(err.Error(), "not found") {
			s.truncateFrom(turnMsg)
			err = nil
		}
		if err == nil {
			out.RolledBack = true
			for _, e := range evs {
				if e.Kind == EvUser && (e.MessageID == turnMsg || containsEvent(tail, e)) {
					out.Content = append(out.Content, s.h.inflate(e.Content)...)
				}
			}
			return out, nil
		}
		s.h.log.Warn("escape: rewind failed", "err", err)
	}
	if len(withdrawn) > 0 {
		s.dropUserEvents(withdrawn, &out)
	}
	return out, nil
}

// Warm：把会话的 agent 进程先起来（见 acpx.MethodWarm）。
func (s *Session) Warm(ctx context.Context) error {
	c, err := s.ensureAttached(ctx)
	if err != nil {
		return err
	}
	_, err = c.Conn.CallExtension(ctx, acpx.MethodWarm, map[string]any{"sessionId": s.agentSID()})
	return err
}

// WithdrawLast：输入框里按 ↑ —— 收回最后一条还没被 agent 读到的插话，放回输入框改。
func (s *Session) WithdrawLast(ctx context.Context) (EscapeResult, error) {
	s.mu.Lock()
	id := ""
	if s.running {
		id = s.lastQueuedLocked()
	}
	s.mu.Unlock()
	if id == "" {
		return EscapeResult{Content: []json.RawMessage{}}, nil
	}
	return s.Withdraw(ctx, id)
}

// lastQueuedLocked：最后一条插话还在 agent 队列里没被读到，就回它的 id。
// 看排队状态（steer_state：queued / started）。原来看「它后面 agent 还有没有动静」：插话默认排到下一个空档（priority next）后，
// 排着的时候 agent 照样在出字，那样判断一条都收不回（用户报的）。不报排队状态的 agent 才退回老办法。
func (s *Session) lastQueuedLocked() string {
	s.loadLocked()
	return chatlog.LastQueued(s.events)
}

// Withdraw：收回一条还没被 agent 读到的插话（「排队中」那行的撤回 / 立即发、输入框里按 ↑）。
// 已经被读到的收不回，agent 回空，这里什么也不动（Withdrawn = 0）。
func (s *Session) Withdraw(ctx context.Context, msgID string) (EscapeResult, error) {
	out := EscapeResult{Content: []json.RawMessage{}}
	s.mu.Lock()
	agentSID := s.rec.AgentSessionID
	found := false
	s.loadLocked()
	for _, e := range s.events {
		if e.Kind == EvUser && e.MessageID == msgID && e.Steer {
			found = true
			break
		}
	}
	s.mu.Unlock()
	if !found {
		return out, nil
	}
	if len(s.takeDeferred(map[string]bool{msgID: true})) > 0 { // held by the hub, the agent never saw it (deferred.go)
		s.dropUserEvents(map[string]bool{msgID: true}, &out)
		return out, nil
	}
	c, err := s.ensureAttached(ctx)
	if err != nil {
		return out, err
	}
	r, err := agenthost.Call[acpx.WithdrawResponse](ctx, c, acpx.MethodWithdraw, acpx.WithdrawRequest{SessionId: acp.SessionId(agentSID), MessageIds: []string{msgID}})
	if err != nil || len(r.Withdrawn) == 0 {
		return out, nil
	}
	s.dropUserEvents(map[string]bool{msgID: true}, &out)
	return out, nil
}

// truncateFrom：日志从这条用户消息起全部删掉（agent 那边没有它，不用回退 agent）。
func (s *Session) truncateFrom(msgID string) {
	found := false
	all := s.rewriteLog(func() {
		for i, e := range s.events {
			if e.Kind == EvUser && e.MessageID == msgID {
				s.events, s.lastErr, found = s.events[:i], "", true
				return
			}
		}
	})
	if !found {
		return
	}
	s.h.broadcastEvents(s.rec.ID, all, true)
	s.setStatus()
}

// dropUserEvents：把收回的那几条用户消息从日志里删掉（内容收进 out）, then tells the UIs with one EvWithdrawn per
// message. It used to push the whole log again (reset): on a long session that rebuilt the whole conversation in
// every open view and dropped the older pages scrolled in, which is what made a withdraw feel slow (user report; the
// rewrite itself is ~70ms on a 14MB log). The marker is logged, so a client catching up by seq gets it too, and a
// replay where the message is already gone ignores it.
func (s *Session) dropUserEvents(ids map[string]bool, out *EscapeResult) {
	var gone []string
	s.rewriteLog(func() {
		for _, e := range s.events {
			if e.Kind == EvUser && ids[e.MessageID] {
				gone = append(gone, e.MessageID)
			}
		}
		kept, content, n := chatlog.DropUsers(s.events, ids)
		out.Content = append(out.Content, s.h.inflate(content)...)
		out.Withdrawn += n
		s.events = kept
	})
	for _, id := range gone {
		s.emit(&Event{Kind: EvWithdrawn, MessageID: id})
	}
}

func containsEvent(list []*Event, e *Event) bool {
	for _, x := range list {
		if x == e {
			return true
		}
	}
	return false
}

type RewindResult struct {
	// Content 是被回退掉的那条消息（前端放回输入框，人改两个字再发）。
	Content []json.RawMessage `json:"content"`
}

// Rewind 回到 messageID 那条消息之前：只回退对话（交给 agent 截断），不动文件。
// 以前用影子 git 仓库给 cwd 打快照、一起恢复文件，但 cwd 以外改的文件管不到、同目录多个会话的快照
// 互相覆盖，坑太多，删掉了。
func (s *Session) Rewind(ctx context.Context, messageID string) (RewindResult, error) {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return RewindResult{}, errors.New(i18n.S("先停下这一轮再回退", "Stop the current turn before rewinding"))
	}
	// 两种用户消息：gerdr 自己发的（EvUser），和导入 / 追上时从 agent 记录重放的（user_message_chunk，
	// messageId 就是记录里那条的 uuid，一条带图的消息拆成几块、同一个 id）。只认前一种的话，
	// 重新载入过的会话上面那些消息全都「找不到这条消息」（踩过）。
	// /clear 之后 claude 换了一个新会话，这条之前的消息都在旧会话里 —— 回不去（chatlog.ErrBeforeClear）
	s.loadLocked()
	idx, content, steer, err := chatlog.RewindPoint(s.events, messageID)
	if err != nil {
		s.mu.Unlock()
		return RewindResult{}, err
	}
	agentSID := s.rec.AgentSessionID
	s.mu.Unlock()

	c, err := s.ensureAttached(ctx)
	if err != nil {
		return RewindResult{}, err
	}
	if steer && !c.SteerRewind() {
		return RewindResult{}, errors.New(i18n.S("插话的消息不能单独回退，回退它所在那一轮开头的那条", "A message sent mid-turn can't be rewound on its own; rewind the message that started its turn"))
	}
	res, err := agenthost.Call[acpx.RewindResponse](ctx, c, acpx.MethodRewind, acpx.RewindRequest{SessionId: acp.SessionId(agentSID), MessageId: messageID})
	if err != nil {
		return RewindResult{}, fmt.Errorf(i18n.S("回退对话失败：%s", "rewinding the conversation failed: %s"), errText(err))
	}
	if res.SessionId != "" && string(res.SessionId) != agentSID {
		s.h.rekey(s, string(res.SessionId))
	}
	if res.Fork != nil {
		s.mu.Lock()
		s.rec.Fork = res.Fork
		s.mu.Unlock()
	}
	out := RewindResult{Content: s.h.inflate(content)}
	evs := s.rewriteLog(func() {
		s.events = s.events[:idx]
		if steer {
			// Cut mid-turn: the turn_end went with the rest. Close the turn so a reload doesn't leave it open.
			s.events = append(s.events, &Event{Seq: s.h.nextSeqLocked(), TS: now(), Kind: EvTurnEnd, StopReason: string(acp.StopReasonEndTurn)})
		}
		s.lastErr = ""
	})
	s.h.broadcastEvents(s.rec.ID, evs, true)
	s.setStatus()
	s.h.saveIndex()
	return out, nil
}
