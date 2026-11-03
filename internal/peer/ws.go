package peer

import (
	"context"
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
	d := websocket.Dialer{HandshakeTimeout: 10 * time.Second, EnableCompression: compress,
		NetDialContext: m.tr.DialContext, TLSClientConfig: m.tr.TLSClientConfig}
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
