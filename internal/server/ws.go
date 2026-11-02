package server

import (
	"encoding/json"
	"net/http"
	"net/url"
	"time"

	"github.com/gorilla/websocket"

	"github.com/gerdr-dev/gerdr/internal/auth"
	"github.com/gerdr-dev/gerdr/internal/hub"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 64 << 10,
	// permessage-deflate：会话日志是 JSON，压完小好几倍。远程（中继 / 手机流量）打开大会话时，
	// 整份日志是一帧，不压的话慢网络上 15 秒写不完就被写期限掐断、重连再推，永远打不开。
	// 中继那段是字节隧道，压缩是浏览器和这里端到端协商的。
	EnableCompression: true,
	CheckOrigin: func(r *http.Request) bool {
		o := r.Header.Get("Origin")
		if o == "" {
			return true // 非浏览器客户端（有令牌就行）
		}
		u, err := url.Parse(o)
		return err == nil && u.Host == r.Host
	},
}

// ws：推会话列表 / 日志 / 布局 / 配置的变化。客户端发两种消息：{t:"watch", ids:[…], since:{id:seq}}、{t:"ping"}（回 {t:"pong"}）。
// Plus {t:"draft", id, draft:{text, sel, imgs, from}}: the unsent composer content, mirrored to the other devices (hub/draft.go).
func (s *Server) ws(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	// 远程界面开着就一直有这条 /ws（走到这儿已经过了 remoteGuard，是配过对的设备）：按它算「有远程连着」
	if auth.FromRemote(r) {
		defer s.trackRemote(r)()
		if s.Awake != nil {
			defer s.Awake.Hold()()
		}
	}
	sub := s.Hub.Subscribe()
	defer s.Hub.Unsubscribe(sub)
	done := make(chan struct{})
	pong := make(chan struct{}, 1)
	// 读期限：客户端 20 秒一次应用层 ping，浏览器对协议层 ping（下面 25 秒一次）也会自动回 pong，收到哪个都续上。
	// 70 秒什么都没收到就是死连接（手机换网、中继那头断了）：不设的话它一直挂在「正在连接」里、一直挡着睡眠。
	const readWait = 70 * time.Second
	_ = conn.SetReadDeadline(time.Now().Add(readWait))
	conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(readWait)) })
	go func() {
		defer close(done)
		// 读这头断了（客户端关了 / 死了）就把整条连接关掉：写的那头可能正卡在一大帧日志上（写期限 30 秒），
		// 不关的话它要等写完或超时才发现，这期间重连上来的新连接和它叠在一起，「正在连接」里一台手机列好几条（踩过）。
		defer conn.Close()
		conn.SetReadLimit(1 << 20)
		for {
			_, b, err := conn.ReadMessage()
			if err != nil {
				return
			}
			_ = conn.SetReadDeadline(time.Now().Add(readWait))
			var m struct {
				T     string           `json:"t"`
				IDs   []string         `json:"ids"`
				Since map[string]int64 `json:"since"` // 会话 id → 前端已有日志的最后序号，接得上就只补后面的
				ID    string           `json:"id"`    // draft: which session
				Draft hub.Draft        `json:"draft"` // draft: text, selection, attachment refs, writer's client id
			}
			if json.Unmarshal(b, &m) != nil {
				continue
			}
			switch m.T {
			case "watch":
				s.Hub.Watch(sub, m.IDs, m.Since)
			case "draft":
				// The composer text, mirrored to every device looking at this session (hub/draft.go)
				s.Hub.SetDraft(m.ID, m.Draft)
			case "ping":
				// 应用层心跳：浏览器读不到协议层的 ping/pong，只能靠这个判断连接是不是「假在线」。
				select {
				case pong <- struct{}{}:
				default:
				}
			}
		}
	}()
	// 写只在这一个 goroutine 里（gorilla 不许并发写），顺带每 25 秒 ping 一次。
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	var batch []hub.Msg
	flush := time.NewTimer(time.Hour)
	flush.Stop()
	// 写失败（多半是写期限到了还没写完：远程慢、一帧太大）记一笔：连接断了界面只会显示「离线」，原因在这儿查
	write := func(v any) bool {
		b, err := json.Marshal(v)
		if err != nil {
			return false
		}
		start := time.Now()
		_ = conn.SetWriteDeadline(start.Add(30 * time.Second))
		if err := conn.WriteMessage(websocket.TextMessage, b); err != nil {
			if s.Log != nil {
				s.Log.Info("ws write failed", "remote", auth.FromRemote(r), "bytes", len(b), "took", time.Since(start).Round(time.Millisecond), "err", err)
			}
			return false
		}
		return true
	}
	for {
		select {
		case <-done:
			return
		case m, ok := <-sub.C():
			if !ok {
				return
			}
			// 流式片段很碎：攒 30ms 一起发，一帧一个数组。
			batch = append(batch, m)
			if len(batch) == 1 {
				flush.Reset(30 * time.Millisecond)
			}
			if len(batch) >= 256 {
				if !write(batch) {
					return
				}
				batch = nil
			}
		case <-flush.C:
			if len(batch) > 0 {
				if !write(batch) {
					return
				}
				batch = nil
			}
		case <-pong:
			if !write([]map[string]string{{"t": "pong"}}) {
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
