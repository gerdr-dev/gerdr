package hub

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// 增量补发：接得上只给后面的；流式片段并过、整份重推过、重启前的序号都得退回整份。
func TestEventsAfter(t *testing.T) {
	h, err := New(Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	s := newSession(h, Record{ID: "s1"})
	chunk := func(text string) *Event {
		return &Event{Kind: EvUpdate, Update: json.RawMessage(`{"sessionUpdate":"agent_message_chunk","messageId":"a1","content":{"type":"text","text":"` + text + `"}}`)}
	}
	s.emit(&Event{Kind: EvUser, MessageID: "u1"})
	u1 := s.events[0].Seq
	s.emit(chunk("ab"))
	ab := s.events[1].Seq

	if _, ok := h.eventsAfter(s, 0); ok {
		t.Fatal("since=0 应整份")
	}
	evs, ok := h.eventsAfter(s, u1)
	if !ok || len(evs) != 1 || evs[0].Seq != ab {
		t.Fatalf("接 u1：%v %v", ok, evs)
	}
	if evs, ok := h.eventsAfter(s, ab); !ok || len(evs) != 0 {
		t.Fatalf("已是最新：%v %v", ok, evs)
	}

	// 再来一段并进上一条：那条换了新序号，拿着旧序号的接不上（不然 "ab" 会重复）
	s.emit(chunk("cd"))
	if _, ok := h.eventsAfter(s, ab); ok {
		t.Fatal("并过的片段应整份")
	}
	if _, ok := h.eventsAfter(s, u1); !ok {
		t.Fatal("u1 还在，应接得上")
	}

	// 整份重推（撤回用户消息之类改写了日志）：之前的序号都不信
	h.broadcastEvents("s1", s.Events(), true)
	if _, ok := h.eventsAfter(s, s.events[len(s.events)-1].Seq); ok {
		t.Fatal("重推前的序号应整份")
	}
	s.emit(&Event{Kind: EvTurnEnd})
	if _, ok := h.eventsAfter(s, s.events[len(s.events)-1].Seq); !ok {
		t.Fatal("重推后的新条目应接得上")
	}

	// 重启前的序号
	h.bootSeq = h.seq
	if _, ok := h.eventsAfter(s, s.events[len(s.events)-1].Seq); ok {
		t.Fatal("重启前的序号应整份")
	}
}

// 图片换成地址、存进 blobs；原来那条不动。
func TestSlimImage(t *testing.T) {
	h, err := New(Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	img := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x89, 'P', 'N', 'G'}, 2000))
	orig := json.RawMessage(`{"type":"image","mimeType":"image/png","data":"` + img + `"}`)
	e := &Event{Kind: EvUser, Content: []json.RawMessage{json.RawMessage(`{"type":"text","text":"hi"}`), orig},
		Update: json.RawMessage(`{"sessionUpdate":"tool_call_update","rawOutput":[{"type":"image","source":{"type":"base64","media_type":"image/jpeg","data":"` + img + `"}}],"n":1.50}`)}
	s := h.slim(e)
	if s == e || string(e.Content[1]) != string(orig) {
		t.Fatal("应返回新对象、不改原来的")
	}
	var c struct{ Data, Blob string }
	_ = json.Unmarshal(s.Content[1], &c)
	if c.Data != "" || !strings.HasPrefix(c.Blob, BlobURL) {
		t.Fatalf("content: %s", s.Content[1])
	}
	if p := h.BlobPath(strings.TrimPrefix(c.Blob, BlobURL)); p == "" {
		t.Fatal("hash")
	} else if b, _ := os.ReadFile(p); len(b) != 8000 {
		t.Fatalf("blob %d", len(b))
	}
	if bytes.Contains(s.Update, []byte(img)) || !bytes.Contains(s.Update, []byte(`"n":1.50`)) {
		t.Fatalf("update: %.200s", s.Update)
	}
	plain := &Event{Kind: EvUser, Content: []json.RawMessage{json.RawMessage(`{"type":"text","text":"hi"}`)}}
	if h.slim(plain) != plain {
		t.Fatal("没图片应原样")
	}
}

// 最近一段从一轮的开头切（插话不算开头），攒够 TailBudget；再往前拉接得上。
func TestTail(t *testing.T) {
	h, err := New(Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	s := newSession(h, Record{ID: "s1"})
	big := strings.Repeat("x", TailBudget/3)
	for i := 0; i < 10; i++ {
		s.emit(&Event{Kind: EvUser, MessageID: "u"})
		s.emit(&Event{Kind: EvUser, MessageID: "steer", Steer: true})
		s.emit(&Event{Kind: EvNotice, Text: big})
	}
	tail, more := s.Tail()
	if !more || tail[0].Kind != EvUser || tail[0].Steer {
		t.Fatalf("开头：%v %+v", more, tail[0])
	}
	if n := len(tail); n != 9 { // 攒够 1 个预算要 3 条大的 = 3 轮
		t.Fatalf("段长 %d", n)
	}
	got := tail
	for more {
		var older []*Event
		older, more = s.OlderEvents(got[0].Seq)
		if len(older) == 0 {
			t.Fatal("还有却拉空")
		}
		got = append(older, got...)
	}
	if len(got) != len(s.events) {
		t.Fatalf("拉全了 %d / %d", len(got), len(s.events))
	}
	for i := range got {
		if got[i].Seq != s.events[i].Seq {
			t.Fatal("顺序 / 接缝不对")
		}
	}
}

// 导入 / 追上来的历史：用户消息是 user_message_chunk，同一句的后几块、子 agent 的都不算一轮开头。
func TestTurnHeadUserChunk(t *testing.T) {
	uc := func(id, meta string) *Event {
		return &Event{Kind: EvUpdate, Update: json.RawMessage(`{"sessionUpdate":"user_message_chunk","messageId":"` + id + `","content":{"type":"text","text":"x"}` + meta + `}`)}
	}
	evs := []*Event{uc("a", ""), uc("a", ""), uc("b", ""), uc("c", `,"_meta":{"gerdr":{"subagent":"t1"}}`), {Kind: EvUpdate, Update: json.RawMessage(`{"sessionUpdate":"agent_message_chunk"}`)}}
	want := []bool{true, false, true, false, false}
	for i, w := range want {
		if turnHead(evs, i) != w {
			t.Fatalf("%d：want %v", i, w)
		}
	}
}
