package hub

import (
	"bufio"
	"encoding/json"
	"os"
	"time"
)

// Event 是一个会话的日志里的一条。前端拿到整份日志自己归并成界面（纯函数），
// 所以这里是**唯一的事实来源**：刷新页面、换一台设备、gerdr 重启，看到的都一样。
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
)

func now() int64 { return time.Now().UnixMilli() }

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

// logFile 是一个会话的日志文件（JSONL，每行一个 Event）。
type logFile struct {
	path    string
	flushed int // events[:flushed] 已经落盘
}

func readLog(path string) ([]*Event, error) {
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

func (l *logFile) append(events []*Event) error {
	if len(events) == 0 {
		return nil
	}
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
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

func (l *logFile) rewrite(events []*Event) error {
	tmp := l.path + ".tmp"
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
	l.flushed = len(events)
	return os.Rename(tmp, l.path)
}
