package native

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/coder/acp-go-sdk"
	"github.com/google/uuid"

	"github.com/gerdr-dev/gerdr/acpx"
	"github.com/gerdr-dev/gerdr/agents/native/llm"
	"github.com/gerdr-dev/gerdr/internal/i18n"
)

// ---- Compaction ----
//
// Automatic compaction must never stop the session. In a real session (524k window, ~480k tokens of history)
// the summary request failed over and over: upstream 500s, and 200s whose stream held nothing (an empty
// summary). Each failure ended the turn with an error the client showed as a network error, the client sent
// "continue", and the next turn sent the same oversized summary request again: 70+ times, never compacted.
//
// So a failure steps down instead of repeating:
//  1. the history as it is (it shares the cached prefix with the last request) — skipped when the estimate says
//     it leaves no room in the window for the summary itself;
//  2. a smaller copy (shrinkForSummary): big tool results / arguments cut to head and tail, images and reasoning
//     left out, and if that is still over half the window, the oldest messages after the first one left out;
//  3. automatic compaction only: no summary at all — the oldest messages are dropped locally (dropOldest), keeping
//     the first request and the most recent messages (entry.Keep), so the next request is a fraction of the last
//     and maybeCompact doesn't fire again until the context really fills up again.
//
// An empty summary counts as a failure: replacing the history with nothing loses all of it. Whatever happened
// is told to the user as what it was (a notice with the real error, and on the compaction card), not as a failed
// turn. /compact typed by the user gets steps 1–2 and reports the error: they asked for a summary, and silently
// dropping messages instead is not what they asked for.

const compactPrompt = `Summarize the conversation so far so that work can continue from the summary alone (the earlier messages will be dropped). Include:
1. The user's requests and intent, in order, quoting important instructions verbatim.
2. Key technical context: files read or changed (with paths), important code, commands run and their results, errors and how they were fixed.
3. Decisions made and constraints learned.
4. What was being worked on right before this summary, and the next step — exactly where to pick up.
Be thorough but concise. Reply with the summary only.`

var errEmptySummary = errors.New("compaction returned an empty summary")

const (
	// imageChars: what an image counts as when estimating a request's size, in characters (an image is ~1.5k
	// tokens on most models, a character of mixed code / prose ~0.3).
	imageChars = 5000
	// shrinkResult / shrinkText: in the smaller summary request, tool results / tool arguments and other long
	// texts keep this many characters (head and tail). The last shrinkKeepLast messages stay whole: they are
	// what the summary's "where to pick up" is about.
	shrinkResult   = 2000
	shrinkText     = 8000
	shrinkKeepLast = 4
	// firstMax: the first message kept by shrinkForSummary / quoted by dropOldest (the original request or the
	// previous summary) is cut to this many characters.
	firstMax = 16000
)

// maybeCompact：上一次请求的输入超过阈值就先压缩再接着跑。
func (r *run) maybeCompact(ctx context.Context) error {
	r.s.mu.Lock()
	n := r.s.lastInput
	r.s.mu.Unlock()
	if n == 0 || n < r.model.compactAt() {
		return nil
	}
	return r.compact(ctx, "", true)
}

