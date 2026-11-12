package hub

// Continuing a conversation with a different agent: the user ran out of Claude Code quota (or just prefers another
// model for the next step) and carries on in Codex / Gerdr Agent. Agents can't read each other's transcripts, so this
// can't be a resume. It is a new gerdr session for the target agent (one gerdr session per agent session, as in
// clear.go) whose log starts with a copy of the old one followed by a "handoff" notice; the old session is left as it
// was and can still be resumed with its own agent.
//
// The new agent learns the earlier conversation from the first message sent after the notice: it rides in front of
// what the user typed as one text block (handoffBlock), the same way `!command` output rides along (shell.go). It is
// logged verbatim, so CatchUp's comparison and Reload match what the agent recorded; the UI drops the block (the
// copied history is already on screen above the notice) or, replayed from the agent's transcript after a Reload, draws
// it as a one-line notice. Tool results are left out of the inline text to keep it small (files the old agent changed
// are already changed on disk); the whole thing, tool output included, goes to a file the new agent can read.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/gerdr-dev/gerdr/internal/i18n"

	"github.com/coder/acp-go-sdk"
)

const (
	// handoffPrefix starts the block carrying the earlier conversation. The UI, titles and "put it back in the composer"
	// recognise it by this and skip it.
	handoffPrefix = "<gerdr-handoff"
	// handoffLevel is the notice marking where the new agent took over. Rewinding past it isn't possible: the messages
	// above it are in the old agent's session, not in this one.
	handoffLevel = "handoff"
	// handoffInline caps the transcript given inline (runes, about 25k tokens). Longer conversations keep their most
	// recent part inline; the full one is in the file.
	handoffInline = 80000
	// handoffToolOut caps each tool's output in the file.
	handoffToolOut = 2000
)

// Handoff on a Record: this session continues another agent's conversation.
type Handoff struct {
	Session string `json:"session"` // the gerdr session it was copied from
	Agent   string `json:"agent"`   // that session's agent
}

func isHandoffBlock(raw json.RawMessage) bool {
	var b struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	return json.Unmarshal(raw, &b) == nil && b.Type == "text" && strings.HasPrefix(b.Text, handoffPrefix)
}

var handoffFrom = regexp.MustCompile(`^<gerdr-handoff from="([^"]*)">`)

// handoffTitle fixes the title an agent lists for one of its sessions (ListImportable). The agent titles a session
// after its first message, which for a handed-over conversation is the handoff block, so the resume list showed
// "<gerdr-handoff from=…> The user started this…". The listed title is already cut short, with nothing the user wrote
// left in it, so all there is to say is whose conversation it carries on.
func handoffTitle(title string) string {
	m := handoffFrom.FindStringSubmatch(strings.TrimSpace(title))
	if m == nil {
		return title
	}
	return fmt.Sprintf(i18n.S("接着 %s 的对话", "Continued from %s"), m[1])
}

// continueFrom is Create with o.ContinueFrom set: a new session for o.Agent carrying on the conversation of another
// session (any agent but o.Agent).
func (h *Hub) continueFrom(ctx context.Context, o CreateOptions) (*Session, error) {
	src, err := h.Session(o.ContinueFrom)
	if err != nil {
		return nil, err
	}
	src.mu.Lock()
	running := src.running
	srcRec := src.rec
	src.loadLocked()
	evs := make([]*Event, 0, len(src.events))
	for _, e := range src.events {
		c := *e // the two sessions' logs must not share event objects
		evs = append(evs, &c)
	}
	src.mu.Unlock()
	if running {
		return nil, errors.New(i18n.S("这个会话还在跑：先停下这一轮，再换 agent 接着聊", "This session is still running: stop the current turn before switching agents"))
	}
	// The same agent only from code (a panel replacing a broken or reconfigured conversation): a person would just
	// open the old one
	same := srcRec.Agent == o.Agent
	if same && !o.FromCLI {
		return nil, errors.New(i18n.S("同一个 agent 直接打开这个会话就行，不用换", "It's the same agent: just open this session, no need to switch"))
	}
	if o.Cwd == "" {
		o.Cwd = srcRec.Cwd
	}
	if o.Project == "" {
		o.Project = srcRec.Project
	}
	if o.ArtifactsDir == "" {
		o.ArtifactsDir = srcRec.ArtifactsDir // a panel's task folder stays the task's
	}
	o.ContinueFrom, o.Import, o.Title = "", "", "" // a given title would count as the user's (CustomTitle)
	s, err := h.Create(ctx, o)
	if err != nil {
		return nil, err
	}
	if same {
		h.adoptHistoryNote(s, srcRec, evs, i18n.S("换了一个新会话接着聊：上面是之前的对话，下一条消息会把它一起带过去", "Continued in a new session: above is the earlier conversation; the next message brings it along"))
	} else {
		h.adoptHistory(s, srcRec, evs)
	}
	return s, nil
}

