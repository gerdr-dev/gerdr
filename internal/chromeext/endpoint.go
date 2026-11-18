package chromeext

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Endpoint is a fake browser-level DevTools WebSocket with the one tab the extension opened behind it, so ordinary
// CDP clients (chromedp, puppeteer.connect, Playwright connectOverCDP) can drive it. Browser-level commands
// (Target.*, Browser.*) are answered here; the tab session's commands go to the extension's chrome.debugger and its
// events come back tagged with the session id. Ported from Annulo's extbridge/endpoint.go, which was written against
// chromedp; other clients may ask for more than it answers.
type Endpoint struct {
	URL string // ws://127.0.0.1:<port>/devtools/browser/<random>

	c      *Conn
	tab    int
	window int
	ln     net.Listener
	srv    *http.Server
	unsub  func()

	mu      sync.Mutex
	ws      *websocket.Conn
	wm      sync.Mutex
	gone    bool
	why     string
	closeMu sync.Once
}

var upgrader = websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

func newEndpoint(c *Conn, tab, window int, onEvent func(Event)) (*Endpoint, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	buf := make([]byte, 16)
	rand.Read(buf)
	path := "/devtools/browser/" + hex.EncodeToString(buf)
	e := &Endpoint{URL: "ws://" + ln.Addr().String() + path, c: c, tab: tab, window: window, ln: ln}
	mux := http.NewServeMux()
	mux.HandleFunc(path, e.serve)
	// puppeteer / Playwright look the browser up over HTTP first when handed http://host:port
	mux.HandleFunc("/json/version", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"Browser": "Chrome (Gerdr extension)", "Protocol-Version": "1.3", "webSocketDebuggerUrl": e.URL})
	})
	e.srv = &http.Server{Handler: mux}
	go e.srv.Serve(ln)
	e.unsub = c.Subscribe(tab, func(ev Event) {
		e.onEvent(ev)
		if onEvent != nil {
			onEvent(ev)
		}
	})
	return e, nil
}

// HTTPURL is the http://127.0.0.1:<port> form some clients want (they fetch /json/version).
func (e *Endpoint) HTTPURL() string { return "http://" + e.ln.Addr().String() }

// Close the endpoint (not the tab).
func (e *Endpoint) Close() {
	e.closeMu.Do(func() {
		e.unsub()
		e.mu.Lock()
		if e.ws != nil {
			e.ws.Close()
		}
		e.mu.Unlock()
		e.srv.Close()
	})
}

func (e *Endpoint) targetID() string  { return "gerdr-tab-" + strconv.Itoa(e.tab) }
func (e *Endpoint) sessionID() string { return "gerdr-session-" + strconv.Itoa(e.tab) }

func (e *Endpoint) targetInfo() map[string]any {
	return map[string]any{"targetId": e.targetID(), "type": "page", "title": "", "url": "", "attached": true, "canAccessOpener": false, "browserContextId": "gerdr"}
}

type cdpMsg struct {
	ID        int64           `json:"id"`
	Method    string          `json:"method"`
	Params    json.RawMessage `json:"params"`
	SessionID string          `json:"sessionId"`
}

func (e *Endpoint) serve(w http.ResponseWriter, r *http.Request) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	ws.SetReadLimit(64 << 20)
	e.mu.Lock()
	if e.ws != nil {
		e.mu.Unlock()
		ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "one client at a time"))
		ws.Close()
		return
	}
	e.ws = ws
	e.mu.Unlock()
	defer func() {
		// the client went away: let the next one connect (the tab stays open until `gerdr browser close`)
		e.mu.Lock()
		if e.ws == ws {
			e.ws = nil
		}
		e.mu.Unlock()
		ws.Close()
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for {
		_, b, err := ws.ReadMessage()
		if err != nil {
			return
		}
		var m cdpMsg
		if json.Unmarshal(b, &m) != nil {
			continue
		}
		go e.handle(ctx, m)
	}
}

func (e *Endpoint) send(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	e.mu.Lock()
	ws := e.ws
	e.mu.Unlock()
	if ws == nil {
		return
	}
	e.wm.Lock()
	ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
	ws.WriteMessage(websocket.TextMessage, b)
	e.wm.Unlock()
}

func (e *Endpoint) result(m cdpMsg, res any) {
	out := map[string]any{"id": m.ID, "result": res}
	if m.SessionID != "" {
		out["sessionId"] = m.SessionID
	}
	e.send(out)
}

func (e *Endpoint) fail(m cdpMsg, err error) {
	out := map[string]any{"id": m.ID, "error": map[string]any{"code": -32000, "message": err.Error()}}
	if m.SessionID != "" {
		out["sessionId"] = m.SessionID
	}
	e.send(out)
}

func (e *Endpoint) event(method string, params any, session bool) {
	out := map[string]any{"method": method, "params": params}
	if session {
		out["sessionId"] = e.sessionID()
	}
	e.send(out)
}

