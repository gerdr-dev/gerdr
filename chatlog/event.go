package chatlog

import (
	"bufio"
	"encoding/json"
	"os"
)

// Event is one entry in a session's log. The UI (@gerdr/chat) folds the whole log into what it shows with pure
// functions, so the log is the **single source of truth**: a reload, another device or a restarted server all see
// the same conversation. Seq orders events across sessions; TS is unix ms. Field use per Kind is in EVENTS.md.
type Event struct {
	Seq  int64  `json:"seq"`
	TS   int64  `json:"ts"` // unix ms
	Kind string `json:"kind"`

	// user
	MessageID string            `json:"messageId,omitempty"`
	Content   []json.RawMessage `json:"content,omitempty"` // ACP ContentBlock
	Steer     bool              `json:"steer,omitempty"`
	From      string            `json:"from,omitempty"` // 别的会话（agent）经 gerdr session send 发来的：发送方的会话 id

	// update：原样的 ACP SessionUpdate
	Update json.RawMessage `json:"update,omitempty"`

	// turn_start / turn_end：Auto = 这一轮是 agent 自己开的（后台任务跑完了在汇报），前面没有用户消息
	Auto       bool   `json:"auto,omitempty"`
	StopReason string `json:"stopReason,omitempty"`
	// turn_end：这一轮用了多久、输出了多少 token（对话里那行「用时 1m 5s · ↓ 2.2k tokens」）
	DurationMs   int64  `json:"durationMs,omitempty"`
	OutputTokens int    `json:"outputTokens,omitempty"`
	Error        string `json:"error,omitempty"`

	// permission / ask 及其 _done
	ReqID    string          `json:"reqId,omitempty"`
	Request  json.RawMessage `json:"request,omitempty"`
	Response json.RawMessage `json:"response,omitempty"`

	// task
	Task json.RawMessage `json:"task,omitempty"`

	// notice：Level 是 info / warn / error / clear / retry（界面上 agent 再出字就收起的「N 秒后重试」）
	Level string `json:"level,omitempty"`
	Text  string `json:"text,omitempty"`
	// notice retry: when the retry fires (unix ms), so the UI can count the "N 秒后" in Text down instead of leaving it frozen
	Until int64 `json:"until,omitempty"`

	// shell_done：命令的退出码（0 也要记，所以是指针）
	Exit *int `json:"exit,omitempty"`
}

const (
	EvUser           = "user"
	EvUpdate         = "update"
	EvTurnStart      = "turn_start"
	EvTurnEnd        = "turn_end"
	EvPermission     = "permission"
	EvPermissionDone = "permission_done"
	EvAsk            = "ask"
	EvAskDone        = "ask_done"
	EvNotice         = "notice"
	EvSteerState     = "steer_state" // 插话在 agent 队列里的进度（MessageID + Text：queued / started）
	EvTask           = "task"        // 一个后台任务 / 子 agent 结束了：留一条记录在对话里（任务栏只放在跑的）

	// gerdr only: a `!command` the user ran from the composer.
	EvShell     = "shell"      // MessageID = 这次运行的 id，Text = 命令
	EvShellOut  = "shell_out"  // Text = 一段输出（stdout、stderr 混在一起，按出现的先后）
	EvShellDone = "shell_done" // Exit = 退出码；Level = stopped（人点了停止）/ lost（gerdr 重启时还在跑）
)

// Add appends e to events, merging a streaming text chunk into the previous event when both are chunks of the same
// message and the previous one isn't on disk yet (index >= flushed): a turn's thousands of token-sized chunks
// end up as a few events, so files and first loads are an order of magnitude smaller. A merged event is a new
// object (the old pointer may still be being serialised elsewhere) carrying e's Seq. merged reports whether e was
// folded in. Either way, live clients get e itself: the UI folds consecutive chunks of a message the same way.
func Add(events []*Event, flushed int, e *Event) (out []*Event, merged bool) {
	if e.Kind == EvUpdate {
		if key, text, ok := chunkKey(e.Update); ok {
			if n := len(events); n > 0 && n > flushed {
				last := events[n-1]
				if last.Kind == EvUpdate {
					if k2, _, ok2 := chunkKey(last.Update); ok2 && k2 == key {
						cp := *last
						cp.Update = mergeChunk(last.Update, text)
						cp.Seq = e.Seq
						events[n-1] = &cp
						return events, true
					}
				}
			}
		}
	}
	return append(events, e), false
}

// chunkKey：能和前一条合并的流式片段（同一条消息的正文 / 思考）返回非空 key。
// 合并之后一轮几千个 token 级的片段在日志里只剩几条，文件和首屏都小一个量级。
func chunkKey(raw json.RawMessage) (key, text string, ok bool) {
	var u struct {
		SessionUpdate string `json:"sessionUpdate"`
		MessageID     string `json:"messageId"`
		Content       struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Meta json.RawMessage `json:"_meta"`
	}
	if json.Unmarshal(raw, &u) != nil {
		return "", "", false
	}
	if (u.SessionUpdate != "agent_message_chunk" && u.SessionUpdate != "agent_thought_chunk") || u.Content.Type != "text" || len(u.Meta) > 0 {
		return "", "", false
	}
	return u.SessionUpdate + "\x00" + u.MessageID, u.Content.Text, true
}

func mergeChunk(prev json.RawMessage, more string) json.RawMessage {
	var m map[string]any
	if json.Unmarshal(prev, &m) != nil {
		return prev
	}
	c, _ := m["content"].(map[string]any)
	if c == nil {
		return prev
	}
	t, _ := c["text"].(string)
	c["text"] = t + more
	b, _ := json.Marshal(m)
	return b
}

// File is a session's log on disk: JSONL, one Event per line. Flushed counts the events already written
// (events[:Flushed]); Append adds the rest, Rewrite replaces the file (rewind, withdraw).
type File struct {
	Path    string
	Flushed int
}

// ReadFile loads a log written by File (a missing file is an empty log).
func ReadFile(path string) ([]*Event, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []*Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 128<<20)
	for sc.Scan() {
		var e Event
		if json.Unmarshal(sc.Bytes(), &e) == nil {
			out = append(out, &e)
		}
	}
	return out, sc.Err()
}

// Append writes events at the end of the file.
func (l *File) Append(events []*Event) error {
	if len(events) == 0 {
		return nil
	}
	f, err := os.OpenFile(l.Path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	for _, e := range events {
		b, _ := json.Marshal(e)
		w.Write(b)
		w.WriteByte('\n')
	}
	return w.Flush()
}

// Rewrite replaces the whole file with events (via a temp file and a rename).
func (l *File) Rewrite(events []*Event) error {
	tmp := l.Path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	for _, e := range events {
		b, _ := json.Marshal(e)
		w.Write(b)
		w.WriteByte('\n')
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	f.Close()
	l.Flushed = len(events)
	return os.Rename(tmp, l.Path)
}

// IsChunk reports a streaming text chunk that a following one may still be merged into (Add): a writer that
// flushes while a turn runs holds the last one back.
func IsChunk(raw json.RawMessage) bool {
	_, _, ok := chunkKey(raw)
	return ok
}