// History is a conversation brought from another computer (CreateOptions.History). From: where it was, for the
// notice ("the Mac mini"). Images in Events must already be in this hub's blobs/ (the console copies them first).
type History struct {
	Record Record   `json:"record"`
	Events []*Event `json:"events"`
	From   string   `json:"from"`
}

// continueHistory is Create with o.History: like continueFrom, but the source is given, not looked up.
func (h *Hub) continueHistory(ctx context.Context, o CreateOptions) (*Session, error) {
	hist := o.History
	if len(hist.Events) == 0 {
		return nil, errors.New(i18n.S("没有对话可以接", "There's no conversation to carry on"))
	}
	evs := make([]*Event, 0, len(hist.Events))
	for _, e := range hist.Events {
		if e == nil {
			continue
		}
		c := *e
		c.From = "" // senders were sessions over there: nothing here to point at
		evs = append(evs, &c)
	}
	o.History, o.ContinueFrom, o.Import, o.Title = nil, "", "", ""
	s, err := h.Create(ctx, o)
	if err != nil {
		return nil, err
	}
	h.adoptHistoryNote(s, hist.Record, evs, fmt.Sprintf(i18n.S("从 %s 搬过来接着聊：上面是在那边的对话，下一条消息会把它一起带给 %s", "Moved here from %s: above is the conversation there; your next message will bring it along to %s"), firstNonEmpty(hist.From, "?"), h.agentName(s.rec.Agent)))
	return s, nil
}

// adoptHistory puts src's log (evs, copies) in front of s's and marks where s's agent takes over.
func (h *Hub) adoptHistory(s *Session, srcRec Record, evs []*Event) {
	from, to := h.agentName(srcRec.Agent), h.agentName(s.rec.Agent)
	h.adoptHistoryNote(s, srcRec, evs, fmt.Sprintf(i18n.S("换 %s 接着聊：上面是和 %s 的对话，下一条消息会把它一起带给 %s", "Switched to %s: above is the conversation with %s; your next message will bring it along to %s"), to, from, to))
}

func (h *Hub) adoptHistoryNote(s *Session, srcRec Record, evs []*Event, note string) {
	all := s.rewriteLog(func() {
		s.events = append(evs, s.events...)
		s.rec.Title, s.rec.CustomTitle, s.rec.TitleKey, s.rec.PluginTitleFor = srcRec.Title, srcRec.CustomTitle, srcRec.TitleKey, srcRec.PluginTitleFor
		s.rec.Handoff = &Handoff{Session: srcRec.ID, Agent: srcRec.Agent}
	})
	h.broadcastEvents(s.rec.ID, all, true)
	s.emit(&Event{Kind: EvNotice, Level: handoffLevel, Text: note})
	h.saveIndex()
	h.broadcastSession(s)
}

func (h *Hub) agentName(id string) string {
	if d, ok := h.agentDef(id); ok && d.Name != "" {
		return d.Name
	}
	return id
}