// compact 让模型把目前的对话总结成一段，之后的上下文从摘要开始（store.go 的 buildContext）。
// 界面上是一张「压缩上下文」的卡片。auto: started by maybeCompact, which may fall back to dropping messages.
// Returns an error only when stopped, or for /compact (auto false) when no summary could be made.
func (r *run) compact(ctx context.Context, extra string, auto bool) error {
	id := acp.ToolCallId(acpx.CompactIDPrefix + uuid.NewString())
	r.out(acp.StartToolCall(id, "Compacting context", acp.WithStartKind(acp.ToolKindThink), acp.WithStartStatus(acp.ToolCallStatusInProgress)))
	msgs := r.history()
	if len(msgs) == 0 {
		r.out(acp.UpdateToolCall(id, acp.WithUpdateStatus(acp.ToolCallStatusCompleted)))
		return nil
	}
	instr := compactPrompt
	if extra != "" {
		instr += "\n\nAdditional instructions from the user: " + extra
	}
	m := r.model
	if sm := r.s.a.config().SmallModel; sm != "" {
		if x, ok := r.s.a.config().find(sm); ok && x.Key == sm {
			m = x
		}
	}
	window := m.Window
	if window <= 0 {
		window = r.model.Window
	}
	r.s.mu.Lock()
	scale, pre := r.s.charScale, r.s.lastInput
	r.s.mu.Unlock()
	if scale <= 0 {
		scale = 0.5 // nothing measured yet: rather overestimate (CJK text is ~1 token a character)
	}

	var errs []error
	summary, post := "", 0
	try := func(hist []llm.Message) bool {
		req := append(append([]llm.Message(nil), hist...), llm.Message{Role: "user", Content: []llm.Part{llm.TextPart(instr)}})
		res, err := m.client().Stream(ctx, llm.Request{Model: m.ID, System: r.system, Messages: req, Thinking: m.Thinking, Effort: lowestEffort(m), CacheKey: r.cacheKey()}, func(llm.Event) {})
		r.s.addUsage(m.Key, r.agentID(), res.Usage)
		if err == nil && strings.TrimSpace(res.Message.Text) == "" {
			err = errEmptySummary
		}
		if err != nil {
			errs = append(errs, err)
			return false
		}
		summary, post = res.Message.Text, res.Usage.Output
		return true
	}
	ok, first := false, false
	if room := summaryRoom(window); room <= 0 || estimate(r.system, msgs, scale) <= room {
		first = true
		ok = try(msgs)
	}
	if !ok && ctx.Err() == nil {
		target := 0
		if window > 0 {
			target = window / 2
		}
		if small, changed := shrinkForSummary(msgs, scale, target); changed || !first {
			ok = try(small)
		}
	}
	if ctx.Err() != nil {
		r.out(acp.UpdateToolCall(id, acp.WithUpdateStatus(acp.ToolCallStatusFailed)))
		return ctx.Err()
	}

	if ok {
		if len(errs) > 0 {
			r.notice(fmt.Sprintf(i18n.S("压缩上下文：第一次总结失败（%s），裁掉大的工具结果和图片后重试成功。",
				"Compacting the context: the first summary attempt failed (%s); it worked after leaving out large tool results and images."), errText(errs[0])))
		}
		r.commitCompact(entry{T: "compact", At: time.Now().UnixMilli(), Summary: summary})
		u := acp.UpdateToolCall(id, acp.WithUpdateStatus(acp.ToolCallStatusCompleted),
			acp.WithUpdateContent([]acp.ToolCallContent{acp.ToolContent(acp.TextBlock(summary))}))
		u.ToolCallUpdate.Meta = acpx.CompactMeta(pre, post, r.model.Window)
		r.out(u)
		// Tell the composer the context shrank: after a /compact no request follows to report it, and the old
		// percentage stayed up as if nothing happened. The summary's size is a lower bound (the system prompt and
		// tools come on top); the next request reports the exact figure.
		if post > 0 && r.sub == nil {
			r.s.send(acp.SessionUpdate{SessionInfoUpdate: &acp.SessionSessionInfoUpdate{Meta: acpx.ContextProgress(post, r.model.Window)}})
		}
		return nil
	}

	why := errText(errs[len(errs)-1])
	if !auto {
		r.out(acp.UpdateToolCall(id, acp.WithUpdateStatus(acp.ToolCallStatusFailed),
			acp.WithUpdateContent([]acp.ToolCallContent{acp.ToolContent(acp.TextBlock(why))})))
		return fmt.Errorf("compact context: %w", errs[len(errs)-1])
	}
	keep, text := dropOldest(msgs, scale, r.model.compactAt()/4, why)
	r.commitCompact(entry{T: "compact", At: time.Now().UnixMilli(), Summary: text, Keep: keep})
	note := fmt.Sprintf(i18n.S("自动压缩上下文失败（%s）。已退而求其次：不做摘要，直接丢掉最早的 %d 条消息，保留最初的请求和最近的 %d 条，接着往下做。",
		"Compacting the context failed (%s). Fell back to dropping the oldest %d messages without a summary, keeping the original request and the latest %d; carrying on."),
		why, len(msgs)-keep, keep)
	u := acp.UpdateToolCall(id, acp.WithUpdateStatus(acp.ToolCallStatusFailed),
		acp.WithUpdateContent([]acp.ToolCallContent{acp.ToolContent(acp.TextBlock(note))}))
	u.ToolCallUpdate.Meta = acpx.CompactMeta(pre, 0, r.model.Window)
	r.out(u)
	r.notice(note)
	return nil
}

