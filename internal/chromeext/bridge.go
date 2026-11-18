// Package chromeext drives the user's own Chrome through Gerdr's browser extension (browser-extension/,
// docs/dev/CHROME-EXT.md): one connection per Chrome profile that has it. The extension starts the native host
// "com.gerdr.browser" (the gerdr program, host.go), which connects back to the running Gerdr (GET /api/browser/ext).
// `gerdr browser` opens a window there and drives it with DevTools commands, or hands a fake browser endpoint
// (endpoint.go) to a CDP client. Ported from Annulo's internal/extbridge: same protocol, same extension code.
//
// The extension's limits (only tabs it opened, only sites the user allowed, an allow-list of DevTools commands)
// live in the extension: nothing here can widen them.
package chromeext

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/gerdr-dev/gerdr/internal/i18n"
)

// ExtensionIDs are the extensions the native host manifest allows. Only the development build so far (its manifest
// carries a key, so the id is fixed; loaded unpacked); add the Chrome Web Store id once it is listed.
var ExtensionIDs = []string{"okkofibhcocmcnafoamflffmkgiphppc"}

// Bridge holds every connected extension (one per Chrome profile that has it) and the tabs Gerdr opened.
type Bridge struct {
	Log *slog.Logger

	// OnTabs runs (on its own goroutine) when a tab opens or closes: the server tells the UI (the Browser pane)
	OnTabs func()

	mu        sync.Mutex
	conns     map[string]*Conn
	wake      chan struct{} // closed and replaced when connections or allowed sites change
	tabs      map[int]*Tab
	nextOwned int // ids of adopted pages (Adopt)
}

func New(log *slog.Logger) *Bridge {
	if log == nil {
		log = slog.Default()
	}
	// Adopted pages count up from a point that differs from one run to the next: a Browser pane remembers its page's
	// number in the layout, and after a restart a new page with the same number would show up in it
	start := int(time.Now().Unix()%90000) * 10
	return &Bridge{Log: log, conns: map[string]*Conn{}, wake: make(chan struct{}), tabs: map[int]*Tab{}, nextOwned: start}
}

func (b *Bridge) tabsChanged() {
	if fn := b.OnTabs; fn != nil {
		go fn()
	}
}

func (b *Bridge) changed() {
	b.mu.Lock()
	close(b.wake)
	b.wake = make(chan struct{})
	b.mu.Unlock()
}

// Error is an error the extension returned; Code is not_granted, blocked, closed, denied, …
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Message }

func errorText(code, msg string) string {
	switch code {
	case "not_granted":
		return i18n.S("还没允许在这个网站上操作你的浏览器", "Not allowed on this site in your browser yet") + " (" + msg + ")"
	case "blocked":
		return i18n.S("页面跳到了没允许操作的网站，已停止操作", "The page went to a site that isn't allowed, so it stopped") + " (" + msg + ")"
	case "denied":
		return i18n.S("插件不允许这个操作", "The extension doesn't allow this operation") + " (" + msg + ")"
	case "closed", "not_owned":
		return i18n.S("这个窗口已经关了", "This window was closed")
	}
	return msg
}

// Event is something the extension pushed.
type Event struct {
	Kind   string // cdp, closed, detached, blocked
	Tab    int
	Method string
	Params json.RawMessage
	Reason string
	URL    string
}

// Conn is the extension in one Chrome profile.
type Conn struct {
	b  *Bridge
	ws *websocket.Conn
	wm sync.Mutex // gorilla allows one writer at a time

	ID      string // random id the extension keeps in this profile
	Version string

	mu         sync.Mutex
	sites      []string // allowed sites, Chrome match patterns like https://www.doubao.com/*
	name       string   // what the user called this profile in the extension's popup ("Work"), so they can say which
	lastActive int64    // ms; when the user last focused a window of this profile
	next       int64
	pending    map[int64]chan reply
	subs       map[int]func(Event)
	done       chan struct{}
}

type reply struct {
	result json.RawMessage
	err    error
}