func (e *Endpoint) handle(ctx context.Context, m cdpMsg) {
	if m.SessionID == "" {
		e.browserLevel(ctx, m)
		return
	}
	switch {
	case strings.HasPrefix(m.Method, "Browser."), m.Method == "Target.activateTarget", m.Method == "Target.closeTarget", m.Method == "Target.getTargetInfo":
		e.browserLevel(ctx, m)
		return
	case strings.HasPrefix(m.Method, "Target."):
		// child targets (iframes, workers) aren't attached; other Target.* count as done
		e.result(m, map[string]any{})
		return
	}
	e.mu.Lock()
	gone := e.gone
	e.mu.Unlock()
	if gone {
		e.fail(m, errors.New("the tab is gone"))
		return
	}
	cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	res, err := cdp(cctx, e.c, e.tab, m.Method, m.Params)
	if err != nil {
		e.fail(m, err)
		return
	}
	e.result(m, res)
}

// cdp sends one DevTools command to the tab through the extension.
func cdp(ctx context.Context, c *Conn, tab int, method string, params json.RawMessage) (json.RawMessage, error) {
	return cdpTo(ctx, c, tab, method, params, false)
}

// cdpTo: quiet asks the extension not to draw its cursor for a mouse event (and not to wait for its glide): the
// person's own mouse in the Browser pane (view.go), which they already see. An agent's clicks keep the cursor.
func cdpTo(ctx context.Context, c *Conn, tab int, method string, params json.RawMessage, quiet bool) (json.RawMessage, error) {
	var p any = map[string]any{}
	if len(params) > 0 && string(params) != "null" {
		p = params
	}
	args := map[string]any{"tab": tab, "method": method, "params": p}
	if quiet {
		args["quiet"] = true
	}
	res, err := c.Call(ctx, "cdp", args)
	if err != nil {
		return nil, err
	}
	if len(res) == 0 || string(res) == "null" {
		res = json.RawMessage("{}")
	}
	return res, nil
}

func (e *Endpoint) browserLevel(ctx context.Context, m cdpMsg) {
	switch m.Method {
	case "Target.setDiscoverTargets":
		e.result(m, map[string]any{})
		e.event("Target.targetCreated", map[string]any{"targetInfo": e.targetInfo()}, false)
	case "Target.createTarget":
		e.result(m, map[string]any{"targetId": e.targetID()})
		e.event("Target.targetCreated", map[string]any{"targetInfo": e.targetInfo()}, false)
	case "Target.attachToTarget":
		e.result(m, map[string]any{"sessionId": e.sessionID()})
	case "Target.getTargets":
		e.result(m, map[string]any{"targetInfos": []any{e.targetInfo()}})
	case "Target.getTargetInfo":
		e.result(m, map[string]any{"targetInfo": e.targetInfo()})
	case "Target.getBrowserContexts":
		e.result(m, map[string]any{"browserContextIds": []string{}})
	case "Target.activateTarget":
		if err := e.c.Focus(ctx, e.tab); err != nil {
			e.fail(m, err)
			return
		}
		e.result(m, map[string]any{})
	case "Target.closeTarget":
		e.c.Close(e.tab)
		e.result(m, map[string]any{"success": true})
	case "Target.setAutoAttach":
		// puppeteer / Playwright attach through auto-attach (flatten): tell them about the one page
		e.result(m, map[string]any{})
		var p struct {
			Flatten bool `json:"flatten"`
		}
		json.Unmarshal(m.Params, &p)
		if p.Flatten {
			e.event("Target.attachedToTarget", map[string]any{"sessionId": e.sessionID(), "targetInfo": e.targetInfo(), "waitingForDebugger": false}, false)
		}
	case "Target.detachFromTarget":
		e.result(m, map[string]any{})
	case "Browser.getVersion":
		e.result(m, map[string]any{"protocolVersion": "1.3", "product": "Chrome/0 (Gerdr extension)", "revision": "", "userAgent": "", "jsVersion": ""})
	case "Browser.getWindowForTarget":
		e.result(m, map[string]any{"windowId": e.window, "bounds": map[string]any{"windowState": "normal"}})
	case "Browser.setWindowBounds", "Browser.setDownloadBehavior":
		e.result(m, map[string]any{})
	case "Browser.close":
		e.c.Close(e.tab)
		e.result(m, map[string]any{})
	default:
		e.fail(m, errors.New("not available in your own Chrome: "+m.Method))
	}
}

func (e *Endpoint) onEvent(ev Event) {
	switch ev.Kind {
	case "cdp":
		var params any = json.RawMessage("{}")
		if len(ev.Params) > 0 {
			params = ev.Params
		}
		e.event(ev.Method, params, true)
	case "closed", "detached", "blocked":
		e.mu.Lock()
		already := e.gone
		e.gone = true
		e.mu.Unlock()
		if already {
			return
		}
		e.event("Target.detachedFromTarget", map[string]any{"sessionId": e.sessionID(), "targetId": e.targetID()}, false)
		if ev.Kind == "closed" {
			e.event("Target.targetDestroyed", map[string]any{"targetId": e.targetID()}, false)
		}
	}
}