// pendingHandoff: the block carrying the earlier conversation, when nothing has been said since the last handoff
// notice (so it hasn't been carried yet). Nil otherwise. Writes the full transcript file as a side effect.
// Not under s.mu: agent names take the hub's locks, and the hub takes s.mu under its own (rekey).
func (s *Session) pendingHandoff() []acp.ContentBlock {
	s.mu.Lock()
	s.loadLocked()
	at := -1
	for i := len(s.events) - 1; i >= 0; i-- {
		e := s.events[i]
		if e.Kind == EvUser {
			break
		}
		if _, ok := userChunkHead(e); ok {
			break
		}
		if e.Kind == EvNotice && e.Level == handoffLevel {
			at = i
			break
		}
	}
	var evs []*Event
	if at >= 0 {
		evs = append(evs, s.events[:at]...)
	}
	hf := s.rec.Handoff
	agent := s.rec.Agent
	s.mu.Unlock()
	if at < 0 {
		return nil
	}
	from := "another agent"
	if hf != nil {
		from = s.h.agentName(hf.Agent)
	}
	path := s.handoffFile()
	if err := os.WriteFile(path, []byte(renderHandoff(evs, true)), 0o600); err != nil {
		s.h.log.Warn("write handoff transcript", "id", s.rec.ID, "err", err)
		path = ""
	}
	return []acp.ContentBlock{acp.TextBlock(handoffBlock(renderHandoff(evs, false), from, s.h.agentName(agent), path))}
}

func (s *Session) handoffFile() string {
	return filepath.Join(filepath.Dir(s.lf.Path), s.rec.ID+".handoff.md")
}

// handoffBlock wraps the transcript for the new agent. Written in English for the model; it answers in the user's
// language anyway.
func handoffBlock(transcript, from, to, path string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s from=%q>\n", handoffPrefix, from)
	fmt.Fprintf(&b, "The user started this conversation with another coding agent (%s) and is now continuing it with you (%s), "+
		"in the same working directory. Below is that conversation so far, rebuilt from the chat log: what the user said, "+
		"what the assistant answered, and the tools it called (titles only, without their results). Files it changed are "+
		"already changed on disk; re-read files or re-run commands when you need details. Carry on from where it left off: "+
		"the user's new message follows this block.\n", from, to)
	cut := false
	if r := []rune(transcript); len(r) > handoffInline {
		transcript = string(r[len(r)-handoffInline:])
		if i := strings.Index(transcript, "\n## "); i >= 0 {
			transcript = transcript[i+1:] // start at a message boundary
		}
		cut = true
	}
	if path != "" {
		if cut {
			fmt.Fprintf(&b, "The conversation is long: only its most recent part is below. The full transcript, with tool output, is in %s.\n", path)
		} else {
			fmt.Fprintf(&b, "The full transcript, with tool output, is in %s.\n", path)
		}
	} else if cut {
		b.WriteString("The conversation is long: only its most recent part is below.\n")
	}
	b.WriteString("\n<transcript>\n")
	b.WriteString(strings.TrimSpace(transcript))
	b.WriteString("\n</transcript>\n</gerdr-handoff>\n")
	return b.String()
}

