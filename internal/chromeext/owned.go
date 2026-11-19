package chromeext

import (
	"context"
	"encoding/json"
	"sync"
)

// Pages of a project's own browser (internal/projbrowser) join the same tab list as the extension's windows, so every
// action, `gerdr browser` and its MCP server work on them unchanged; only how a DevTools command gets to the page
// differs.

// ownedBase keeps adopted ids apart from the extension's: those are Chrome's tab ids, small numbers counted up from 1
// in each Chrome session. Above it, each run starts somewhere else (New).
const ownedBase = 100000

// Page is how to reach a page Gerdr runs itself.
type Page struct {
	Project string
	URL     string
	Opener  int // the tab whose page opened this one (target=_blank, window.open); 0 = none
	Send    func(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error)
	Close   func()
	Resize  func(ctx context.Context, width, height int) error // the window's size (a headless window is all viewport)
}

// Owned is an adopted page: feed it the page's DevTools events, and say when it's gone.
type Owned struct {
	b    *Bridge
	t    *Tab
	once sync.Once
}

// Adopt adds a page to the tab list.
func (b *Bridge) Adopt(p Page) *Owned {
	b.mu.Lock()
	b.nextOwned++
	id := ownedBase + b.nextOwned
	for b.tabs[id] != nil {
		b.nextOwned++
		id = ownedBase + b.nextOwned
	}
	t := newTab(id, p.URL)
	t.Project, t.Opener = p.Project, p.Opener
	t.send = p.Send
	t.resize = p.Resize
	o := &Owned{b: b, t: t}
	t.close = func(bool) { o.once.Do(p.Close) }
	b.inheritSession(t)
	b.tabs[id] = t
	b.mu.Unlock()
	b.tabsChanged()
	return o
}

// ID is the tab id actions take.
func (o *Owned) ID() int { return o.t.ID }

// Event passes one DevTools event of the page on (console / network logs, dialogs, the Browser pane).
func (o *Owned) Event(method string, params json.RawMessage) { o.t.event(method, params) }

// Gone: the page closed by itself (its window shut, the browser exited). Drops it from the list.
func (o *Owned) Gone(why string) {
	o.once.Do(func() {})
	b := o.b
	b.mu.Lock()
	if o.t.Gone == "" {
		o.t.Gone = why
	}
	if b.tabs[o.t.ID] == o.t {
		delete(b.tabs, o.t.ID)
	}
	b.mu.Unlock()
	o.t.watch.emit("gerdr.gone", nil)
	b.tabsChanged()
}
