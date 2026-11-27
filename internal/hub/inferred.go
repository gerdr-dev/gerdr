package hub

import (
	"context"
	"time"

	"github.com/coder/acp-go-sdk"
)

// Inferred turns: an external ACP agent (a "command" in config.json) can keep working after its session/prompt has
// answered. `agy acp` answers end_turn right after starting background subagents, then reads their results and goes
// on sending session/update for minutes. ACP has no message for "the agent started a turn on its own": our adapters
// say it with _meta.gerdr.autoTurn (onGerdrInfo), a third-party agent says nothing, so the session read as idle
// while the agent was plainly working (user report, 2026-11-23). For those agents the turn is inferred: content
// arriving while no turn runs opens an auto turn. Unfinished tool calls also carry straight into an inferred turn
// when the prompt answers, without waiting for another update. Our adapters report their own turns.
const (
	inferQuiet     = 60 * time.Second // agy goes ~45s without a word while it writes a big file
	inferOpenLimit = 10 * time.Minute // a tool call that never reports done doesn't hold the turn forever
	inferStopQuiet = 3 * time.Second  // after Escape: what still trickles in is dropped until it goes quiet
	inferAgyPoll   = 5 * time.Second
)

func (s *Session) infersTurns() bool {
	d, ok := s.h.agentDef(s.rec.Agent)
	return ok && len(d.Command) > 0
}

// noteActivity runs for each content update from an external agent, before it is emitted (the turn_start goes first).
func (s *Session) noteActivity(u acp.SessionUpdate) {
	var callID, title string
	var status acp.ToolCallStatus
	switch {
	case u.ToolCall != nil:
		callID, status, title = string(u.ToolCall.ToolCallId), u.ToolCall.Status, u.ToolCall.Title
		if status == "" {
			status = acp.ToolCallStatusPending
		}
	case u.ToolCallUpdate != nil:
		callID = string(u.ToolCallUpdate.ToolCallId)
		if u.ToolCallUpdate.Status != nil {
			status = *u.ToolCallUpdate.Status
		}
		if u.ToolCallUpdate.Title != nil {
			title = *u.ToolCallUpdate.Title
		}
	case u.AgentMessageChunk == nil && u.AgentThoughtChunk == nil && u.Plan == nil:
		return
	}
	s.mu.Lock()
	// session/load replays the whole conversation as updates: that's history, not work
	if s.loading > 0 || s.shut || (s.stopping != nil && !s.infer) {
		s.mu.Unlock()
		return
	}
	start := !s.running
	if callID != "" {
		switch status {
		case acp.ToolCallStatusCompleted, acp.ToolCallStatusFailed:
			delete(s.inferOpen, callID)
		case acp.ToolCallStatusPending, acp.ToolCallStatusInProgress:
			if s.inferOpen == nil {
				s.inferOpen = map[string]bool{}
			}
			s.inferOpen[callID] = true
		}
	}
	if s.agyACP && status == acp.ToolCallStatusCompleted && title == "Run invoke_subagent" {
		s.agyChildren = true // the launch finished; the child has its own conversation and can still be working
	}
	if start {
		s.startInferredLocked()
	} else if s.infer {
		s.inferLast = time.Now()
		if s.stopTurn {
			s.armInferLocked(inferStopQuiet)
		} else {
			s.armInferLocked(inferQuiet)
		}
	}
	s.mu.Unlock()
	if start {
		s.emit(&Event{Kind: EvTurnStart, Auto: true})
		s.setStatus()
	}
}

func (s *Session) startInferredLocked() {
	s.running, s.auto, s.infer, s.lastErr = true, true, true, ""
	s.turnStarted, s.turnTokens = now(), 0
	s.turnMode = s.turnModeLocked()
	s.quiet = false
	s.inferLast = time.Now()
	s.armInferLocked(inferQuiet)
}

func (s *Session) armInferLocked(d time.Duration) {
	if s.inferTimer == nil {
		s.inferTimer = time.AfterFunc(d, s.inferCheck)
	} else {
		s.inferTimer.Reset(d)
	}
}

func (s *Session) inferCheck() {
	s.mu.Lock()
	if !s.infer {
		s.mu.Unlock()
		return
	}
	idle := time.Since(s.inferLast)
	if s.stopTurn { // Escape already sent its turn_end; finishStop waits for stopTurn to drop
		if idle < inferStopQuiet {
			s.armInferLocked(inferStopQuiet - idle)
			s.mu.Unlock()
			return
		}
		s.running, s.auto, s.infer, s.stopTurn = false, false, false, false
		s.inferOpen, s.agyChildren = nil, false
		s.mu.Unlock()
		s.setStatus()
		return
	}
	if len(s.pending) > 0 { // waiting on a person's answer: the agent is not done
		s.armInferLocked(inferQuiet)
		s.mu.Unlock()
		return
	}
	wait := inferQuiet
	if len(s.inferOpen) > 0 {
		wait = inferOpenLimit
	}
	if idle < wait {
		s.armInferLocked(wait - idle)
		s.mu.Unlock()
		return
	}
	last, probe, sid := s.inferLast, s.agyProbe, s.rec.AgentSessionID
	checkChildren := s.agyChildren && probe != nil
	s.mu.Unlock()
	if checkChildren && probe(sid) {
		s.mu.Lock()
		if s.infer && !s.stopTurn && s.inferLast == last {
			s.armInferLocked(inferAgyPoll)
		}
		s.mu.Unlock()
		return
	}
	s.finishInferred(last)
}

// endInferred closes an inferred turn: it went quiet, or a person sent a message (Send), which then goes as a new
// prompt instead of being refused as a steer the agent can't take.
func (s *Session) endInferred() {
	s.finishInferred(time.Time{})
}

func (s *Session) finishInferred(last time.Time) {
	s.mu.Lock()
	if !s.infer || !last.IsZero() && (s.inferLast != last || s.stopTurn) {
		s.mu.Unlock()
		return
	}
	if !last.IsZero() && len(s.pending) > 0 {
		s.armInferLocked(inferQuiet)
		s.mu.Unlock()
		return
	}
	s.running, s.auto, s.infer = false, false, false
	s.inferTimer.Stop()
	stopped := s.stopTurn
	s.stopTurn = false
	for id, p := range s.pending {
		close(p.ch)
		delete(s.pending, id)
	}
	held := s.popDeferredLocked()
	if !last.IsZero() || s.shut {
		s.inferOpen, s.agyChildren = nil, false
	}
	closeNow := s.closeAfter && !s.shut && !last.IsZero() && held == nil
	if closeNow || s.shut {
		s.closeAfter = false
	}
	reason := acp.StopReasonEndTurn
	if s.shut {
		reason = acp.StopReasonCancelled
		s.quiet = true
	}
	dur, tok := now()-s.turnStarted, s.turnTokens
	s.mu.Unlock()
	if !stopped {
		s.emit(&Event{Kind: EvTurnEnd, StopReason: string(reason), Auto: true, DurationMs: dur, OutputTokens: tok})
	}
	s.flush(true)
	s.setStatus()
	if held != nil {
		go s.sendDeferred(held)
	}
	if closeNow {
		go s.h.CloseUI(context.Background(), s.ID())
	}
}

// beginLoading / endLoading bracket a session/load or resume: the updates it replays are the conversation so far.
func (s *Session) beginLoading() {
	s.mu.Lock()
	s.loading++
	s.mu.Unlock()
}

func (s *Session) endLoading() {
	s.mu.Lock()
	s.loading--
	s.mu.Unlock()
}
