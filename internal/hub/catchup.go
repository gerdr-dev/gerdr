package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/coder/acp-go-sdk"

	"github.com/gerdr-dev/gerdr/acpx"
	"github.com/gerdr-dev/gerdr/chatlog"
	"github.com/gerdr-dev/gerdr/internal/agenthost"
)

// ---- 追上别处的进度 ----
//
// gerdr 的对话日志只记它自己经手的事（以及导入那一刻从 agent 记录里重放的）。会话在别处（herdr / 终端里的 claude）
// 接着聊过，agent 的记录文件长了，gerdr 这边却还停在原地 —— 人看到的是一段过时的对话（用户报的「追不上 herdr」）。
// 打开会话时（client 读日志那一下）查一次：文件比上次对齐时大、而我们这边没在跑它，就从记录整份重新载入。

func (s *Session) transcriptStat(ctx context.Context, since int64) (acpx.TranscriptStatResponse, bool) {
	c, err := s.h.host(ctx, s.rec.Agent)
	if err != nil {
		return acpx.TranscriptStatResponse{}, false
	}
	s.mu.Lock()
	sid, cwd := s.rec.AgentSessionID, s.rec.Cwd
	s.mu.Unlock()
	st, err := agenthost.Call[acpx.TranscriptStatResponse](ctx, c, acpx.MethodTranscriptStat, acpx.TranscriptStatRequest{SessionId: acp.SessionId(sid), Cwd: cwd, Since: since})
	return st, err == nil
}

func (s *Session) markSynced(delay time.Duration) {
	time.Sleep(delay)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if st, ok := s.transcriptStat(ctx, -1); ok {
		s.mu.Lock()
		changed := s.rec.SyncedSize != st.Size
		s.rec.SyncedSize = st.Size
		s.mu.Unlock()
		if changed {
			s.h.saveIndex()
		}
	}
}

// CatchUp：别处接着聊过就重新载入。打开会话时调（异步，不挡读日志）。
//
// 判据不是「文件变大了」，而是「文件里多出了 gerdr 没发过的人话」：文件变大还有一堆自己的原因 —— 自己一轮刚完、
// 还没来得及记大小就重启了（用户报的误报：明明没在别处聊过，却提示「别处接着聊过」）、claude 一轮之后补写的收尾、
// 切模式写的那一行。所以让 agent 把上次对齐之后记录里人说的话列出来，和自己日志里的比：全是自己发过的，只更新位置。
func (s *Session) CatchUp(ctx context.Context) {
	s.mu.Lock()
	// 同一个会话半分钟内只查一次：切 tab、几台设备同时开着，都会触发
	busy := s.running || s.catchingUp || s.adopting || time.Since(s.lastCatch) < 30*time.Second
	if !busy {
		s.lastCatch = time.Now()
	}
	synced := s.rec.SyncedSize
	s.mu.Unlock()
	if busy {
		return
	}
	st, ok := s.transcriptStat(ctx, synced)
	if !ok {
		return // codex 之外的外部 agent：没这个口，不管
	}
	if synced > 0 && st.Size == synced {
		return
	}
	mine := s.userTextSet()
	foreign := 0
	for _, t := range st.NewUserTexts {
		if !mine[normText(t)] {
			foreign++
		}
	}
	s.h.log.Debug("catch up", "session", s.rec.ID, "size", st.Size, "synced", synced, "new", len(st.NewUserTexts), "foreign", foreign)
	if foreign == 0 {
		s.mu.Lock()
		s.rec.SyncedSize = st.Size
		s.mu.Unlock()
		s.h.saveIndex()
		return
	}
	if err := s.Reload(ctx); err != nil {
		s.h.log.Warn("catch up", "session", s.rec.ID, "err", err)
		return
	}
	s.emit(&Event{Kind: EvNotice, Level: "info", Text: fmt.Sprintf("这个会话在别处（herdr / 终端）接着聊过（%d 条消息），已经从 agent 自己的记录同步到最新", foreign)})
}

