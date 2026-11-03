package peer

import (
	"context"
	"crypto/tls"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// DialWS opens a WebSocket to pathQuery on a host (its /ws, or /api/screen/stream for the remote screen), with the
// device cookie. compress: permessage-deflate, worth it for JSON, not for JPEG tiles (screenstream.go turns it off).
func (m *Manager) DialWS(ctx context.Context, hostID, pathQuery string, compress bool) (*websocket.Conn, *http.Response, error) {
	h, ok := m.Host(hostID)
	if !ok {
		return nil, nil, ErrNoHost
	}
	hdr := http.Header{}
	hdr.Set("Cookie", h.Cookie+"="+h.Token)
	hdr.Set("User-Agent", "Gerdr console")
	// Our own copy of the TLS config, pinned to HTTP/1.1: the HTTP client shares m.tr, and net/http adds "h2" to
	// m.tr.TLSClientConfig.NextProtos the first time it sends a request. Handed over as is, the WebSocket handshake
	// then negotiates h2 and fails ("protocol h2 was given but is not supported"), so every upstream /ws dropped
	// right after the session-list fetch that precedes it, and the UI never saw a host session's events.
	var tc *tls.Config
	if m.tr.TLSClientConfig != nil {
		tc = m.tr.TLSClientConfig.Clone()
	} else {
		tc = &tls.Config{}
	}
	tc.NextProtos = []string{"http/1.1"}
	d := websocket.Dialer{HandshakeTimeout: 10 * time.Second, EnableCompression: compress,
		NetDialContext: m.tr.DialContext, TLSClientConfig: tc}
	dctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return d.DialContext(dctx, "wss://"+strings.TrimPrefix(h.URL, "https://")+pathQuery, hdr)
}

// Pipe copies messages both ways between a client's connection and a host's until either side closes. Message types
// (text / binary) are kept: the remote screen sends JPEG tiles as binary frames.
func Pipe(client, host *websocket.Conn) {
	done := make(chan struct{}, 2)
	cp := func(dst, src *websocket.Conn) {
		defer func() { done <- struct{}{} }()
		for {
			t, b, err := src.ReadMessage()
			if err != nil {
				return
			}
			_ = dst.SetWriteDeadline(time.Now().Add(30 * time.Second))
			if dst.WriteMessage(t, b) != nil {
				return
			}
		}
	}
	go cp(host, client)
	go cp(client, host)
	<-done
	client.Close()
	host.Close()
	<-done
}
