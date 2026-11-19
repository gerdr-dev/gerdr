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
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gerdr-dev/gerdr/internal/i18n"
)

// Tab is a window Gerdr drives: one it opened in the user's Chrome through the extension, or a page of a project's own
// browser (internal/projbrowser, Adopt). It stays open until closed (`gerdr browser close`, the user closing it, the
// extension stopping it). An extension tab's endpoint takes one CDP client at a time.
type Tab struct {
	ID      int    `json:"tab"`
	Profile string `json:"profile,omitempty"`
	Project string `json:"project,omitempty"` // a project's own browser (projbrowser); "" = the user's Chrome
	// Opener: the tab whose page opened this one. A Browser pane showing the opener follows it here, as a browser
	// does with a new tab, and back when it closes (ui/src/lib/actions/browser.ts)
	Opener int `json:"opener,omitempty"`
	// Native: a page of the desktop App's own browser (app.go): the App shows it in the pane as the real page
	Native bool `json:"native,omitempty"`
	// Replaces: the tab this one took over (the same page moved into the App's browser when it connected): a pane
	// showing that one moves here (ui/src/lib/actions/browser.ts)
	Replaces int    `json:"replaces,omitempty"`
	URL      string `json:"url"`            // what it was opened on
	WS       string `json:"ws,omitempty"`   // CDP browser WebSocket (extension tabs only)
	HTTP     string `json:"http,omitempty"` // the same as http://host:port (for clients that fetch /json/version)
	Gone     string `json:"gone,omitempty"` // why it can't be driven any more; "" while it can
	Opened   int64  `json:"opened"`

	// send runs one DevTools command in the page: through the extension, or the project browser's pipe
	send func(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error)
	// sendQuiet is send for the person's own input from the Browser pane: the extension then draws no cursor for it
	// (cdpTo). nil = send (a project browser's page has no such cursor).
	sendQuiet func(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error)
	close     func(keep bool)
	// resize the window itself (the Browser pane's size); nil where the window isn't ours to size (the user's Chrome)
	resize func(ctx context.Context, width, height int) error
	ep     *Endpoint
	log    *tabLog // console, network, the open dialog (actions.go)
	watch  *watchers
	cast   *caster       // the screencast its viewers share (view.go)
	cur    *atomic.Value // CurrentURL
	// phone layout for remote viewers of an App page (phone.go)
	phoneOnce *sync.Once
	phoneL    *phoneLayout
}

// watchers get a tab's DevTools events (the Browser pane's screencast, view.go).
type watchers struct {
	mu   sync.Mutex
	next int
	fns  map[int]func(method string, params json.RawMessage)
}

func (w *watchers) add(fn func(string, json.RawMessage)) func() {
	w.mu.Lock()
	w.next++
	id := w.next
	w.fns[id] = fn
	w.mu.Unlock()
	return func() {
		w.mu.Lock()
		delete(w.fns, id)
		w.mu.Unlock()
	}
}

func (w *watchers) emit(method string, params json.RawMessage) {
	w.mu.Lock()
	fns := make([]func(string, json.RawMessage), 0, len(w.fns))
	for _, fn := range w.fns {
		fns = append(fns, fn)
	}
	w.mu.Unlock()
	for _, fn := range fns {
		fn(method, params)
	}
}

func newTab(id int, url string) *Tab {
	t := &Tab{ID: id, URL: url, Opened: time.Now().UnixMilli(), log: newTabLog(), watch: &watchers{fns: map[int]func(string, json.RawMessage){}}}
	t.cast = &caster{t: t}
	t.cur = &atomic.Value{}
	t.phoneOnce = &sync.Once{}
	return t
}

// event takes one DevTools event of the page.
func (t *Tab) event(method string, params json.RawMessage) {
	if method == "Page.frameNavigated" || method == "Page.navigatedWithinDocument" {
		var p struct {
			Frame struct {
				ParentID string `json:"parentId"`
				URL      string `json:"url"`
				Fragment string `json:"urlFragment"`
			} `json:"frame"`
			URL string `json:"url"` // navigatedWithinDocument
		}
		if json.Unmarshal(params, &p) == nil {
			u := p.URL
			if u == "" && p.Frame.ParentID == "" {
				u = p.Frame.URL + p.Frame.Fragment
			}
			if u != "" && !strings.HasPrefix(u, "about:") && !strings.HasPrefix(u, "data:") {
				t.cur.Store(u)
			}
		}
	}
	t.log.record(method, params)
	t.watch.emit(method, params)
}

// CurrentURL: where the page is now (the last main-frame navigation), else where it was opened.
func (t *Tab) CurrentURL() string {
	if u, ok := t.cur.Load().(string); ok && u != "" {
		return u
	}
	return t.URL
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
	t := newTab(tabID, o.URL)
	t.Profile = conn.ID
	t.send = func(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
		return cdp(ctx, conn, tabID, method, params)
	}
	t.sendQuiet = func(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
		return cdpTo(ctx, conn, tabID, method, params, true)
	}
	ep, err := newEndpoint(conn, tabID, window, func(ev Event) { b.tabEvent(t, ev) })
	if err != nil {
		conn.Close(tabID)
		return nil, err
	}
	t.ep, t.WS, t.HTTP = ep, ep.URL, ep.HTTPURL()
	t.close = func(keep bool) {
		ep.Close()
		if keep {
			conn.Release(tabID)
		} else {
			conn.Close(tabID)
		}
	}
	b.mu.Lock()
	b.tabs[tabID] = t
	b.mu.Unlock()
	b.tabsChanged()
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
		t.event(ev.Method, ev.Params)
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
		// its browser went (the browser-only one quitting for the App): the App opens it again (TakeOrphans)
		if t.Native && b.appOnly && t.Project != "" {
			b.orphans = append(b.orphans, orphan{TabInfo{ID: t.ID, Project: t.Project, URL: t.CurrentURL()}, time.Now()})
		}
	}
	b.mu.Unlock()
	if closed {
		t.ep.Close()
	}
	b.tabsChanged()
}

// Tabs Gerdr has open, oldest first.
func (b *Bridge) Tabs() []Tab {
	b.mu.Lock()
	out := make([]Tab, 0, len(b.tabs))
	for _, t := range b.tabs {
		c := *t
		// where it is now, not where it was opened: the Browser pane's page list named a page by the site it had
		// left long ago (user report: "www.baidu.com" for a page at talizen.com)
		c.URL = t.CurrentURL()
		out = append(out, c)
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
			return nil, errors.New(i18n.S("Gerdr 还没开浏览器窗口：先 open", "Gerdr has no browser window open: open one first"))
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
	t.close(keep)
	b.tabsChanged()
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
