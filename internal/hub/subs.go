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
	Older   bool     `json:"older,omitempty"` // reset 推的只是最近一段，更早的还有（界面往上翻到顶再拉，见 blob.go）
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

// Watch 订阅一组会话（替换之前的）。新加进来的会话推一份日志：since 里有它（前端手里已有到这个序号为止的日志）
// 而且接得上，就只推之后的；接不上推最近一段（reset，更早的界面往上翻再拉）。断线重连、切回看过的面板都走这儿，
// 远程连着时一份几十 MB 的日志每次整份重推，慢网络上会卡死在「写超时 → 重连 → 再推」里。
func (h *Hub) Watch(sub *Sub, ids []string, since map[string]int64) {
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
		if evs, ok := h.eventsAfter(s, since[id]); ok {
			sub.send(Msg{T: "events", ID: id, Events: evs})
		} else {
			tail, older := s.Tail()
			sub.send(Msg{T: "events", ID: id, Events: tail, Reset: true, Older: older})
		}
		// 打开 / 切到这个会话：顺手看看它是不是在别处（herdr / 终端）接着聊过，是的话追上（CatchUp）
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			s.CatchUp(ctx)
		}()
	}
}

// eventsAfter：序号 since 之后的日志，前提是前端那份（到 since 为止）和这边的还是同一份。接不上的情况：
//   - since 是重启前的：内存里没落盘的流式片段丢了，序号也可能对不上；
//   - since 之后整份重推过（撤回用户消息会删掉中间的条目，光看序号看不出来）；
//   - 日志里已经没有 since 这一条：流式片段并进上一条时会换新序号（emit），前端那条的内容被并进了后面的条目，
//     再按序号往后接会把已经有的字重复一遍。
func (h *Hub) eventsAfter(s *Session, since int64) ([]*Event, bool) {
	if since <= 0 {
		return nil, false
	}
	h.mu.Lock()
	stale := since <= h.bootSeq || since <= h.resetAt[s.rec.ID]
	h.mu.Unlock()
	if stale {
		return nil, false
	}
	s.mu.Lock()
	var evs []*Event
	found := false
	for i := len(s.events) - 1; i >= 0; i-- {
		if s.events[i].Seq == since {
			evs = append([]*Event{}, s.events[i+1:]...)
			found = true
			break
		}
		if s.events[i].Seq < since {
			break
		}
	}
	s.mu.Unlock()
	if !found {
		return nil, false
	}
	// 离开太久、中间攒了一大堆：不如只推最近一段（补的这些是一整份发出去的，又会撞上远程那个「一帧传不完」）
	evs = h.slimEvents(evs)
	size := 0
	for _, e := range evs {
		if size += evSize(e); size > 4*TailBudget {
			return nil, false
		}
	}
	return evs, true
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
	m := Msg{T: "events", ID: id, Reset: reset}
	if reset {
		h.mu.Lock()
		h.resetAt[id] = h.seq
		h.mu.Unlock()
		// 改写过的日志（回退 / 撤回 / 清空）也只推最近一段，和打开时一样
		m.Events, m.Older = h.tailOf(evs, len(evs))
	} else {
		m.Events = h.slimEvents(evs)
	}
	for _, s := range h.subscribers() {
		s.mu.Lock()
		w := s.ids[id]
		s.mu.Unlock()
		if w {
			s.send(m)
		}
	}
}

// Broadcast 给外面（server）推非会话的东西（布局、插件变了）。
func (h *Hub) Broadcast(t string, data any) { h.broadcast(Msg{T: t, Data: data}) }
