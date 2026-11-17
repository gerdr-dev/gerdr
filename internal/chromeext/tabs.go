package chromeext

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"runtime"
	"sort"
	"time"

	"github.com/gerdr-dev/gerdr/internal/i18n"
)

// Tab is a window Gerdr opened in the user's Chrome. It stays open until closed (`gerdr browser close`, the user
// closing it, the extension stopping it); its endpoint takes one CDP client at a time.
type Tab struct {
	ID      int    `json:"tab"`
	Profile string `json:"profile"`
	URL     string `json:"url"`            // what it was opened on
	WS      string `json:"ws"`             // CDP browser WebSocket
	HTTP    string `json:"http"`           // the same as http://host:port (for clients that fetch /json/version)
	Gone    string `json:"gone,omitempty"` // why it can't be driven any more; "" while it can
	Opened  int64  `json:"opened"`

	conn *Conn
	ep   *Endpoint
	log  *tabLog // console, network, the open dialog (actions.go)
}

// OpenOptions for Bridge.Open.
type OpenOptions struct {
	URL     string
	Profile string // the extension's id in one Chrome profile; "" picks by site
	Why     string // shown on the extension's "allow this site" window
	Focused bool
	Width   int // window size; 0 = the extension's default (1280 x 900)
	Height  int
}

// Open a window on o.URL in the user's Chrome. When the site isn't allowed yet the extension asks the user (up to 10
// minutes) in every connected profile, and the one they allow it in is used.
func (b *Bridge) Open(ctx context.Context, o OpenOptions) (*Tab, error) {
	u, err := url.Parse(o.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("%s: %q", i18n.S("只能打开 http / https 地址", "Only http / https URLs can be opened"), o.URL)
	}
	conn, needGrant, err := b.Pick(o.Profile, o.URL)
	if errors.Is(err, ErrNotConnected) {
		// the extension is only connected while Chrome runs: start it as usual (the user's own profile) and wait
		if launchChrome() == nil {
			wctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			if b.Wait(wctx) {
				conn, needGrant, err = b.Pick(o.Profile, o.URL)
			}
			cancel()
		}
	}
	if errors.Is(err, ErrNotConnected) {
		return nil, errors.New(i18n.S("没连上你的 Chrome：确认 Chrome 开着、装了 Gerdr 浏览器插件（docs/guide 里有装法）",
			"Couldn't reach your Chrome: make sure Chrome is open with the Gerdr browser extension installed (see docs/guide)"))
	}
	if err != nil {
		return nil, err
	}
	if needGrant {
		ask := []*Conn{conn}
		if o.Profile == "" {
			ask = b.conns_()
		}
		gctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		why := o.Why
		if why == "" {
			why = "Gerdr"
		}
		granted, err := Grant(gctx, ask, o.URL, why)
		cancel()
		if err != nil {
			return nil, err
		}
		if granted == nil {
			return nil, fmt.Errorf("%s: %s", i18n.S("你没有允许在这个网站上操作", "You didn't allow it on this site"), u.Hostname())
		}
		conn = granted
	}
	// open on about:blank, turn on the console / network logs, then load the page: the first load is logged too
	octx, cancel := context.WithTimeout(ctx, 30*time.Second)
	tabID, window, err := conn.Open(octx, "about:blank", o.Focused, o.Width, o.Height)
	cancel()
	if err != nil {
		return nil, err
	}
	t := &Tab{ID: tabID, Profile: conn.ID, URL: o.URL, Opened: time.Now().UnixMilli(), conn: conn, log: newTabLog()}
	ep, err := newEndpoint(conn, tabID, window, func(ev Event) { b.tabEvent(t, ev) })
	if err != nil {
		conn.Close(tabID)
		return nil, err
	}
	t.ep, t.WS, t.HTTP = ep, ep.URL, ep.HTTPURL()
	b.mu.Lock()
	b.tabs[tabID] = t
	b.mu.Unlock()
	ectx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for _, m := range []string{"Page.enable", "Runtime.enable", "Network.enable", "Log.enable"} {
		cdp(ectx, conn, tabID, m, nil)
	}
	// location.replace rather than Page.navigate: the about:blank we started on stays out of the history (back)
	target, _ := json.Marshal(o.URL)
	p, _ := json.Marshal(map[string]any{"expression": "location.replace(" + string(target) + ")"})
	if _, err := cdp(ectx, conn, tabID, "Runtime.evaluate", p); err != nil {
		b.Close(tabID, false)
		return nil, err
	}
	return t, nil
}

