package relay

import (
	"io"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

/* ------------------------------------------------------------------ WebSocket → 字节流 */

// wsStream 把一条 WebSocket 当成字节流用（yamux 要的是 io.ReadWriteCloser）。
//
// 主机到中继这一段为什么要包 WebSocket 而不是直接一条 TLS：公司网络里常见那种只放行
// HTTP(S) 的出口代理，裸 TLS 上跑自定义协议过不去，WebSocket 过得去。
//
// 写要上锁：gorilla 的连接**不能并发写**，撞上了是 panic、整个进程退出（CLAUDE.md 里
// wsWriter 那条踩过一次）。yamux 自己只有一个发送协程，但关闭时还有一次写，别赌。
type wsStream struct {
	ws *websocket.Conn
	mu sync.Mutex
	r  io.Reader
}

func newWSStream(ws *websocket.Conn) *wsStream { return &wsStream{ws: ws} }

func (s *wsStream) Read(p []byte) (int, error) {
	for {
		if s.r == nil {
			_, r, err := s.ws.NextReader()
			if err != nil {
				return 0, err
			}
			s.r = r
		}
		n, err := s.r.Read(p)
		if err == io.EOF {
			s.r = nil
			if n > 0 {
				return n, nil
			}
			continue
		}
		return n, err
	}
}

func (s *wsStream) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// 写也要有个期限：对面断网时 TCP 缓冲填满，WriteMessage 会一直阻塞（pty.go 那条坑）
	_ = s.ws.SetWriteDeadline(time.Now().Add(30 * time.Second))
	if err := s.ws.WriteMessage(websocket.BinaryMessage, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (s *wsStream) Close() error { return s.ws.Close() }

// 握手和子流头的时限，和 herdr-relay 那一侧（herdr-web 的 internal/relay/server.go）同一组数。
const (
	// handshakeTimeout：握手三句话必须在这么久之内说完。
	handshakeTimeout = 15 * time.Second
	// helloTimeout：子流开头那个头必须在这么久之内到。
	helloTimeout = 10 * time.Second
)

// discard 给 yamux 的 logger 用：它的日志（每条子流的开关）对人没有用。
type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