type message struct {
	ID         *int64          `json:"id"`
	OK         bool            `json:"ok"`
	Result     json.RawMessage `json:"result"`
	Error      string          `json:"error"`
	Code       string          `json:"code"`
	Event      string          `json:"event"`
	Tab        int             `json:"tab"`
	Method     string          `json:"method"`
	Params     json.RawMessage `json:"params"`
	Reason     string          `json:"reason"`
	URL        string          `json:"url"`
	At         int64           `json:"at"`
	Sites      []string        `json:"sites"`
	LastActive int64           `json:"lastActive"`
	Name       string          `json:"name"`
}

type helloInfo struct {
	ID         string   `json:"id"`
	Version    string   `json:"version"`
	Sites      []string `json:"sites"`
	LastActive int64    `json:"lastActive"`
	Name       string   `json:"name"`
}

// Serve takes over a WebSocket the native host opened until it closes: ask who it is (hello), then register.
func (b *Bridge) Serve(ctx context.Context, ws *websocket.Conn) {
	ws.SetReadLimit(64 << 20)
	c := &Conn{b: b, ws: ws, pending: map[int64]chan reply{}, subs: map[int]func(Event){}, done: make(chan struct{})}
	go c.read()
	hctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	raw, err := c.Call(hctx, "hello", nil)
	cancel()
	var h helloInfo
	if err == nil {
		err = json.Unmarshal(raw, &h)
	}
	if err != nil || !strings.HasPrefix(h.ID, "gerdr-") {
		b.Log.Warn("chromeext: extension connected but hello failed", "err", err)
		ws.Close()
		return
	}
	c.ID, c.Version = h.ID, h.Version
	c.mu.Lock()
	c.sites, c.lastActive, c.name = h.Sites, h.LastActive, strings.TrimSpace(h.Name)
	c.mu.Unlock()
	b.mu.Lock()
	if old := b.conns[c.ID]; old != nil {
		old.ws.Close()
	}
	b.conns[c.ID] = c
	b.mu.Unlock()
	b.changed()
	b.Log.Info("chromeext: extension connected", "id", c.ID, "version", c.Version)
	select {
	case <-c.done:
	case <-ctx.Done():
		ws.Close()
		<-c.done
	}
	b.mu.Lock()
	if b.conns[c.ID] == c {
		delete(b.conns, c.ID)
	}
	b.mu.Unlock()
	b.changed()
	b.Log.Info("chromeext: extension disconnected", "id", c.ID)
}

func (c *Conn) read() {
	defer func() {
		close(c.done)
		c.mu.Lock()
		for id, ch := range c.pending {
			ch <- reply{err: &Error{Code: "disconnected", Message: i18n.S("浏览器插件断开了（Chrome 关了，或者插件被停用）", "The browser extension disconnected (Chrome closed, or the extension was turned off)")}}
			delete(c.pending, id)
		}
		subs := c.subs
		c.subs = map[int]func(Event){}
		c.mu.Unlock()
		for tab, fn := range subs {
			fn(Event{Kind: "closed", Tab: tab})
		}
	}()
	for {
		_, b, err := c.ws.ReadMessage()
		if err != nil {
			return
		}
		var m message
		if json.Unmarshal(b, &m) != nil {
			continue
		}
		if m.Event == "" && m.ID != nil {
			c.mu.Lock()
			ch := c.pending[*m.ID]
			delete(c.pending, *m.ID)
			c.mu.Unlock()
			if ch != nil {
				if m.OK {
					ch <- reply{result: m.Result}
				} else {
					ch <- reply{err: &Error{Code: m.Code, Message: errorText(m.Code, m.Error)}}
				}
			}
			continue
		}
		switch m.Event {
		case "hello":
			c.mu.Lock()
			c.sites, c.lastActive = m.Sites, m.LastActive
			c.mu.Unlock()
			c.b.changed()
		case "name":
			c.mu.Lock()
			c.name = strings.TrimSpace(m.Name)
			c.mu.Unlock()
			c.b.changed()
		case "sites":
			c.mu.Lock()
			c.sites = m.Sites
			c.mu.Unlock()
			c.b.changed()
		case "active":
			c.mu.Lock()
			c.lastActive = m.At
			c.mu.Unlock()
		case "cdp", "closed", "detached", "blocked":
			c.mu.Lock()
			fn := c.subs[m.Tab]
			c.mu.Unlock()
			if fn != nil {
				fn(Event{Kind: m.Event, Tab: m.Tab, Method: m.Method, Params: m.Params, Reason: m.Reason, URL: m.URL})
			}
		}
	}
}

