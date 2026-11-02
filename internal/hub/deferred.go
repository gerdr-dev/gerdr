package hub

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/coder/acp-go-sdk"

	"github.com/gerdr-dev/gerdr/internal/agenthost"
)

// A slash command sent while a turn runs (/compact, /clear, a skill…) can't go in as a steer to every agent: Gerdr
// Agent and Codex only read commands from a turn's prompt, and to them a steer is text for the model — a mid-turn
// /compact got the reply "stopping here so you can compact" and compacted nothing (user report). Claude Code handles
// it itself (it holds a queued slash command until the turn ends, then runs it), and says so (Host.SteerCommands).
// For the others the hub does the same: the command is logged as a queued interjection (so it shows at the bottom as
// "Queued", with Withdraw, and ↑ / Esc take it back), held here, and once the turn ends it is taken out of the log
// and sent as a turn of its own — logged again at that point, so the log reads as if it had been sent then.
//
// A command the agent marks _meta.midTurn runs right away instead (Gerdr Agent's /rename: it only renames, nothing
// to wait for); it goes in as a steer and the agent runs it there.

type deferredMsg struct {
	id, from string
	content  []acp.ContentBlock
	logged   chan struct{} // closed once its queued user event is in the log (dispatch removes that event)
}

// commandName: the slash command the message starts with ("" = none). Same reading as the agents': the first block
// is text that starts with /name.
func commandName(content []acp.ContentBlock) string {
	if len(content) == 0 || content[0].Text == nil {
		return ""
	}
	t := strings.TrimSpace(content[0].Text.Text)
	if !strings.HasPrefix(t, "/") {
		return ""
	}
	name, _, _ := strings.Cut(t[1:], " ")
	name, _, _ = strings.Cut(name, "\n")
	if name == "" || strings.Contains(name, "/") {
		return ""
	}
	return name
}

// holdsCommand: whether this message, sent mid-turn, has to wait for the turn to end in the hub.
func (s *Session) holdsCommand(c *agenthost.Host, content []acp.ContentBlock) bool {
	if c.SteerCommands() {
		return false
	}
	name := commandName(content)
	if name == "" {
		return false
	}
	s.mu.Lock()
	cmds := s.commands
	s.mu.Unlock()
	if len(cmds) == 0 {
		cmds = s.h.cmds.get(s.rec.Agent)
	}
	for _, cmd := range cmds {
		if cmd.Name == name {
			mid, _ := cmd.Meta["midTurn"].(bool)
			return !mid
		}
	}
	return false // not a command of this agent's: just text, a steer like any other
}

// deferCommand holds the command until the running turn ends. False: the turn ended meanwhile, send it normally.
func (s *Session) deferCommand(msgID, from string, content []acp.ContentBlock, raw []json.RawMessage) bool {
	d := &deferredMsg{id: msgID, from: from, content: content, logged: make(chan struct{})}
	s.mu.Lock()
	if !s.running || s.shut {
		s.mu.Unlock()
		return false
	}
	s.deferred = append(s.deferred, d)
	s.mu.Unlock()
	s.emit(&Event{Kind: EvUser, MessageID: msgID, Content: raw, Steer: true, Held: true, From: from})
	s.emit(&Event{Kind: EvSteerState, MessageID: msgID, Text: "queued"})
	close(d.logged)
	return true
}

// popDeferredLocked takes the next held command when a turn ends (nil = none, or the pane is being closed).
func (s *Session) popDeferredLocked() *deferredMsg {
	if s.shut {
		s.deferred = nil
		return nil
	}
	if len(s.deferred) == 0 {
		return nil
	}
	d := s.deferred[0]
	s.deferred = s.deferred[1:]
	return d
}

// takeDeferred removes held commands (all of them when ids is nil) and returns their ids: they are being withdrawn.
func (s *Session) takeDeferred(ids map[string]bool) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	var keep []*deferredMsg
	for _, d := range s.deferred {
		if ids == nil || ids[d.id] {
			out = append(out, d.id)
		} else {
			keep = append(keep, d)
		}
	}
	s.deferred = keep
	return out
}

// sendDeferred runs a held command as a turn of its own, after the one it waited for has ended. The queued copy
// leaves the log first (the UI drops its bubble), and SendFrom logs it again where it really starts. Should another
// turn have started in between (someone sent a message), SendFrom holds it again behind that one.
func (s *Session) sendDeferred(d *deferredMsg) {
	<-d.logged
	// The SendFrom that held it may not have returned yet (its sending mark is still up): a turn that ended right then
	for i := 0; i < 40; i++ {
		s.mu.Lock()
		busy := s.sending[d.id]
		s.mu.Unlock()
		if !busy {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	var out EscapeResult
	s.dropUserEvents(map[string]bool{d.id: true}, &out)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if _, err := s.SendFrom(ctx, d.content, d.id, d.from, false); err != nil {
		s.h.log.Warn("deferred command", "session", s.ID(), "err", err)
		s.emit(&Event{Kind: EvNotice, Level: "error", Text: "排着的命令没发出去：" + oneLine(errText(err), 200)})
		// Nothing started, so no turn end will send the ones behind it
		s.mu.Lock()
		var next *deferredMsg
		if !s.running {
			next = s.popDeferredLocked()
		}
		s.mu.Unlock()
		if next != nil {
			s.sendDeferred(next)
		}
	}
}
