package server

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gorilla/websocket"

	"github.com/gerdr-dev/gerdr/internal/term"
)

// 终端：建 / 列 / 关走 HTTP，输入输出走每个终端一条 WebSocket（/api/terms/{id}/ws）。
// 不并进 /ws 那条：终端输出是原始字节、量大，混进 JSON 批里要 base64，还会和会话推送互相拖慢。
func (s *Server) termRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/terms", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, s.Terms.List()) })
	mux.HandleFunc("POST /api/terms", func(w http.ResponseWriter, r *http.Request) {
		var o term.CreateOptions
		if err := decode(r, &o); err != nil {
			httpError(w, 400, err.Error())
			return
		}
		t, err := s.Terms.Create(o)
		if err != nil {
			httpError(w, 400, err.Error())
			return
		}
		writeJSON(w, t.Info())
	})
	mux.HandleFunc("GET /api/terms/{id}", func(w http.ResponseWriter, r *http.Request) {
		t := s.Terms.Get(r.PathValue("id"))
		if t == nil {
			httpError(w, 404, term.ErrNotFound.Error())
			return
		}
		info := t.Info()
		if r.URL.Query().Get("cwd") == "1" {
			if d := t.LiveCwd(); d != "" {
				info.Cwd = d
			}
		}
		writeJSON(w, info)
	})
	mux.HandleFunc("DELETE /api/terms/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := s.Terms.Kill(r.PathValue("id")); err != nil {
			httpError(w, 404, err.Error())
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	})
	mux.HandleFunc("GET /api/terms/{id}/ws", s.termWS)
}

// termWS：服务端 → 客户端：二进制帧是输出（先是重放的那一段），文本帧是 JSON 控制消息 {t:"exit",code}、{t:"pong"}。
// 客户端 → 服务端：二进制帧是输入，文本帧 {t:"resize",cols,rows}、{t:"ping"}。
func (s *Server) termWS(w http.ResponseWriter, r *http.Request) {
	t := s.Terms.Get(r.PathValue("id"))
	if t == nil {
		httpError(w, 404, term.ErrNotFound.Error())
		return
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	backlog, out, detach := t.Attach()
	defer detach()

	done := make(chan struct{})
	pong := make(chan struct{}, 1)
	go func() {
		defer close(done)
		conn.SetReadLimit(1 << 20)
		for {
			typ, b, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if typ == websocket.BinaryMessage {
				_ = t.Write(b)
				continue
			}
			var m struct {
				T    string `json:"t"`
				Cols uint16 `json:"cols"`
				Rows uint16 `json:"rows"`
			}
			if json.Unmarshal(b, &m) != nil {
				continue
			}
			switch m.T {
			case "resize":
				_ = t.Resize(m.Cols, m.Rows)
			case "ping":
				select {
				case pong <- struct{}{}:
				default:
				}
			}
		}
	}()

	write := func(typ int, b []byte) bool {
		_ = conn.SetWriteDeadline(time.Now().Add(15 * time.Second))
		return conn.WriteMessage(typ, b) == nil
	}
	writeJSONMsg := func(v any) bool {
		b, _ := json.Marshal(v)
		return write(websocket.TextMessage, b)
	}
	if !write(websocket.BinaryMessage, backlog) {
		return
	}
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-done:
			return
		case chunk, ok := <-out:
			if !ok {
				// 两种关法：shell 退了（告诉界面退出码），或者这边消费太慢被踢（直接断，界面重连拿重放）。
				select {
				case <-t.Done():
					writeJSONMsg(map[string]any{"t": "exit", "code": t.ExitCode()})
				case <-time.After(200 * time.Millisecond):
				}
				return
			}
			// 输出很碎（一个字一个字地回显）：手上已经到了的并成一帧。
			buf := chunk
		more:
			for len(buf) < 64<<10 {
				select {
				case c, ok := <-out:
					if !ok {
						break more
					}
					buf = append(buf, c...)
				default:
					break more
				}
			}
			if !write(websocket.BinaryMessage, buf) {
				return
			}
		case <-pong:
			if !writeJSONMsg(map[string]string{"t": "pong"}) {
				return
			}
		case <-ping.C:
			_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if conn.WriteMessage(websocket.PingMessage, nil) != nil {
				return
			}
		}
	}
}
