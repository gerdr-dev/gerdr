package chromeext

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/gerdr-dev/gerdr/internal/i18n"
)

// The desktop App's own browser (tools/chromeshell on Lightron) connects like an extension and speaks the same
// protocol (hello, open, cdp, close; events cdp, closed), with "app": true in its hello and one more event, popup.
// Its pages are a project's (projbrowser opens them here while the App runs, in the project's profile), shown in the
// Browser pane as the real page: a window of the App's browser placed over the pane (Lightron's View), not a picture.
// The same tab list, actions and stream serve them; Tab.Native tells the UI it can show them natively.
//
// Ids of the App's tabs start at appBase: Chrome's own (the extension's) count from 1, adopted pages from ownedBase.

// ErrNoApp: the desktop App's browser isn't connected (the App is closed, or it's the Windows App).
var ErrNoApp = errors.New("the desktop App's browser is not connected")

// AppConnected: the desktop App's browser is there to open pages in.
func (b *Bridge) AppConnected() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.app != nil
}

// AppEngine: the browser program and data directory of the App that connected last ("" if it didn't say).
func (b *Bridge) AppEngine() (exe, dataDir string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.appEngine, b.appDataDir
}

// AppKind: "electron" when the App that connected last is the Electron one (its program is also the projects' browser
// while it's closed: projbrowser starts it with --gerdr-browser); its arguments before its own (a development build).
func (b *Bridge) AppKind() (kind string, args []string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.appKind, b.appArgs
}

// AppBrowserOnly: what's connected is the browser-only process, not the App (no pane shows its pages).
func (b *Bridge) AppBrowserOnly() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.app != nil && b.appOnly
}

// QuitApp asks the browser-only process to quit (the App needs its data directory) and waits for it to go.
func (b *Bridge) QuitApp(ctx context.Context) {
	b.mu.Lock()
	c := b.app
	only := b.appOnly
	b.mu.Unlock()
	if c == nil || !only {
		return
	}
	qctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	_, _ = c.Call(qctx, "quit", nil)
	cancel()
	select {
	case <-c.done:
	case <-ctx.Done():
	case <-time.After(8 * time.Second):
	}
}

// TakeOrphans: the App's pages whose browser went in the last minute, for the App to open again.
func (b *Bridge) TakeOrphans() []TabInfo {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []TabInfo
	for _, o := range b.orphans {
		if time.Since(o.at) < time.Minute {
			out = append(out, o.TabInfo)
		}
	}
	b.orphans = nil
	return out
}

func (b *Bridge) serveApp(ctx context.Context, c *Conn) {
	b.mu.Lock()
	if old := b.app; old != nil {
		old.ws.Close()
	}
	b.app = c
	b.mu.Unlock()
	b.Log.Info("chromeext: the App's browser connected", "version", c.Version)
	if fn := b.OnApp; fn != nil {
		go fn(true)
	}
	select {
	case <-c.done:
	case <-ctx.Done():
		c.ws.Close()
		<-c.done
	}
	b.mu.Lock()
	if b.app == c {
		b.app = nil
	}
	b.mu.Unlock()
	b.Log.Info("chromeext: the App's browser disconnected")
	if fn := b.OnApp; fn != nil {
		go fn(false)
	}
}

// OpenNative opens raw in the desktop App's browser, in the profile named profile (the project's, a directory of the
// App's browser data directory), as project's tab. replaces: the tab this one takes the place of (Tab.Replaces).
func (b *Bridge) OpenNative(ctx context.Context, project, profile, raw string, width, height, replaces int) (*Tab, error) {
	if profile == "" {
		return nil, errors.New(i18n.S("这个 App 太旧，没告诉后端它的浏览器数据在哪：更新 App", "This App is too old to say where its browser keeps its data: update the App"))
	}
	b.mu.Lock()
	c := b.app
	b.mu.Unlock()
	if c == nil {
		return nil, ErrNoApp
	}
	octx, cancel := context.WithTimeout(ctx, 30*time.Second)
	res, err := c.Call(octx, "open", map[string]any{"profile": profile, "width": width, "height": height})
	cancel()
	if err != nil {
		return nil, err
	}
	var r struct {
		Tab int `json:"tab"`
	}
	if err := json.Unmarshal(res, &r); err != nil || r.Tab == 0 {
		return nil, errors.New(i18n.S("App 的浏览器没开出页面", "The App's browser didn't open a page"))
	}
	t, err := b.nativeTab(c, r.Tab, project, raw, 0, replaces)
	if err != nil {
		return nil, err
	}
	ectx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// location.replace: the blank page it opened on stays out of the history, and the first load is logged
	target, _ := json.Marshal(raw)
	p, _ := json.Marshal(map[string]any{"expression": "location.replace(" + string(target) + ")"})
	if raw != "" && raw != "about:blank" {
		if _, err := cdp(ectx, c, r.Tab, "Runtime.evaluate", p); err != nil {
			b.Close(r.Tab, false)
			return nil, err
		}
	}
	return t, nil
}

// nativeTab registers one of the App's pages and turns its logs on.
func (b *Bridge) nativeTab(c *Conn, id int, project, raw string, opener, replaces int) (*Tab, error) {
	t := newTab(id, raw)
	t.Project, t.Native, t.Opener, t.Replaces = project, true, opener, replaces
	t.send = func(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
		return cdp(ctx, c, id, method, params)
	}
	ep, err := newEndpoint(c, id, 0, func(ev Event) { b.tabEvent(t, ev) })
	if err != nil {
		c.Close(id)
		return nil, err
	}
	t.ep, t.WS, t.HTTP = ep, ep.URL, ep.HTTPURL()
	t.close = func(bool) {
		ep.Close()
		c.Close(id)
	}
	b.mu.Lock()
	b.tabs[id] = t
	b.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, m := range []string{"Page.enable", "Runtime.enable", "Network.enable", "Log.enable"} {
		cdp(ctx, c, id, m, nil)
	}
	b.tabsChanged()
	return t, nil
}

// appPopup: a page in the App's browser opened another, which the App opened as a page of its own (tab).
func (b *Bridge) appPopup(c *Conn, tab, opener int, raw string) {
	b.mu.Lock()
	parent := b.tabs[opener]
	b.mu.Unlock()
	project := ""
	if parent != nil {
		project = parent.Project
	}
	if _, err := b.nativeTab(c, tab, project, raw, opener, 0); err != nil {
		b.Log.Warn("chromeext: the App's popup", "err", err)
	}
}

// TabInfo: an open page, for moving it elsewhere (projbrowser).
type TabInfo struct {
	ID      int
	Project string
	URL     string // where it is now
}

// ProjectTabs: the pages of a project's own headless browser that are open now.
func (b *Bridge) ProjectTabs(project string) []TabInfo {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []TabInfo
	for _, t := range b.tabs {
		if t.Project == project && !t.Native && t.Gone == "" {
			out = append(out, TabInfo{ID: t.ID, Project: project, URL: t.CurrentURL()})
		}
	}
	return out
}
