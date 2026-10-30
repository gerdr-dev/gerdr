package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/coder/acp-go-sdk"
)

// Code mode (on by default since codex ~0.16x): the model no longer calls tools directly, it calls one `exec` tool with
// a JavaScript cell that calls the real tools (`tools.exec_command(...)`, `tools.mcp__x__y(...)`), and `wait` to poll a
// cell that yielded. The app-server emits items for the nested tool calls (commandExecution, mcpToolCall…) but nothing
// for `exec` / `wait` themselves — not live, not in thread/read or thread/turns/list (checked on 0.166). A pure-JS cell
// (e.g. filtering ALL_TOOLS) left no trace at all in the chat. The only record is the rollout file
// (response_item custom_tool_call / function_call + their *_output), so we read the cards from there.
//
// Live: the rollout line for the call is written as soon as the model emits it, before the nested tools run (measured:
// ~2s before the nested commandExecution's item/started), so polling the file before handling each notification puts
// the exec card above the nested cards. A ticker covers pure-JS cells, which produce no notifications while running.

// rollTail follows one thread's rollout file.
type rollTail struct {
	mu       sync.Mutex
	thread   string
	path     string
	off      int64
	fromEnd  bool // attached to an existing thread: skip what's already in the file (LoadSession replays it)
	lastLook time.Time
	open     map[string]*rollCall // cards opened and not yet completed, by call id
	stop     chan struct{}        // closes the per-turn ticker
}

type rollCall struct {
	tool string // "exec" | "wait"
	code string
}

func newRollTail(thread string, fromEnd bool) *rollTail {
	return &rollTail{thread: thread, fromEnd: fromEnd, open: map[string]*rollCall{}}
}

// rollLine is the part of a rollout line we care about.
type rollLine struct {
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"`
	Payload   struct {
		Type      string          `json:"type"`
		Name      string          `json:"name"`
		Namespace string          `json:"namespace"`
		CallID    string          `json:"call_id"`
		Input     string          `json:"input"`     // custom_tool_call
		Arguments string          `json:"arguments"` // function_call
		Output    json.RawMessage `json:"output"`
	} `json:"payload"`
}

func parseRollLine(b []byte) (rollLine, bool) {
	var l rollLine
	if !bytes.Contains(b, []byte(`"response_item"`)) || !bytes.Contains(b, []byte(`call`)) {
		return l, false
	}
	if json.Unmarshal(b, &l) != nil || l.Type != "response_item" || l.Payload.CallID == "" {
		return l, false
	}
	switch l.Payload.Type {
	case "custom_tool_call":
		return l, l.Payload.Name == "exec"
	case "function_call":
		return l, l.Payload.Name == "wait" && l.Payload.Namespace == ""
	case "custom_tool_call_output", "function_call_output":
		return l, true
	}
	return l, false
}

// rollStartTurn / rollEndTurn bracket a turn: a ticker polls meanwhile so pure-JS cells show up while they run.
func (a *Agent) rollStartTurn(s *session) {
	rt := s.roll
	if rt == nil {
		return
	}
	rt.mu.Lock()
	if rt.stop != nil {
		rt.mu.Unlock()
		return
	}
	stop := make(chan struct{})
	rt.stop = stop
	rt.mu.Unlock()
	go func() {
		t := time.NewTicker(400 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				a.rollPoll(context.Background(), s)
			}
		}
	}()
}

func (a *Agent) rollEndTurn(ctx context.Context, s *session) {
	rt := s.roll
	if rt == nil {
		return
	}
	a.rollPoll(ctx, s)
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.stop != nil {
		close(rt.stop)
		rt.stop = nil
	}
	// No output by the end of the turn = interrupted: don't leave a spinner behind.
	for id, c := range rt.open {
		a.out(ctx, s, acp.UpdateToolCall(acp.ToolCallId(id), acp.WithUpdateStatus(acp.ToolCallStatusFailed), acp.WithUpdateContent(callContent(c, ""))))
		delete(rt.open, id)
	}
}

