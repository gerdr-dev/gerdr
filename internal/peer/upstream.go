package peer

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Upstream is one console /ws connection's link to one host's /ws. One per local connection, not one shared: a
// newly watched session's catch-up (the tail of its log, or what came after `since`) is per connection on the host,
// exactly like for a browser, and sharing one upstream would have to redo that bookkeeping here.
type Upstream struct {
	m    *Manager
	host string
	out  chan<- json.RawMessage

	mu    sync.Mutex
	conn  *websocket.Conn
	ids   []string         // what this connection watches on the host (host ids, no prefix)
	since map[string]int64 // last seq seen per watched session: a reconnect asks only for what came after
}

// forwarded: message kinds that belong to a session. Everything else a host broadcasts (its layout, config,
// add-ons, unread set, "open") is about its own screen and stays there.
var forwarded = map[string]bool{"session": true, "events": true, "session_removed": true, "draft": true, "close": true}

// Connect starts keeping an upstream to hostID until ctx ends; messages, rewritten, go to out.
func (m *Manager) Connect(ctx context.Context, hostID string, out chan<- json.RawMessage) *Upstream {
	u := &Upstream{m: m, host: hostID, out: out, since: map[string]int64{}}
	go u.run(ctx)
	return u
}

// Watch: the session ids (host ids) this connection now looks at, and the seqs the UI already holds.
func (u *Upstream) Watch(ids []string, since map[string]int64) {
	u.mu.Lock()
	u.ids = ids
	for id, s := range since {
		u.since[id] = s
	}
	// Writes to conn only ever happen under mu: gorilla allows one writer at a time
	if u.conn != nil {
		_ = u.conn.WriteMessage(websocket.TextMessage, u.watchMsgLocked())
	}
	u.mu.Unlock()
}

func (u *Upstream) watchMsgLocked() []byte {
	since := map[string]int64{}
	for _, id := range u.ids {
		if s, ok := u.since[id]; ok {
			since[id] = s
		}
	}
	b, _ := json.Marshal(map[string]any{"t": "watch", "ids": u.ids, "since": since})
	return b
}

// Send passes a client message (a draft) on to the host.
func (u *Upstream) Send(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	b = UnrewriteURLs(u.host, b)
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.conn != nil {
		_ = u.conn.WriteMessage(websocket.TextMessage, b)
	}
}

func (u *Upstream) run(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		start := time.Now()
		u.once(ctx)
		if ctx.Err() != nil {
			return
		}
		if time.Since(start) > time.Minute {
			backoff = time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

func (u *Upstream) once(ctx context.Context) {
	h, ok := u.m.Host(u.host)
	if !ok {
		return
	}
	hdr := http.Header{}
	hdr.Set("Cookie", h.Cookie+"="+h.Token)
	hdr.Set("User-Agent", "Gerdr console")
	d := websocket.Dialer{HandshakeTimeout: 10 * time.Second, EnableCompression: true,
		NetDialContext: u.m.tr.DialContext, TLSClientConfig: u.m.tr.TLSClientConfig}
	wsURL := "wss://" + strings.TrimPrefix(h.URL, "https://") + "/ws"
	dctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	conn, resp, err := d.DialContext(dctx, wsURL, hdr)
	cancel()
	if err != nil {
		msg := err.Error()
		if resp != nil && resp.StatusCode == 401 {
			msg = "unauthorized"
		}
		u.m.setState(u.host, func(st *hostState) { st.online, st.err = false, msg })
		return
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()

	// The UI may have been looking at an old list (the host was off when the page loaded): send it the current one
	u.m.refresh(ctx, h)
	u.m.mu.Lock()
	var list []json.RawMessage
	if st := u.m.state[u.host]; st != nil {
		list = append(list, st.sessions...)
	}
	u.m.mu.Unlock()
	for _, s := range list {
		b, _ := json.Marshal(map[string]any{"t": "session", "id": idOf(s), "session": s})
		u.emit(ctx, b)
	}

	u.mu.Lock()
	u.conn = conn
	_ = conn.WriteMessage(websocket.TextMessage, u.watchMsgLocked())
	u.mu.Unlock()
	defer func() {
		u.mu.Lock()
		u.conn = nil
		u.mu.Unlock()
	}()

	// Keep the host's read deadline fed (it drops a connection silent for 70 s)
	go func() {
		t := time.NewTicker(20 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				u.mu.Lock()
				c := u.conn
				if c != nil {
					_ = c.WriteMessage(websocket.TextMessage, []byte(`{"t":"ping"}`))
				}
				u.mu.Unlock()
				if c == nil {
					return
				}
			}
		}
	}()

	_ = conn.SetReadDeadline(time.Now().Add(70 * time.Second))
	conn.SetPingHandler(func(s string) error {
		_ = conn.SetReadDeadline(time.Now().Add(70 * time.Second))
		u.mu.Lock()
		defer u.mu.Unlock()
		return conn.WriteControl(websocket.PongMessage, []byte(s), time.Now().Add(5*time.Second))
	})
	for {
		_, b, err := conn.ReadMessage()
		if err != nil {
			return
		}
		_ = conn.SetReadDeadline(time.Now().Add(70 * time.Second))
		var batch []map[string]json.RawMessage
		if json.Unmarshal(b, &batch) != nil {
			continue
		}
		for _, msg := range batch {
			if out := u.rewrite(msg); out != nil {
				u.emit(ctx, out)
			}
		}
	}
}

func (u *Upstream) emit(ctx context.Context, b json.RawMessage) {
	select {
	case u.out <- b:
	case <-ctx.Done():
	}
}

func idOf(raw json.RawMessage) string {
	var x struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(raw, &x)
	return x.ID
}

func (u *Upstream) rewrite(msg map[string]json.RawMessage) json.RawMessage {
	var t, id string
	_ = json.Unmarshal(msg["t"], &t)
	if !forwarded[t] {
		return nil
	}
	_ = json.Unmarshal(msg["id"], &id)
	h, ok := u.m.Host(u.host)
	if !ok {
		return nil
	}
	if id != "" {
		msg["id"], _ = json.Marshal(Join(u.host, id))
	}
	switch t {
	case "session":
		if s := u.m.RewriteSummary(h, msg["session"]); s != nil {
			msg["session"] = s
			u.m.noteSession(u.host, s, "")
		}
	case "session_removed":
		u.m.noteSession(u.host, nil, Join(u.host, id))
	case "events":
		msg["events"] = RewriteEvents(u.host, msg["events"])
		var evs []struct {
			Seq int64 `json:"seq"`
		}
		if json.Unmarshal(msg["events"], &evs) == nil && len(evs) > 0 {
			u.mu.Lock()
			if last := evs[len(evs)-1].Seq; last > u.since[id] {
				u.since[id] = last
			}
			u.mu.Unlock()
		}
	case "close":
		var d map[string]any
		if json.Unmarshal(msg["data"], &d) == nil {
			if s, ok := d["id"].(string); ok {
				d["id"] = Join(u.host, s)
			}
			msg["data"], _ = json.Marshal(d)
		}
	}
	b, err := json.Marshal(msg)
	if err != nil {
		return nil
	}
	return RewriteURLs(u.host, b)
}