// commitCompact records a compact entry and forgets the last request's size (it described the old context).
func (r *run) commitCompact(e entry) {
	r.s.mu.Lock()
	r.s.entries = append(r.s.entries, e)
	r.s.lastInput = 0
	sid := r.s.id
	r.s.mu.Unlock()
	_ = r.s.a.st.append(sid, e)
}

func errText(err error) string { return oneLine(err.Error(), 300) }

// summaryRoom: the most input tokens a summary request may have and still leave the window room for the summary
// (a 16th of the window, at most 32k). 0 = window unknown, no limit.
func summaryRoom(window int) int {
	if window <= 0 {
		return 0
	}
	return window - min(window/16, 32_000)
}

// ---- Estimating a request's size ----

func msgChars(m llm.Message) int {
	n := len(m.Text)
	for _, p := range m.Content {
		if p.Type == "image" {
			n += imageChars
		} else {
			n += len(p.Text)
		}
	}
	for _, t := range m.Reasoning {
		n += len(t.Text) + len(t.Encrypted)
	}
	for _, c := range m.ToolCalls {
		n += len(c.Name) + len(c.Args)
	}
	return n
}

// histChars: the characters a request sends (system prompt + messages; tool definitions are left out, they are a
// constant overhead that the measured scale then spreads over the text).
func histChars(system string, ms []llm.Message) int {
	n := len(system)
	for _, m := range ms {
		n += msgChars(m)
	}
	return n
}

// estimate: a request's input tokens, from its characters and the scale the last request measured.
func estimate(system string, ms []llm.Message, scale float64) int {
	return int(float64(histChars(system, ms)) * scale)
}

func msgTokens(m llm.Message, scale float64) int { return int(float64(msgChars(m)) * scale) }

// ---- Step 2: a smaller summary request ----

// shrinkForSummary returns a copy of msgs small enough to summarize: long texts cut (all but the last few
// messages), images and reasoning left out; then, if it is still over target tokens (0 = no target), the oldest
// messages after the first one left out, with a note saying so. changed: anything was cut. msgs is not modified.
func shrinkForSummary(msgs []llm.Message, scale float64, target int) ([]llm.Message, bool) {
	out := make([]llm.Message, len(msgs))
	copy(out, msgs)
	changed := false
	for i := 0; i < len(out)-shrinkKeepLast; i++ {
		if m, c := shrinkMsg(out[i]); c {
			out[i], changed = m, true
		}
	}
	if target <= 0 || estimate("", out, scale) <= target || len(out) < 3 {
		return out, changed
	}
	// Leave out the oldest messages after the first (the original request, or the previous summary).
	head := out[0]
	rest, total := out[1:], estimate("", out, scale)
	j := 0
	for j < len(rest)-1 && total > target {
		total -= msgTokens(rest[j], scale)
		j++
	}
	for j < len(rest) && rest[j].Role == "tool" { // results whose call was left out can't stay
		j++
	}
	if j == 0 {
		return out, changed
	}
	note := llm.TextPart(fmt.Sprintf("[%d messages after this one were left out here to keep this summary request within the context window.]", j))
	if head.Role == "user" {
		head.Content = append(cutParts(head.Content, firstMax), note)
		return append([]llm.Message{head}, rest[j:]...), true
	}
	return append([]llm.Message{{Role: "user", Content: []llm.Part{note}}}, rest[j:]...), true
}

