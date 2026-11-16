package server

// g.socket: a WebSocket to a third-party service, opened by the backend for an add-on, the way g.fetch makes HTTP
// requests (addonfetch.go). Asked for by streaming speech-to-text (the voice add-on shows words as they are said):
// the page can't open it itself — its CSP is `connect-src 'self'`, and a browser WebSocket can't set headers, while
// such APIs authenticate with one (Azure's Ocp-Apim-Subscription-Key).
//
// Two steps, so headers (a key) never go in a URL: POST /api/addons/{id}/socket {url, headers, protocols} keeps the
// request under a one-time token for 30s; the page then opens GET /api/addons/{id}/socket/{token} as a WebSocket,
// the backend dials the service with those headers, and messages are copied both ways as they are (text stays
// text, binary stays binary). Both sit behind the same guard as every /api call, like g.fetch: it grants nothing an
// add-on can't already do (it can run a service).

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/gerdr-dev/gerdr/internal/i18n"
	"github.com/gerdr-dev/gerdr/internal/peer"
)

type addonSocketReq struct {
	URL       string            `json:"url"`
	Headers   map[string]string `json:"headers"`
	Protocols []string          `json:"protocols"`
	addon     string
	at        time.Time
}

var addonSockets = struct {
	sync.Mutex
	m map[string]addonSocketReq
}{m: map[string]addonSocketReq{}}

const addonSocketTTL = 30 * time.Second

func (s *Server) addonSocketOpen(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.addonByID(id) == nil {
		httpError(w, 404, "no such add-on: "+id)
		return
	}
	var b addonSocketReq
	if err := decode(r, &b); err != nil {
		httpError(w, 400, err.Error())
		return
	}
	u, err := url.Parse(b.URL)
	if err != nil || (u.Scheme != "ws" && u.Scheme != "wss") || u.Host == "" {
		httpError(w, 400, "url must be ws(s): "+b.URL)
		return
	}
	var tok [16]byte
	_, _ = rand.Read(tok[:])
	t := hex.EncodeToString(tok[:])
	b.addon, b.at = id, time.Now()
	addonSockets.Lock()
	for k, v := range addonSockets.m { // drop the ones never opened
		if time.Since(v.at) > addonSocketTTL {
			delete(addonSockets.m, k)
		}
	}
	addonSockets.m[t] = b
	addonSockets.Unlock()
	writeJSON(w, map[string]string{"token": t})
}

func (s *Server) addonSocketConnect(w http.ResponseWriter, r *http.Request) {
	addonSockets.Lock()
	b, ok := addonSockets.m[r.PathValue("token")]
	delete(addonSockets.m, r.PathValue("token"))
	addonSockets.Unlock()
	if !ok || b.addon != r.PathValue("id") || time.Since(b.at) > addonSocketTTL {
		httpError(w, 404, i18n.S("这个连接已经用过或过期了", "This socket was already used or has expired"))
		return
	}
	h := http.Header{}
	for k, v := range b.Headers {
		h.Set(k, v)
	}
	d := websocket.Dialer{Proxy: http.ProxyFromEnvironment, HandshakeTimeout: 15 * time.Second, Subprotocols: b.Protocols}
	// Dialled first, so a service that refuses (a wrong key: 401) answers the page with that status rather than an
	// upgrade that closes at once
	up, resp, err := d.DialContext(r.Context(), b.URL, h)
	if err != nil {
		code := http.StatusBadGateway
		if resp != nil && resp.StatusCode >= 400 {
			code = resp.StatusCode
		}
		httpError(w, code, err.Error())
		return
	}
	client, err := plainUpgrader.Upgrade(w, r, nil)
	if err != nil {
		up.Close()
		return
	}
	s.Log.Info("add-on socket", "addon", b.addon, "host", up.RemoteAddr().String())
	peer.Pipe(client, up)
}