// rollPoll emits cards for whatever was appended to the rollout since the last poll.
func (a *Agent) rollPoll(ctx context.Context, s *session) {
	rt := s.roll
	if rt == nil {
		return
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.path == "" {
		if time.Since(rt.lastLook) < time.Second {
			return
		}
		rt.lastLook = time.Now()
		if rt.path = a.rolloutPath(rt.thread); rt.path == "" {
			return
		}
		if rt.fromEnd {
			if st, err := os.Stat(rt.path); err == nil {
				rt.off = st.Size()
			}
		}
	}
	f, err := os.Open(rt.path)
	if err != nil {
		return
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil || st.Size() <= rt.off {
		return
	}
	if _, err := f.Seek(rt.off, io.SeekStart); err != nil {
		return
	}
	data, _ := io.ReadAll(f)
	// Only whole lines: the writer may be mid-line.
	end := bytes.LastIndexByte(data, '\n')
	if end < 0 {
		return
	}
	rt.off += int64(end + 1)
	for _, b := range bytes.Split(data[:end], []byte{'\n'}) {
		if l, ok := parseRollLine(b); ok {
			a.rollEmit(ctx, s, rt, l)
		}
	}
}

// rollEmit turns one call / output line into a card update. Caller holds rt.mu (or owns rt).
func (a *Agent) rollEmit(ctx context.Context, s *session, rt *rollTail, l rollLine) {
	p := l.Payload
	switch p.Type {
	case "custom_tool_call", "function_call":
		c := &rollCall{tool: p.Name, code: p.Input}
		title := "Run JS"
		if p.Name == "wait" {
			var args struct {
				CellID string `json:"cell_id"`
			}
			_ = json.Unmarshal([]byte(p.Arguments), &args)
			title = "Wait for JS cell " + args.CellID
		} else if line := firstCodeLine(p.Input); line != "" {
			title = "Run JS · " + line
		}
		rt.open[p.CallID] = c
		a.out(ctx, s, acp.StartToolCall(acp.ToolCallId(p.CallID), title,
			acp.WithStartKind(acp.ToolKindOther), acp.WithStartStatus(acp.ToolCallStatusInProgress),
			acp.WithStartContent(callContent(c, ""))))
	default:
		c := rt.open[p.CallID]
		if c == nil {
			return // output of some other tool (sleep, spawn_agent…): the app-server already shows those
		}
		delete(rt.open, p.CallID)
		text, failed := cellOutput(p.Output)
		status := acp.ToolCallStatusCompleted
		if failed {
			status = acp.ToolCallStatusFailed
		}
		a.out(ctx, s, acp.UpdateToolCall(acp.ToolCallId(p.CallID), acp.WithUpdateStatus(status), acp.WithUpdateContent(callContent(c, text))))
	}
}

// callContent: an exec card shows the cell's code, then its output (content replaces, so the code goes in every update).
func callContent(c *rollCall, output string) []acp.ToolCallContent {
	var out []acp.ToolCallContent
	if c.code != "" {
		out = append(out, acp.ToolContent(acp.TextBlock(c.code)))
	}
	if output != "" {
		out = append(out, acp.ToolContent(acp.TextBlock(tail(output, 64<<10))))
	}
	return out
}

// cellOutput: the output is a string or a list of content items. The first text is a header
// ("Script completed|failed|terminated|running with cell ID N\nWall time …\nOutput:\n"); only its first line is worth
// keeping, and only when it isn't the plain "Script completed".
func cellOutput(raw json.RawMessage) (string, bool) {
	var parts []string
	var s string
	if json.Unmarshal(raw, &s) == nil {
		parts = []string{s}
	} else {
		var items []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		_ = json.Unmarshal(raw, &items)
		for _, it := range items {
			switch it.Type {
			case "input_text", "output_text", "text":
				parts = append(parts, it.Text)
			case "input_image":
				parts = append(parts, "[image]")
			}
		}
	}
	if len(parts) == 0 {
		return "", false
	}
	head, rest, _ := strings.Cut(parts[0], "\n")
	failed := false
	if strings.HasPrefix(head, "Script ") {
		failed = head == "Script failed" || head == "Script terminated"
		// The header's own body (after "Output:") is usually empty; keep anything that isn't the boilerplate.
		var keep []string
		for _, ln := range strings.Split(rest, "\n") {
			if ln != "" && !strings.HasPrefix(ln, "Wall time ") && ln != "Output:" {
				keep = append(keep, ln)
			}
		}
		parts[0] = strings.Join(keep, "\n")
		if head != "Script completed" {
			parts = append([]string{head}, parts...)
		}
	}
	var nonEmpty []string
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			nonEmpty = append(nonEmpty, p)
		}
	}
	return strings.Join(nonEmpty, "\n"), failed
}

// firstCodeLine: the first meaningful line of a cell, for the card title (skips the `// @exec:` pragma and blanks).
func firstCodeLine(code string) string {
	for _, ln := range strings.Split(code, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" || strings.HasPrefix(ln, "// @exec:") {
			continue
		}
		if utf8.RuneCountInString(ln) > 80 {
			r := []rune(ln)
			ln = string(r[:80]) + "…"
		}
		return ln
	}
	return ""
}

// rollEvent is a call / output line with its time, for merging into a replay.
type rollEvent struct {
	at   int64 // unix ms
	line rollLine
}

func rolloutCalls(path string) []rollEvent {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []rollEvent
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 256<<20)
	for sc.Scan() {
		l, ok := parseRollLine(sc.Bytes())
		if !ok {
			continue
		}
		t, err := time.Parse(time.RFC3339Nano, l.Timestamp)
		if err != nil {
			continue
		}
		out = append(out, rollEvent{at: t.UnixMilli(), line: l})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].at < out[j].at })
	return out
}

// replayTurns replays history with the exec / wait cards slotted in by time: each card goes before the first item
// that started after it (its nested tool calls start after the cell does).
func (a *Agent) replayTurns(ctx context.Context, s *session, turns []turn) {
	var evs []rollEvent
	if p := a.rolloutPath(s.id); p != "" {
		evs = rolloutCalls(p)
	}
	rt := newRollTail(s.id, false)
	flush := func(until int64) {
		for len(evs) > 0 && (until < 0 || evs[0].at <= until) {
			a.rollEmit(ctx, s, rt, evs[0].line)
			evs = evs[1:]
		}
	}
	for _, t := range turns {
		for _, raw := range t.Items {
			var h struct {
				StartedAtMs int64 `json:"startedAtMs"`
			}
			_ = json.Unmarshal(raw, &h)
			if h.StartedAtMs > 0 {
				flush(h.StartedAtMs)
			}
			a.replayItem(ctx, s, raw)
		}
	}
	flush(-1)
	// Calls whose output never landed (interrupted): close them like rollEndTurn does.
	for id, c := range rt.open {
		a.out(ctx, s, acp.UpdateToolCall(acp.ToolCallId(id), acp.WithUpdateStatus(acp.ToolCallStatusFailed), acp.WithUpdateContent(callContent(c, ""))))
	}
}