// Call sends a request to the extension and waits for its answer.
func (c *Conn) Call(ctx context.Context, op string, args any) (json.RawMessage, error) {
	c.mu.Lock()
	c.next++
	id := c.next
	ch := make(chan reply, 1)
	c.pending[id] = ch
	c.mu.Unlock()
	if args == nil {
		args = map[string]any{}
	}
	b, err := json.Marshal(map[string]any{"id": id, "op": op, "args": args})
	if err == nil {
		c.wm.Lock()
		c.ws.SetWriteDeadline(time.Now().Add(30 * time.Second))
		err = c.ws.WriteMessage(websocket.TextMessage, b)
		c.wm.Unlock()
	}
	if err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, err
	}
	select {
	case r := <-ch:
		return r.result, r.err
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, ctx.Err()
	case <-c.done:
		return nil, &Error{Code: "disconnected", Message: i18n.S("浏览器插件断开了", "The browser extension disconnected")}
	}
}

// Subscribe receives this tab's events; returns the unsubscribe.
func (c *Conn) Subscribe(tab int, fn func(Event)) func() {
	c.mu.Lock()
	c.subs[tab] = fn
	c.mu.Unlock()
	return func() {
		c.mu.Lock()
		delete(c.subs, tab)
		c.mu.Unlock()
	}
}

// Allows: this profile allowed the site of this URL.
func (c *Conn) Allows(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, s := range c.sites {
		if siteMatch(s, u) {
			return true
		}
	}
	return false
}

// siteMatch: the extension's sites are Chrome match patterns, scheme://host/* (host may be *.example.com).
func siteMatch(pattern string, u *url.URL) bool {
	scheme, rest, ok := strings.Cut(pattern, "://")
	if !ok || (scheme != "*" && scheme != u.Scheme) {
		return false
	}
	host, _, _ := strings.Cut(rest, "/")
	h := strings.ToLower(u.Hostname())
	host = strings.ToLower(host)
	if strings.HasPrefix(host, "*.") {
		return h == host[2:] || strings.HasSuffix(h, host[1:])
	}
	return host == "*" || h == host
}

// Profile is one connected Chrome profile, for `gerdr browser status`.
type Profile struct {
	ID         string   `json:"id"`
	Name       string   `json:"name,omitempty"`
	Version    string   `json:"version"`
	Sites      []string `json:"sites"`
	LastActive int64    `json:"lastActive"`
}

func (b *Bridge) conns_() []*Conn {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]*Conn, 0, len(b.conns))
	for _, c := range b.conns {
		out = append(out, c)
	}
	return out
}

// Profiles are the connected extensions, most recently used first.
func (b *Bridge) Profiles() []Profile {
	out := []Profile{}
	for _, c := range b.conns_() {
		c.mu.Lock()
		out = append(out, Profile{ID: c.ID, Name: c.name, Version: c.Version, Sites: append([]string{}, c.sites...), LastActive: c.lastActive})
		c.mu.Unlock()
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastActive > out[j].LastActive })
	return out
}

// Wait until at least one extension is connected, or ctx ends (false).
func (b *Bridge) Wait(ctx context.Context) bool {
	for {
		b.mu.Lock()
		n, wake := len(b.conns), b.wake
		b.mu.Unlock()
		if n > 0 {
			return true
		}
		select {
		case <-wake:
		case <-ctx.Done():
			return false
		}
	}
}

// ErrNotConnected: no extension is connected.
var ErrNotConnected = errors.New("browser extension not connected")

// find a connected profile by its id or by the name the user gave it in the popup (any case).
func (b *Bridge) find(key string) *Conn {
	b.mu.Lock()
	c := b.conns[key]
	b.mu.Unlock()
	if c != nil {
		return c
	}
	key = strings.TrimSpace(key)
	for _, c := range b.conns_() {
		c.mu.Lock()
		name := c.name
		c.mu.Unlock()
		if name != "" && strings.EqualFold(name, key) {
			return c
		}
	}
	return nil
}