// shrinkMsg cuts one message for the summary request. Reasoning goes (as does Blocks, the verbatim copy that would
// be sent instead of the edited fields); tool arguments too long are replaced by a JSON note, since a cut JSON
// string would be rejected.
func shrinkMsg(m llm.Message) (llm.Message, bool) {
	changed := false
	if len(m.Reasoning) > 0 || len(m.Blocks) > 0 {
		m.Reasoning, m.Blocks, changed = nil, nil, true
	}
	limit := shrinkText
	if m.Role == "tool" {
		limit = shrinkResult
	}
	if len(m.Text) > limit {
		m.Text, changed = headTail(m.Text, limit), true
	}
	for _, p := range m.Content {
		if p.Type == "image" || len(p.Text) > limit {
			m.Content, changed = cutParts(m.Content, limit), true
			break
		}
	}
	if len(m.ToolCalls) > 0 {
		calls := make([]llm.ToolCall, len(m.ToolCalls))
		copy(calls, m.ToolCalls)
		for i, c := range calls {
			if len(c.Args) > shrinkResult {
				calls[i].Args, changed = fmt.Sprintf(`{"omitted":"%d characters of arguments left out"}`, len(c.Args)), true
			}
		}
		m.ToolCalls = calls
	}
	return m, changed
}

// cutParts: a copy of ps with texts cut to n characters and images replaced by a short note.
func cutParts(ps []llm.Part, n int) []llm.Part {
	out := make([]llm.Part, 0, len(ps))
	for _, p := range ps {
		switch {
		case p.Type == "image":
			out = append(out, llm.TextPart("[image left out]"))
		case len(p.Text) > n:
			p.Text = headTail(p.Text, n)
			out = append(out, p)
		default:
			out = append(out, p)
		}
	}
	return out
}

// ---- Step 3: dropping the oldest messages ----

// dropOldest decides what survives when no summary could be made: the most recent messages that fit in budget
// tokens (keep, counted from the end of msgs; never all of them, never starting with tool results whose call is
// gone), and the text that stands in for the summary — what happened, plus the first message of the dropped part
// (the original request or the previous summary) and the last thing the user said in it.
func dropOldest(msgs []llm.Message, scale float64, budget int, why string) (int, string) {
	keep, total := 0, 0
	for i := len(msgs) - 1; i >= 1; i-- {
		t := msgTokens(msgs[i], scale)
		if total+t > budget {
			break
		}
		total += t
		keep++
	}
	for keep > 0 && msgs[len(msgs)-keep].Role == "tool" {
		keep--
	}
	dropped := msgs[:len(msgs)-keep]
	var b strings.Builder
	fmt.Fprintf(&b, "Summarizing the earlier part of this conversation failed (%s), so its oldest %d messages were dropped without a summary. "+
		"The messages after this one are the most recent part of the conversation. Details from the dropped part (files read, command results, "+
		"decisions) are gone: check the current state of files and tasks again rather than assuming, then carry on with the task.", why, len(dropped))
	firstIdx := -1
	for i, m := range dropped {
		if m.Role == "user" {
			firstIdx = i
			fmt.Fprintf(&b, "\n\nThe first message of the dropped part (the original request, or the summary of what came before it):\n%s",
				headTail(strings.TrimPrefix(userText(m), summaryPrefix), firstMax))
			break
		}
	}
	for i := len(dropped) - 1; i > firstIdx && firstIdx >= 0; i-- {
		if m := dropped[i]; m.Role == "user" {
			if t := strings.TrimSpace(userText(m)); t != "" && !strings.HasPrefix(t, "<system-reminder>") {
				fmt.Fprintf(&b, "\n\nThe last user message in the dropped part:\n%s", headTail(t, shrinkText))
				break
			}
		}
	}
	return keep, b.String()
}
