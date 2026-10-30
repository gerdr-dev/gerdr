package chatlog

import (
	"encoding/json"
	"errors"

	"github.com/gerdr-dev/gerdr/acpx"
)

// The rules that turn ACP traffic into log events and read the log back (what to withdraw on stop, where a rewind
// cuts). gerdr's hub and Session both use them, so a log means the same thing whoever wrote it.

// NoticeFromInfo is the notice event in a session_info_update's _meta.gerdr ("notice", "noticeLevel" — default
// warn —, "noticeDelayMs"), or nil. The adapter reports how long a retry waits; Until turns it into a deadline so
// the UI can count down (a bare "9 秒后" would stay at 9, and a replayed log would restart the count).
func NoticeFromInfo(g map[string]any, now int64) *Event {
	n, _ := g["notice"].(string)
	if n == "" {
		return nil
	}
	lv, _ := g["noticeLevel"].(string)
	if lv == "" {
		lv = "warn"
	}
	ev := &Event{Kind: EvNotice, Level: lv, Text: n}
	if d := num(g["noticeDelayMs"]); d > 0 {
		ev.Until = now + d
	}
	return ev
}

// SteerFromInfo is an interjection's queue state in _meta.gerdr.steer: id and "queued" | "started".
func SteerFromInfo(g map[string]any) (id, state string, ok bool) {
	st, _ := g["steer"].(map[string]any)
	id, _ = st["id"].(string)
	state, _ = st["state"].(string)
	return id, state, id != "" && (state == "queued" || state == "started")
}

// TasksFromInfo is _meta.gerdr.tasks, the agent's whole task list, if present.
func TasksFromInfo(g map[string]any) ([]acpx.Task, bool) {
	v, ok := g["tasks"]
	if !ok {
		return nil, false
	}
	var list []acpx.Task
	b, _ := json.Marshal(v)
	_ = json.Unmarshal(b, &list)
	return list, true
}

// FinishedTasks: a task event for each task in next that has just ended (was running in prev, or first seen
// already ended). The tasks bar only shows running ones; this leaves a line in the conversation. Tasks started by a
// sub-agent (Parent set) are left to its own panel.
func FinishedTasks(prev, next []acpx.Task) []*Event {
	was := map[string]string{}
	for _, t := range prev {
		was[t.ID] = t.Status
	}
	var out []*Event
	for _, t := range next {
		if t.Status == "running" || t.Parent != "" {
			continue
		}
		if st, seen := was[t.ID]; seen && st != "running" {
			continue
		}
		tb, _ := json.Marshal(t)
		out = append(out, &Event{Kind: EvTask, Task: tb})
	}
	return out
}

// StopPlan reads the running turn (from its last turn_start) for a stop: tail is the user messages to take back —
// those after the agent last did anything, plus interjections still marked queued (an interjection waits for the
// next gap, and the agent keeps writing meanwhile, so "after the last output" alone misses them) — and quiet
// reports a turn in which the agent hasn't done anything yet (stopping it can rewind the message away entirely).
func StopPlan(evs []*Event) (tail []*Event, quiet bool) {
	start := -1
	for i := len(evs) - 1; i >= 0; i-- {
		if evs[i].Kind == EvTurnStart {
			start = i
			break
		}
	}
	lastOut := start
	state := map[string]string{}
	for i := start + 1; i < len(evs); i++ {
		switch evs[i].Kind {
		case EvUpdate, EvPermission, EvAsk, EvTask:
			lastOut = i
		case EvSteerState:
			state[evs[i].MessageID] = evs[i].Text
		}
	}
	for i := start + 1; i < len(evs); i++ {
		if e := evs[i]; e.Kind == EvUser && (i > lastOut || (e.Steer && state[e.MessageID] == "queued")) {
			tail = append(tail, e)
		}
	}
	return tail, start >= 0 && lastOut == start
}

// LastQueued is the id of the last interjection the agent hasn't read yet ("" = none): by its queue state when the
// agent reports one (steer_state), otherwise by whether the agent did anything after it.
func LastQueued(evs []*Event) string {
	state := map[string]string{}
	busy := false
	for i := len(evs) - 1; i >= 0; i-- {
		e := evs[i]
		switch e.Kind {
		case EvSteerState:
			if _, ok := state[e.MessageID]; !ok {
				state[e.MessageID] = e.Text // walking backwards: the first one met is the latest
			}
		case EvUpdate, EvPermission, EvAsk:
			busy = true
		case EvUser:
			if !e.Steer {
				return ""
			}
			if st, ok := state[e.MessageID]; ok {
				if st == "queued" {
					return e.MessageID
				}
				return "" // the last one was read, so were the earlier ones
			}
			if busy {
				return ""
			}
			return e.MessageID
		}
	}
	return ""
}

// ErrBeforeClear: the message is from before a /clear, whose context the agent no longer has.
var ErrBeforeClear = errors.New("这条消息在 /clear 之前：清空之后只能回退到清空以后发的消息")

// ErrNoMessage: no user message with that id in the log.
var ErrNoMessage = errors.New("找不到这条消息")

// RewindPoint finds where rewinding to user message msgID cuts the log: its index, its content (back into the
// composer) and whether it was an interjection. Two kinds of user messages count: the ones sent through the log
// (user events) and the ones replayed from the agent's transcript (user_message_chunk updates, several chunks with
// the same messageId for a message with images).
func RewindPoint(evs []*Event, msgID string) (idx int, content []json.RawMessage, steer bool, err error) {
	idx = -1
	for i, e := range evs {
		if e.Kind == EvUser && e.MessageID == msgID {
			idx, content, steer = i, e.Content, e.Steer
			break
		}
		if id, c, ok := UserChunk(e); ok && id == msgID {
			if idx < 0 {
				idx = i
			}
			content = append(content, c)
		} else if idx >= 0 {
			break
		}
	}
	if idx < 0 {
		return -1, nil, false, ErrNoMessage
	}
	for _, e := range evs[idx:] {
		if e.Kind == EvNotice && e.Level == "clear" {
			return -1, nil, false, ErrBeforeClear
		}
	}
	return idx, content, steer, nil
}

// UserChunk: a user_message_chunk update replayed from an agent's transcript — its message id and content block.
func UserChunk(e *Event) (id string, content json.RawMessage, ok bool) {
	if e.Kind != EvUpdate {
		return "", nil, false
	}
	var u struct {
		SessionUpdate string          `json:"sessionUpdate"`
		MessageID     string          `json:"messageId"`
		Content       json.RawMessage `json:"content"`
	}
	if json.Unmarshal(e.Update, &u) != nil || u.SessionUpdate != "user_message_chunk" || u.MessageID == "" {
		return "", nil, false
	}
	return u.MessageID, u.Content, true
}

// DropUsers removes the user messages with these ids (withdrawn interjections) and returns what's left plus
// their content, in order.
func DropUsers(evs []*Event, ids map[string]bool) (kept []*Event, content []json.RawMessage, n int) {
	kept = evs[:0:0]
	for _, e := range evs {
		if e.Kind == EvUser && ids[e.MessageID] {
			content = append(content, e.Content...)
			n++
			continue
		}
		kept = append(kept, e)
	}
	return kept, content, n
}

func num(v any) int64 {
	switch x := v.(type) {
	case float64:
		return int64(x)
	case int64:
		return x
	case int:
		return int64(x)
	}
	return 0
}
