package hub

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// 推给界面的日志：图片换成地址、打开会话只给最近一段。
//
// 为什么：日志原样推出去，一个会话动辄几 MB 到几十 MB，大头是贴的截图（base64，压缩也压不动），
// 远程（中继 / 手机流量）打开时一帧传不完、被写期限掐断，重连又整份重推，永远打不开。
// 盘上的日志不动（agent 恢复会话、`gerdr session read` 都要原样的），只改推出去的那份。

// TailBudget：打开会话先推的那一段大概多大（换掉图片之后的 JSON 字节）；更早的往上翻再拉（OlderEvents）。
const TailBudget = 256 << 10

// BlobURL 是图片的地址前缀（server 挂 GET /api/blobs/{hash}）。
const BlobURL = "/api/blobs/"

func (h *Hub) blobDir() string { return filepath.Join(h.opt.Dir, "blobs") }

// BlobPath：hash 对应的文件；hash 不像样返回空。
func (h *Hub) BlobPath(hash string) string {
	if len(hash) != 64 {
		return ""
	}
	if _, err := hex.DecodeString(hash); err != nil {
		return ""
	}
	return filepath.Join(h.blobDir(), hash)
}

// slimEvents：每条换掉图片（没有图片的原样返回同一个指针）。
func (h *Hub) slimEvents(evs []*Event) []*Event {
	out := make([]*Event, len(evs))
	for i, e := range evs {
		out[i] = h.slim(e)
	}
	return out
}

// slim：日志里的图片（base64）存进 blobs/<sha256>，原处换成 {"blob": "/api/blobs/<sha256>"}。
// 返回新对象，不改原来那条（它还在内存日志里、也可能正被别的连接序列化）。
// 图片出现在三种地方：用户消息的 ContentBlock {type:image, data, mimeType}、Read 工具的 rawOutput.file {base64, type:"image/png"}、
// Claude 原样的 {source:{type:base64, data, media_type}}。不认具体位置，只认「一个大字符串字段 + 旁边标了图片类型」。
func (h *Hub) slim(e *Event) *Event {
	var cp *Event
	if u, ok := h.slimRaw(e.Update); ok {
		c := *e
		cp = &c
		cp.Update = u
	}
	for i, c := range e.Content {
		s, ok := h.slimRaw(c)
		if !ok {
			continue
		}
		if cp == nil {
			c := *e
			cp = &c
		}
		if len(cp.Content) > 0 && &cp.Content[0] == &e.Content[0] { // 还和原来那条共用底层数组
			cp.Content = append([]json.RawMessage(nil), e.Content...)
		}
		cp.Content[i] = s
	}
	if cp == nil {
		return e
	}
	return cp
}

