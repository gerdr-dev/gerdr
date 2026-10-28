package hub

import (
	"context"
	"sync"
	"time"
)

// Msg 是推给前端的一帧。
type Msg struct {
	T       string   `json:"t"`
	ID      string   `json:"id,omitempty"`
	Session *Summary `json:"session,omitempty"`
	Events  []*Event `json:"events"`
	Reset   bool     `json:"reset,omitempty"`
	Data    any      `json:"data,omitempty"`
}

// Sub 是一个前端连接的订阅：会话列表的变化总是推；某个会话的日志只在订阅了它时才推
// （画面上看得见的那几个 pane）。
type Sub struct {
	mu     sync.Mutex
	ids    map[string]bool
	out    chan Msg
	closed bool
}

func (h *Hub) Subscribe() *Sub {
	s := &Sub{ids: map[string]bool{}, out: make(chan Msg, 4096)}
	h.mu.Lock()
	h.subs[s] = struct{}{}
	h.mu.Unlock()
	return s
}

func (h *Hub) Unsubscribe(s *Sub) {
	h.mu.Lock()
	delete(h.subs, s)
	h.mu.Unlock()
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		close(s.out)
	}
	s.mu.Unlock()
}

func (s *Sub) C() <-chan Msg { return s.out }

// Watch 订阅一组会话（替换之前的）。新加进来的会话先推一份完整日志。
func (h *Hub) Watch(sub *Sub, ids []string) {
	// 注意 Msg.Events 不能 omitempty：空日志的会话也要推一帧（reset + []），前端靠它知道「加载完了」。
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	sub.mu.Lock()
	var added []string
	for id := range want {
		if !sub.ids[id] {
			added = append(added, id)
		}
	}
	sub.ids = want
	sub.mu.Unlock()
	for _, id := range added {
		s, err := h.Session(id)
		if err != nil {
			continue
		}
		sub.send(Msg{T: "events", ID: id, Events: s.Events(), Reset: true})
		// 打开 / 切到这个会话：顺手看看它是不是在别处（herdr / 终端）接着聊过，是的话追上（CatchUp）
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			s.CatchUp(ctx)
		}()
	}
}

func (s *Sub) send(m Msg) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	select {
	case s.out <- m:
	default:
		// 慢客户端：缓冲满了就断开它，让它重连拿完整日志（比悄悄丢帧好）。
		s.closed = true
		close(s.out)
	}
}

func (h *Hub) subscribers() []*Sub {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]*Sub, 0, len(h.subs))
	for s := range h.subs {
		out = append(out, s)
	}
	return out
}

func (h *Hub) broadcast(m Msg) {
	for _, s := range h.subscribers() {
		s.send(m)
	}
}

func (h *Hub) broadcastSession(s *Session) {
	sum := s.Summary()
	h.broadcast(Msg{T: "session", ID: sum.ID, Session: &sum})
}

func (h *Hub) broadcastEvents(id string, evs []*Event, reset bool) {
	for _, s := range h.subscribers() {
		s.mu.Lock()
		w := s.ids[id]
		s.mu.Unlock()
		if w {
			s.send(Msg{T: "events", ID: id, Events: evs, Reset: reset})
		}
	}
}

// Broadcast 给外面（server）推非会话的东西（布局、插件变了）。
func (h *Hub) Broadcast(t string, data any) { h.broadcast(Msg{T: t, Data: data}) }