// renderHandoff turns a chat log into readable text: user messages, the agents' replies, tool calls (one line each,
// with their output when withOutput), errors that ended a turn, earlier handoffs. Thoughts, plans, sub-agents' own
// messages and permission traffic are left out. Replies are "Assistant", not the agent's name: a conversation handed
// over twice has more than one agent in it, and the handoff lines between them say who was who.
func renderHandoff(evs []*Event, withOutput bool) string {
	type tool struct {
		title, status string
		out           strings.Builder
	}
	var b strings.Builder
	tools := map[string]*tool{}
	var order []string // tool call ids, in the order they first appeared, flushed at the next text
	role := ""
	head := func(r string) {
		if role != r {
			if b.Len() > 0 {
				b.WriteString("\n\n")
			}
			fmt.Fprintf(&b, "## %s\n", r)
			role = r
		}
	}
	flushTools := func() {
		for _, id := range order {
			t := tools[id]
			head("Assistant")
			line := "- tool: " + oneLine(t.title, 200)
			if t.status == "failed" {
				line += " (failed)"
			}
			b.WriteString("\n" + line)
			if withOutput && t.out.Len() > 0 {
				o := t.out.String()
				if utf8.RuneCountInString(o) > handoffToolOut {
					o = string([]rune(o)[:handoffToolOut]) + "\n…(cut)"
				}
				b.WriteString("\n  ```\n  " + strings.ReplaceAll(strings.TrimRight(o, "\n"), "\n", "\n  ") + "\n  ```")
			}
		}
		order = order[:0]
	}
	userMsg := ""
	for _, e := range evs {
		switch e.Kind {
		case EvUser:
			flushTools()
			if t := handoffUserText(e.Content); t != "" {
				head("User")
				b.WriteString(t)
			}
			userMsg = ""
		case EvTurnEnd:
			if e.Error != "" {
				flushTools()
				head("Assistant")
				b.WriteString("\n(the turn ended with an error: " + oneLine(e.Error, 300) + ")")
			}
		case EvNotice:
			if e.Level == handoffLevel || e.Level == "clear" {
				flushTools()
				if b.Len() > 0 {
					b.WriteString("\n\n")
				}
				b.WriteString("(" + e.Text + ")")
				role = ""
			}
		case EvUpdate:
			var u struct {
				SessionUpdate string          `json:"sessionUpdate"`
				MessageID     string          `json:"messageId"`
				Content       json.RawMessage `json:"content"`
				ToolCallID    string          `json:"toolCallId"`
				Title         *string         `json:"title"`
				Status        *string         `json:"status"`
				Meta          struct {
					Gerdr struct {
						Subagent string `json:"subagent"`
					} `json:"gerdr"`
				} `json:"_meta"`
			}
			if json.Unmarshal(e.Update, &u) != nil || u.Meta.Gerdr.Subagent != "" {
				continue
			}
			switch u.SessionUpdate {
			case "user_message_chunk": // history replayed from an agent's transcript (imported / reloaded)
				t := handoffUserText([]json.RawMessage{u.Content})
				if t == "" {
					continue
				}
				flushTools()
				if u.MessageID == "" || u.MessageID != userMsg || role != "User" {
					head("User")
				} else {
					b.WriteString("\n")
				}
				userMsg = u.MessageID
				b.WriteString(t)
			case "agent_message_chunk":
				var c struct {
					Type string `json:"type"`
					Text string `json:"text"`
				}
				if json.Unmarshal(u.Content, &c) != nil || c.Type != "text" || c.Text == "" {
					continue
				}
				if len(order) > 0 {
					flushTools()
					b.WriteString("\n\n")
				}
				head("Assistant")
				b.WriteString(c.Text)
			case "tool_call", "tool_call_update":
				t := tools[u.ToolCallID]
				if t == nil {
					t = &tool{}
					tools[u.ToolCallID] = t
					order = append(order, u.ToolCallID)
				}
				if u.Title != nil && *u.Title != "" {
					t.title = *u.Title
				}
				if u.Status != nil {
					t.status = *u.Status
				}
				if withOutput {
					var items []struct {
						Type    string `json:"type"`
						Path    string `json:"path"`
						Content struct {
							Type string `json:"type"`
							Text string `json:"text"`
						} `json:"content"`
					}
					if json.Unmarshal(u.Content, &items) == nil && len(items) > 0 {
						t.out.Reset() // updates carry the whole content so far
						for _, it := range items {
							switch {
							case it.Type == "content" && it.Content.Type == "text":
								t.out.WriteString(it.Content.Text + "\n")
							case it.Type == "diff":
								t.out.WriteString("(edited " + it.Path + ")\n")
							}
						}
					}
				}
			}
		}
	}
	flushTools()
	return b.String()
}

// handoffUserText: what the user said in these blocks (with any `!command` output that rode along), earlier handoff
// blocks left out, images and files as placeholders.
func handoffUserText(blocks []json.RawMessage) string {
	var parts []string
	for _, raw := range blocks {
		var c struct {
			Type string `json:"type"`
			Text string `json:"text"`
			URI  string `json:"uri"`
			Name string `json:"name"`
		}
		if json.Unmarshal(raw, &c) != nil || isHandoffBlock(raw) {
			continue
		}
		switch c.Type {
		case "text":
			if t := strings.TrimSpace(c.Text); t != "" {
				parts = append(parts, t)
			}
		case "image":
			parts = append(parts, "[image]")
		case "resource_link":
			parts = append(parts, "[file: "+firstNonEmpty(c.URI, c.Name)+"]")
		}
	}
	return strings.Join(parts, "\n")
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