// adoptOrphanTurn：gerdr 重启时日志里有一轮没收尾（recoverAfterRestart 补了「中断」），核对它是不是真断了。
//
// gerdr 被强杀 / 崩溃 / App 退出时等不及（Electron 3 秒就 SIGKILL，claude 的 stop 也要等 3 秒）时，
// 它起的 claude 成了孤儿：stdin 断了，但会把这一轮跑完、写进自己的记录才退。这时 gerdr 的日志停在半截。
// CatchUp 救不了：它只认「多出了 gerdr 没发过的人话」，这里多出来的全是 agent 的回复，它只会把位置挪过去。
//
// 做法：孤儿还活着（stream-json 进程也登记 <配置目录>/sessions/<pid>.json，holders 认得出，重启后已不算「自己起的」）
// 就等它退；之后记录文件在 gerdr 最后一条日志之后还被写过（超出日志落盘的那 2 秒间隔），就从记录整份重放。
// 没写过 = 真断了，「中断」留着。
func (s *Session) adoptOrphanTurn(lastTS int64) {
	s.mu.Lock()
	s.adopting = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.adopting = false
		s.mu.Unlock()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	defer cancel()
	c, err := s.h.host(ctx, s.rec.Agent)
	if err != nil {
		return
	}
	told := false
	for {
		hs := s.heldElsewhere(ctx, c)
		if len(hs) == 0 {
			break
		}
		if !told {
			told = true
			s.emit(&Event{Kind: EvNotice, Level: "info", Text: fmt.Sprintf("Gerdr 重启前的这一轮还在后台跑（claude，pid %d），跑完会自动同步过来", hs[0].Pid)})
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
	st, ok := s.transcriptStat(ctx, -1)
	// 日志每 2 秒落一次盘（flusher），被强杀时最后这一段本来就丢；只差这么点的不算「接着跑过」
	if !ok || st.ModTime <= lastTS+5000 {
		return
	}
	s.mu.Lock()
	running := s.running
	s.mu.Unlock()
	if running {
		// 人已经在新的一轮里了（比如在「别处开着」里把孤儿关掉、接着说话）：这时重放会丢掉正在跑的这一轮，不动
		s.h.log.Warn("adopt orphan turn: session running, skip reload", "session", s.rec.ID)
		return
	}
	if err := s.Reload(ctx); err != nil {
		s.h.log.Warn("adopt orphan turn", "session", s.rec.ID, "err", err)
		return
	}
	// 没见到孤儿也可能走到这儿：这一轮在 gerdr 死之前已经跑完，只是日志还没落盘（或者 gerdr 停了很久才起来）
	text := "Gerdr 重启前这一轮有一段没记下来，已经从 agent 自己的记录补上"
	if told {
		text = "Gerdr 重启后这一轮在后台跑完了，已经从 agent 自己的记录同步到最新"
	}
	s.emit(&Event{Kind: EvNotice, Level: "info", Text: text})
}

// userChunk：从 agent 记录重放的一块用户消息（user_message_chunk），取出它的 messageId 和内容块。
func userChunk(e *Event) (string, json.RawMessage, bool) { return chatlog.UserChunk(e) }

// userTextSet：gerdr 日志里人说过的话（自己发的 + 导入 / 重新载入时从记录重放的），归一化过。
func (s *Session) userTextSet() map[string]bool {
	s.mu.Lock()
	s.loadLocked()
	evs := append([]*Event(nil), s.events...)
	s.mu.Unlock()
	out := map[string]bool{}
	for _, e := range evs {
		switch e.Kind {
		case EvUser:
			var parts []string
			for _, c := range e.Content {
				var b struct {
					Type string `json:"type"`
					Text string `json:"text"`
				}
				if json.Unmarshal(c, &b) == nil && b.Type == "text" {
					parts = append(parts, b.Text)
				}
			}
			out[normText(strings.Join(parts, "\n"))] = true
			for _, p := range parts { // 带图的消息，记录里可能只剩文字那块
				out[normText(p)] = true
			}
		case EvUpdate:
			var u struct {
				SessionUpdate string `json:"sessionUpdate"`
				Content       struct {
					Text string `json:"text"`
				} `json:"content"`
			}
			if json.Unmarshal(e.Update, &u) == nil && u.SessionUpdate == "user_message_chunk" {
				out[normText(u.Content.Text)] = true
			}
		}
	}
	return out
}

// normText：比对用。空白折成一个空格、只看前 200 个字（两边对长消息的存法可能差在末尾的换行 / 截断）。
func normText(t string) string {
	t = strings.Join(strings.Fields(t), " ")
	if r := []rune(t); len(r) > 200 {
		t = string(r[:200])
	}
	return t
}

// Reload：丢掉 gerdr 这边的对话日志，从 agent 的记录整份重放（LoadSession）。
func (s *Session) Reload(ctx context.Context) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return errors.New("先停下这一轮再重新载入")
	}
	if s.catchingUp {
		s.mu.Unlock()
		return nil
	}
	s.catchingUp = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.catchingUp = false
		s.mu.Unlock()
	}()
	c, err := s.h.host(ctx, s.rec.Agent)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.events, s.loaded = nil, true
	s.lastErr = ""
	rec := s.rec
	s.mu.Unlock()
	if err := s.lf.Rewrite(nil); err != nil {
		s.h.log.Warn("rewrite log", "err", err)
	}
	s.h.broadcastEvents(rec.ID, []*Event{}, true)
	meta := map[string]any{}
	if rec.Mode != "" {
		meta["mode"] = rec.Mode
	}
	if rec.Fork != nil {
		meta["fork"] = rec.Fork
	}
	res, err := c.Conn.LoadSession(ctx, acp.LoadSessionRequest{SessionId: acp.SessionId(rec.AgentSessionID), Cwd: rec.Cwd, McpServers: []acp.McpServer{}, Meta: acpx.Meta(meta)})
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.attached, s.shut = true, false
	if res.Modes != nil {
		s.modes = res.Modes
	}
	if len(res.ConfigOptions) > 0 {
		s.config = res.ConfigOptions
	}
	s.mu.Unlock()
	go s.markSynced(0)
	s.setStatus()
	return nil
}

// isSteer：这条消息是不是一条插话（只有插话才有「排队中」，正常开一轮的那句不显示）。
func (s *Session) isSteer(msgID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadLocked()
	for i := len(s.events) - 1; i >= 0; i-- {
		if e := s.events[i]; e.Kind == EvUser && e.MessageID == msgID {
			return e.Steer
		}
	}
	return false
}

// num reads a number out of _meta: float64 once it has been through JSON, the Go type when an adapter runs in-process.
func num(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int:
		return int64(n)
	case int64:
		return n
	}
	return 0
}
