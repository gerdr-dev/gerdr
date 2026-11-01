package hub

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/gerdr-dev/gerdr/chatlog"
)

func upd(v map[string]any) *Event {
	raw, _ := json.Marshal(v)
	return &Event{Kind: EvUpdate, Update: raw}
}

func agentText(text string) *Event {
	return upd(map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": text}})
}

// A Claude Code conversation handed to Codex: the history is copied in front, a handoff notice follows, the first message
// after it carries the earlier conversation (once), and nothing before the notice can be rewound.
func TestHandoff(t *testing.T) {
	h, err := New(Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	src := Record{ID: "old", Agent: "claude", Cwd: "/tmp", Title: "修登录页", TitleKey: "k"}
	evs := []*Event{
		msg("u1", "登录页打不开，帮我看看"),
		{Kind: EvTurnStart, MessageID: "u1"},
		agentText("我先看一下路由。"),
		upd(map[string]any{"sessionUpdate": "tool_call", "toolCallId": "t1", "title": "Read src/router.ts", "status": "pending"}),
		upd(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "t1", "status": "completed",
			"content": []any{map[string]any{"type": "content", "content": map[string]any{"type": "text", "text": "export const routes = []"}}}}),
		upd(map[string]any{"sessionUpdate": "agent_thought_chunk", "content": map[string]any{"type": "text", "text": "秘密的思考"}}),
		agentText("路由表是空的。"),
		{Kind: EvTurnEnd, MessageID: "u1", Error: "usage limit reached"},
	}
	s := newSession(h, Record{ID: "new", Agent: "codex", Cwd: "/tmp"})
	h.register(s)
	h.adoptHistory(s, src, evs)

	got := s.Events()
	if len(got) != len(evs)+1 || got[len(got)-1].Kind != EvNotice || got[len(got)-1].Level != handoffLevel {
		t.Fatalf("log: %d events, last %+v", len(got), got[len(got)-1])
	}
	if s.rec.Title != "修登录页" || s.rec.CustomTitle || s.rec.TitleKey != "k" || s.rec.Handoff == nil || s.rec.Handoff.Agent != "claude" {
		t.Fatalf("record: %+v", s.rec)
	}

	p := s.pendingHandoff()
	if len(p) != 1 {
		t.Fatalf("pending %d", len(p))
	}
	txt := p[0].Text.Text
	for _, want := range []string{`<gerdr-handoff from="Claude Code">`, "## User\n登录页打不开", "## Assistant\n我先看一下路由。", "- tool: Read src/router.ts", "路由表是空的。", "usage limit reached", "</gerdr-handoff>"} {
		if !strings.Contains(txt, want) {
			t.Errorf("block lacks %q:\n%s", want, txt)
		}
	}
	if strings.Contains(txt, "秘密的思考") || strings.Contains(txt, "export const routes") {
		t.Errorf("thoughts / tool output inline:\n%s", txt)
	}
	full, err := os.ReadFile(s.handoffFile())
	if err != nil || !strings.Contains(string(full), "export const routes") || !strings.Contains(txt, s.handoffFile()) {
		t.Fatalf("full transcript file: %v\n%s", err, full)
	}
	raw, _ := json.Marshal(p[0])
	if !isHandoffBlock(raw) || contentText([]json.RawMessage{raw}) != "" {
		t.Error("the block must not count as something the user said")
	}

	// Carried once: after a user message it's no longer pending
	s.emit(msg("u2", "接着修"))
	if p := s.pendingHandoff(); len(p) != 0 {
		t.Fatalf("carried twice")
	}
	if _, _, _, err := chatlog.RewindPoint(s.Events(), "u1"); !errors.Is(err, chatlog.ErrBeforeHandoff) {
		t.Fatalf("rewind before the handoff: %v", err)
	}
	if _, _, _, err := chatlog.RewindPoint(s.Events(), "u2"); err != nil {
		t.Fatalf("rewind after the handoff: %v", err)
	}
}

// Handing over again: the earlier handoff block isn't repeated inside the new one (its history is in the log already).
func TestHandoffChained(t *testing.T) {
	block := `{"type":"text","text":"<gerdr-handoff from=\"Claude Code\">\nold stuff\n</gerdr-handoff>\n"}`
	evs := []*Event{
		msg("u1", "第一句"),
		agentText("好的"),
		{Kind: EvNotice, Level: handoffLevel, Text: "换 Codex 接着聊"},
		{Kind: EvUser, MessageID: "u2", Content: []json.RawMessage{json.RawMessage(block), json.RawMessage(`{"type":"text","text":"第二句"}`)}},
	}
	out := renderHandoff(evs, false)
	if strings.Contains(out, "old stuff") || !strings.Contains(out, "第一句") || !strings.Contains(out, "## User\n第二句") || !strings.Contains(out, "(换 Codex 接着聊)") {
		t.Fatalf("render:\n%s", out)
	}
}

// A long conversation keeps its most recent part inline, starting at a message.
func TestHandoffLong(t *testing.T) {
	var evs []*Event
	for i := 0; i < 400; i++ {
		evs = append(evs, msg("u", "问题"), agentText(strings.Repeat("长回答", 100)))
	}
	evs = append(evs, msg("last", "最后一个问题"))
	b := handoffBlock(renderHandoff(evs, false), "Claude Code", "Codex", "/x/full.md")
	if !strings.Contains(b, "only its most recent part") || !strings.Contains(b, "<transcript>\n## ") || !strings.Contains(b, "最后一个问题") {
		t.Fatalf("cut:\n%s", b[:500])
	}
	if n := len([]rune(b)); n > handoffInline+2000 {
		t.Fatalf("too long: %d", n)
	}
}