func (h *Hub) slimRaw(raw json.RawMessage) (json.RawMessage, bool) {
	// 先粗筛：大多数条目没有图片，别每条都解一遍
	if len(raw) < 4096 || !(bytes.Contains(raw, []byte(`"data":"`)) || bytes.Contains(raw, []byte(`"base64":"`))) {
		return raw, false
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if dec.Decode(&v) != nil {
		return raw, false
	}
	if !h.slimValue(v) {
		return raw, false
	}
	b, err := json.Marshal(v)
	if err != nil {
		return raw, false
	}
	return b, true
}

func (h *Hub) slimValue(v any) bool {
	changed := false
	switch x := v.(type) {
	case map[string]any:
		if imageMime(x) {
			for _, k := range []string{"data", "base64"} {
				s, ok := x[k].(string)
				if !ok || len(s) < 1024 {
					continue
				}
				if url := h.saveBlob(s); url != "" {
					delete(x, k)
					x["blob"] = url
					changed = true
				}
			}
		}
		for _, c := range x {
			if h.slimValue(c) {
				changed = true
			}
		}
	case []any:
		for _, c := range x {
			if h.slimValue(c) {
				changed = true
			}
		}
	}
	return changed
}

func imageMime(m map[string]any) bool {
	for _, k := range []string{"mimeType", "media_type", "type"} {
		if s, ok := m[k].(string); ok && strings.HasPrefix(s, "image/") {
			return true
		}
	}
	return m["type"] == "image"
}

// SaveBlob stores an image pasted into the composer so the draft can point other devices at it (hub/draft.go).
func (h *Hub) SaveBlob(b64 string) string { return h.saveBlob(b64) }

// saveBlob：base64 解开存成 blobs/<sha256>（已有就不写），返回地址；解不开返回空（原样留着）。
func (h *Hub) saveBlob(b64 string) string {
	b, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	hash := hex.EncodeToString(sum[:])
	p := filepath.Join(h.blobDir(), hash)
	if _, err := os.Stat(p); err != nil {
		if err := os.MkdirAll(h.blobDir(), 0o700); err != nil {
			return ""
		}
		// 先写临时文件再改名：同时两个连接在存同一张图，不会有人读到写了一半的
		tmp := p + ".tmp" + newID()
		if err := os.WriteFile(tmp, b, 0o600); err != nil {
			return ""
		}
		if err := os.Rename(tmp, p); err != nil {
			os.Remove(tmp)
			return ""
		}
	}
	return BlobURL + hash
}

// evSize：一条日志推出去大概多大（只用来切段，不用准）。
func evSize(e *Event) int {
	n := 64 + len(e.Update) + len(e.Request) + len(e.Response) + len(e.Task) + len(e.Text) + len(e.Error)
	for _, c := range e.Content {
		n += len(c)
	}
	return n
}

// turnHead：从 evs[i] 开始重放，界面上得到的对话和从头重放的一样 —— 一句用户消息（插话除外：它在一轮中间）、
// agent 自己开的一轮（后台任务跑完了汇报），或导入 / 追上来的历史里的一句用户消息（user_message_chunk，
// 同一句可能分几块来：和前一块是同一句的不算开头；子 agent 的不算，主对话里不画它）。
// 界面的 Transcript 在这几处都会另起一段，前面的状态带不过来。
func turnHead(evs []*Event, i int) bool {
	e := evs[i]
	if (e.Kind == EvUser && !e.Steer) || (e.Kind == EvTurnStart && e.Auto) {
		return true
	}
	id, ok := userChunkHead(e)
	if !ok {
		return false
	}
	if i > 0 {
		if prev, ok := userChunkHead(evs[i-1]); ok && prev == id && id != "" {
			return false
		}
	}
	return true
}

// userChunkHead：主对话里的一块 user_message_chunk（返回它的 messageId）。
func userChunkHead(e *Event) (string, bool) {
	if e.Kind != EvUpdate || !bytes.Contains(e.Update, []byte(`"user_message_chunk"`)) {
		return "", false
	}
	var u struct {
		SessionUpdate string `json:"sessionUpdate"`
		MessageID     string `json:"messageId"`
		Meta          struct {
			Gerdr struct {
				Subagent string `json:"subagent"`
			} `json:"gerdr"`
		} `json:"_meta"`
	}
	if json.Unmarshal(e.Update, &u) != nil || u.SessionUpdate != "user_message_chunk" || u.Meta.Gerdr.Subagent != "" {
		return "", false
	}
	return u.MessageID, true
}

// tailOf：evs[:end] 的最后一段（换好图片），从一轮的开头切，攒够 TailBudget 为止；more = 前面还有。
// 一轮本身就很大时整轮给（从中间切开的话，界面重放出来的会缺头少尾）。
func (h *Hub) tailOf(evs []*Event, end int) (out []*Event, more bool) {
	size, start := 0, 0
	for i := end - 1; i >= 0; i-- {
		e := h.slim(evs[i])
		out = append(out, e)
		size += evSize(e)
		if size >= TailBudget && turnHead(evs, i) {
			start = i
			break
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, start > 0
}

// Tail：打开会话时推的那一段。
func (s *Session) Tail() ([]*Event, bool) {
	evs := s.Events()
	return s.h.tailOf(evs, len(evs))
}

// OlderEvents：序号 before 之前的再一段（界面往上翻到顶了来拉）。
func (s *Session) OlderEvents(before int64) ([]*Event, bool) {
	evs := s.Events()
	end := sort.Search(len(evs), func(i int) bool { return evs[i].Seq >= before })
	return s.h.tailOf(evs, end)
}