func (b *Bridge) tabEvent(t *Tab, ev Event) {
	var why string
	switch {
	case ev.Kind == "cdp":
		t.log.record(ev.Method, ev.Params)
		return
	case ev.Kind == "closed":
		why = i18n.S("窗口关了", "The window was closed")
	case ev.Kind == "blocked":
		why = i18n.S("页面跳到了没允许的网站，插件停止了操作：", "The page went to a site that isn't allowed, so the extension stopped: ") + ev.URL
	case ev.Kind == "detached" && ev.Reason == "canceled_by_user":
		why = i18n.S("你在浏览器的调试提示条上点了「取消」", "You clicked Cancel on the browser's debugging bar")
	case ev.Kind == "detached":
		why = i18n.S("浏览器断开了对这个窗口的调试", "The browser detached from this window")
	default:
		return
	}
	b.mu.Lock()
	if t.Gone == "" {
		t.Gone = why
	}
	closed := ev.Kind == "closed" && b.tabs[t.ID] == t
	if closed {
		delete(b.tabs, t.ID)
	}
	b.mu.Unlock()
	if closed {
		t.ep.Close()
	}
}

// Tabs Gerdr has open, oldest first.
func (b *Bridge) Tabs() []Tab {
	b.mu.Lock()
	out := make([]Tab, 0, len(b.tabs))
	for _, t := range b.tabs {
		out = append(out, *t)
	}
	b.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Opened < out[j].Opened })
	return out
}

// tab looks a window up; 0 means the one opened last.
func (b *Bridge) tab(id int) (*Tab, error) {
	b.mu.Lock()
	if id == 0 {
		var last *Tab
		for _, t := range b.tabs {
			if last == nil || t.Opened > last.Opened {
				last = t
			}
		}
		if last == nil {
			b.mu.Unlock()
			return nil, errors.New(i18n.S("Gerdr 还没在 Chrome 里开窗口：先 open", "Gerdr has no window open in Chrome: open one first"))
		}
		id = last.ID
	}
	t := b.tabs[id]
	gone := ""
	if t != nil {
		gone = t.Gone
	}
	b.mu.Unlock()
	if t == nil {
		return nil, fmt.Errorf("%s: %d", i18n.S("没有这个窗口（不是 Gerdr 开的，或者已经关了）", "No such window (not opened by Gerdr, or closed)"), id)
	}
	if gone != "" {
		return nil, errors.New(gone)
	}
	return t, nil
}

// Close the window (keep: hand it to the user instead, no longer driven). 0 = the one opened last.
func (b *Bridge) Close(id int, keep bool) error {
	if id == 0 {
		t, err := b.tab(0)
		if err != nil {
			return err
		}
		id = t.ID
	}
	b.mu.Lock()
	t := b.tabs[id]
	delete(b.tabs, id)
	b.mu.Unlock()
	if t == nil {
		return nil
	}
	t.ep.Close()
	if keep {
		t.conn.Release(id)
	} else {
		t.conn.Close(id)
	}
	return nil
}

// launchChrome starts the user's Chrome normally (their own profile), in the background.
func launchChrome() error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", "-g", "-a", "Google Chrome").Run()
	case "windows":
		return exec.Command("cmd", "/c", "start", "", "chrome").Run()
	}
	return exec.Command("google-chrome").Start()
}