// Pick which Chrome profile opens this URL: id (or the profile's name) if given; else one that allowed the site (the most recently used of
// them); else the most recently used, with needGrant (ask the user first).
func (b *Bridge) Pick(id, raw string) (c *Conn, needGrant bool, err error) {
	if id != "" {
		c = b.find(id)
		if c == nil {
			return nil, false, fmt.Errorf("%s: %s", i18n.S("这个 Chrome 现在没连上（gerdr browser status 列出连上的）", "That Chrome isn't connected (gerdr browser status lists the connected ones)"), id)
		}
		return c, raw != "" && !c.Allows(raw), nil
	}
	conns := b.conns_()
	if len(conns) == 0 {
		return nil, false, ErrNotConnected
	}
	recent := func(list []*Conn) *Conn {
		var best *Conn
		var at int64 = -1
		for _, c := range list {
			c.mu.Lock()
			a := c.lastActive
			c.mu.Unlock()
			if a > at {
				best, at = c, a
			}
		}
		return best
	}
	if raw == "" {
		return recent(conns), false, nil
	}
	var allowed []*Conn
	for _, c := range conns {
		if c.Allows(raw) {
			allowed = append(allowed, c)
		}
	}
	if len(allowed) > 0 {
		return recent(allowed), false, nil
	}
	return recent(conns), true, nil
}

// Grant asks the user to allow the site of raw: each profile's extension pops a small window, the user allows it in
// the one signed in to the site (Chrome confirms once more), the others close. Returns the one allowed, nil if none.
func Grant(ctx context.Context, conns []*Conn, raw, why string) (*Conn, error) {
	if len(conns) == 0 {
		return nil, ErrNotConnected
	}
	buf := make([]byte, 8)
	rand.Read(buf)
	key := "grant-" + hex.EncodeToString(buf)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type answer struct {
		c       *Conn
		granted bool
		err     error
	}
	answers := make(chan answer, len(conns))
	for _, c := range conns {
		c.mu.Lock()
		who := c.name // shown on the Allow window, so with several profiles the user sees which one this is
		c.mu.Unlock()
		go func() {
			res, err := c.Call(ctx, "grant", map[string]any{"url": raw, "why": why, "key": key, "multi": len(conns) > 1, "who": who})
			var r struct {
				Granted bool `json:"granted"`
			}
			if err == nil {
				err = json.Unmarshal(res, &r)
			}
			answers <- answer{c, r.Granted, err}
		}()
	}
	closeOthers := func(except *Conn) {
		for _, c := range conns {
			if c != except {
				cctx, ccancel := context.WithTimeout(context.Background(), 5*time.Second)
				c.Call(cctx, "cancel_grant", map[string]any{"key": key})
				ccancel()
			}
		}
	}
	var firstErr error
	for range conns {
		select {
		case a := <-answers:
			if a.granted {
				go closeOthers(a.c)
				return a.c, nil
			}
			if a.err != nil && firstErr == nil {
				firstErr = a.err
			}
		case <-ctx.Done():
			go closeOthers(nil)
			return nil, ctx.Err()
		}
	}
	if firstErr != nil && len(conns) == 1 {
		return nil, firstErr
	}
	return nil, nil
}

// Open a new window (one tab) in this Chrome; returns the tab and window.
func (c *Conn) Open(ctx context.Context, raw string, focused bool, width, height int) (tab, window int, err error) {
	args := map[string]any{"url": raw, "focused": focused}
	if width > 0 && height > 0 {
		args["width"], args["height"] = width, height
	}
	res, err := c.Call(ctx, "open", args)
	if err != nil {
		return 0, 0, err
	}
	var r struct {
		Tab    int `json:"tab"`
		Window int `json:"window"`
	}
	if err := json.Unmarshal(res, &r); err != nil {
		return 0, 0, err
	}
	return r.Tab, r.Window, nil
}

func (c *Conn) call5s(op string, args any) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c.Call(ctx, op, args)
}

// Close the tab (and its window when it is the only tab there).
func (c *Conn) Close(tab int) { c.call5s("close", map[string]any{"tab": tab}) }

// Release hands the window to the user: no longer debugged or tracked, it becomes theirs.
func (c *Conn) Release(tab int) { c.call5s("release", map[string]any{"tab": tab}) }

// Focus brings the tab's window to the front.
func (c *Conn) Focus(ctx context.Context, tab int) error {
	_, err := c.Call(ctx, "focus", map[string]any{"tab": tab})
	return err
}
