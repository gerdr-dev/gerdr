// Package el is the desktop core's side of the Electron App (desktop/main.js): the same few things the Lightron shell
// used from Lightron (an App with windows, menus, a badge, views of projects' pages), done by asking Electron's main
// process over this process's stdin / stdout, one JSON object a line. So the core's code (moved here from
// tools/chromeshell) changed little: lightron.X became el.X.
//
// Core → Electron: {"id": n, "op": …, …}; Electron answers {"ev": "reply", "id": n, "ok", "result", "error"}.
//
// The first line is always {"op": "script", "path", "hot"}: the main-process script to run (Options.ShellJS, the App's
// own logic, which ships with this binary: docs/dev/SHELL.md). desktop/main.js, the frozen loader, takes that one
// itself; every other line goes to the script, which answers "ready" once it runs.
//
//	start {url, port}            the backend is up: open the first window at its login link
//	newWindow {url} → window id  windows {} → [ids]   focus {win}   load {win, url}   eval {win, js} → value
//	badge {label}   menu {items}   dockMenu {items}   quit {}   webPid {win} → pid   engine {} → {exe, dataDir, kind}
//	relaunch {}   focused {} → bool   close {win}
//	openExternal {url}   openPath {path, reveal}
//	notify {win, title, body, sessionId, space}   dialog {kind, title, detail, buttons, defaultId, cancelId} → index
//	openView {profile, url} → view id   viewCdp {view, method, params} → result   viewPlace {view, win, rect, radius}
//	viewHide {view}   viewClose {view}
//
// Electron → core: {"ev": …}
//
//	view kinds also: focus (the page took the focus)
//	ready {browserOnly, port}   message {win, data}   window {win, open}   menu {item, win}
//	noteClick {win, sessionId, space}   view {view, kind: cdp | popup | closed, method, params, url}   quit {}
package el

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"strconv"
	"sync"
	"time"
)

// MenuItem is one item of a menu (Lightron's shape).
type MenuItem struct {
	Label       string
	Accelerator string
	Role        string // hide, hideOthers, unhide, quit, close, toggleFullScreen, zoomIn, zoomOut, resetZoom, devTools, editMenu, windowMenu…
	Click       func(w *Window)
	Submenu     []MenuItem
	Separator   bool
}

// Separator is a menu separator line.
var Separator = MenuItem{Separator: true}

// ErrNoBrowser: kept for the core's code (Lightron's); Electron always has its engine.
var ErrNoBrowser = errors.New("no browser")

// WindowOptions: the first window's size (Electron keeps the last one's bounds itself).
type WindowOptions struct{ Width, Height int }

// Options: what Run needs (the subset of Lightron's the core sets).
type Options struct {
	Name, ID, Version string
	Icon              []byte
	// URLFunc starts the backend and returns the first window's address (a login link). Not called in browser-only mode.
	URLFunc func() (string, error)
	Host    string
	Window  WindowOptions
	// ShellJS: the main-process script desktop/main.js loads (its path on disk) and Hot, what it and this core are
	// (HotID): sent first, before anything else (docs/dev/SHELL.md)
	ShellJS, Hot string
	Menu         []MenuItem
	DockMenu     []MenuItem
	OnMessage    func(w *Window, raw json.RawMessage)
	// OnWindowClose: a window is gone (called once it is off Windows())
	OnWindowClose func(w *Window)
	// OnNoteClick: a notification shown with App.Notify was clicked.
	OnNoteClick func(w *Window, sessionID, space string)
	BrowserArgs []string
	DataDir     string
	Logf        func(string, ...any)
	OnReady     func(a *App)
	// BrowserOnly: started by the backend as the projects' browser (no UI windows); URLFunc isn't called.
	BrowserOnly bool
}

// App is the running Electron App.
type App struct {
	opts Options
	out  *json.Encoder
	outM sync.Mutex

	mu      sync.Mutex
	next    int64
	waiting map[int64]chan reply
	windows map[string]*Window
	views   map[string]*View
	clicks  map[string]func(*Window)
	quit    chan struct{}
	quitted bool
}

type reply struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result"`
	Error  string          `json:"error"`
}

// Window is one of the App's windows.
type Window struct {
	app *App
	id  string
}

// ID: the window's id (Electron's BrowserWindow id).
func (w *Window) ID() string { return w.id }

// Eval runs js in the window's page.
func (w *Window) Eval(js string) error {
	_, err := w.app.call(map[string]any{"op": "eval", "win": w.id, "js": js})
	return err
}

// EvalValue runs js in the window's page and returns its value (promises awaited).
func (w *Window) EvalValue(js string) (json.RawMessage, error) {
	return w.app.call(map[string]any{"op": "eval", "win": w.id, "js": js})
}

// Load points the window at url (the backend's address or a path at the pages' host).
func (w *Window) Load(url string) error {
	_, err := w.app.call(map[string]any{"op": "load", "win": w.id, "url": url})
	return err
}

// Focus brings the window up.
func (w *Window) Focus() { _, _ = w.app.call(map[string]any{"op": "focus", "win": w.id}) }

// Close closes the window (as its close button would).
func (w *Window) Close() { _, _ = w.app.call(map[string]any{"op": "close", "win": w.id}) }

// Call: the DevTools protocol on the window's page isn't offered (the core used it for Page.navigate: Load).
func (w *Window) Call(method string, params any) (json.RawMessage, error) {
	if method == "Page.navigate" {
		if m, ok := params.(map[string]any); ok {
			if u, ok := m["url"].(string); ok {
				return nil, w.Load(u)
			}
		}
	}
	return nil, fmt.Errorf("el: %s on a window isn't offered", method)
}

// Run starts talking to Electron (this process's stdin / stdout) and returns once the App quits.
func Run(o Options) error {
	if o.Logf == nil {
		o.Logf = log.Printf
	}
	a := &App{opts: o, out: json.NewEncoder(os.Stdout), waiting: map[int64]chan reply{}, windows: map[string]*Window{},
		views: map[string]*View{}, clicks: map[string]func(*Window){}, quit: make(chan struct{})}
	if o.ShellJS != "" {
		_ = a.send(map[string]any{"op": "script", "path": o.ShellJS, "hot": o.Hot})
	}
	go a.read(os.Stdin)
	<-a.quit
	return nil
}

func (a *App) send(m map[string]any) error {
	a.outM.Lock()
	defer a.outM.Unlock()
	return a.out.Encode(m)
}

func (a *App) call(m map[string]any) (json.RawMessage, error) {
	a.mu.Lock()
	if a.quitted {
		a.mu.Unlock()
		return nil, errors.New("el: the App quit")
	}
	a.next++
	id := a.next
	ch := make(chan reply, 1)
	a.waiting[id] = ch
	a.mu.Unlock()
	m["id"] = id
	if err := a.send(m); err != nil {
		return nil, err
	}
	select {
	case r := <-ch:
		if !r.OK {
			return nil, errors.New(r.Error)
		}
		return r.Result, nil
	case <-time.After(60 * time.Second):
		a.mu.Lock()
		delete(a.waiting, id)
		a.mu.Unlock()
		return nil, fmt.Errorf("el: %v timed out", m["op"])
	case <-a.quit:
		return nil, errors.New("el: the App quit")
	}
}

func (a *App) window(id string) *Window {
	if id == "" {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	w := a.windows[id]
	if w == nil {
		w = &Window{app: a, id: id}
		a.windows[id] = w
	}
	return w
}

func (a *App) read(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		var m struct {
			Ev        string          `json:"ev"`
			ID        int64           `json:"id"`
			OK        bool            `json:"ok"`
			Result    json.RawMessage `json:"result"`
			Error     string          `json:"error"`
			Win       string          `json:"win"`
			Open      bool            `json:"open"`
			Data      json.RawMessage `json:"data"`
			Item      string          `json:"item"`
			SessionID string          `json:"sessionId"`
			Space     string          `json:"space"`
			View      string          `json:"view"`
			Kind      string          `json:"kind"`
			Method    string          `json:"method"`
			Params    json.RawMessage `json:"params"`
			URL       string          `json:"url"`
			Port      int             `json:"port"`
		}
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			continue
		}
		switch m.Ev {
		case "reply":
			a.mu.Lock()
			ch := a.waiting[m.ID]
			delete(a.waiting, m.ID)
			a.mu.Unlock()
			if ch != nil {
				ch <- reply{m.OK, m.Result, m.Error}
			}
		case "ready":
			go a.ready()
		case "message":
			if a.opts.OnMessage != nil {
				go a.opts.OnMessage(a.window(m.Win), m.Data)
			}
		case "window":
			if !m.Open {
				a.mu.Lock()
				w := a.windows[m.Win]
				delete(a.windows, m.Win)
				a.mu.Unlock()
				if w != nil && a.opts.OnWindowClose != nil {
					go a.opts.OnWindowClose(w)
				}
			} else {
				a.window(m.Win)
			}
		case "menu":
			a.mu.Lock()
			f := a.clicks[m.Item]
			a.mu.Unlock()
			if f != nil {
				go f(a.window(m.Win))
			}
		case "noteClick":
			if a.opts.OnNoteClick != nil {
				go a.opts.OnNoteClick(a.window(m.Win), m.SessionID, m.Space)
			}
		case "view":
			a.mu.Lock()
			v := a.views[m.View]
			a.mu.Unlock()
			if v != nil {
				v.event(m.Kind, m.Method, m.Params, m.URL)
			}
		case "quit":
			a.stop()
			return
		}
	}
	// Electron went away
	a.stop()
}

func (a *App) stop() {
	a.mu.Lock()
	if a.quitted {
		a.mu.Unlock()
		return
	}
	a.quitted = true
	a.mu.Unlock()
	close(a.quit)
}

func (a *App) ready() {
	a.SetMenu(a.opts.Menu)
	a.SetDockMenu(a.opts.DockMenu)
	if !a.opts.BrowserOnly && a.opts.URLFunc != nil {
		u, err := a.opts.URLFunc()
		if err != nil {
			a.opts.Logf("el: start: %v", err)
			_, _ = a.call(map[string]any{"op": "dialog", "kind": "error", "title": a.opts.Name, "detail": err.Error()})
			_, _ = a.call(map[string]any{"op": "quit"})
			return
		}
		port := 0
		if p := portOf(u); p != "" {
			port, _ = strconv.Atoi(p)
		}
		if _, err := a.call(map[string]any{"op": "start", "url": u, "port": port}); err != nil {
			a.opts.Logf("el: start: %v", err)
		}
	}
	if a.opts.OnReady != nil {
		a.opts.OnReady(a)
	}
}

func portOf(u string) string {
	pu, err := url.Parse(u)
	if err != nil {
		return ""
	}
	return pu.Port()
}

// Windows: the App's windows now.
func (a *App) Windows() []*Window {
	raw, err := a.call(map[string]any{"op": "windows"})
	if err != nil {
		return nil
	}
	var ids []string
	_ = json.Unmarshal(raw, &ids)
	ws := make([]*Window, 0, len(ids))
	for _, id := range ids {
		ws = append(ws, a.window(id))
	}
	return ws
}

// NewWindow opens a window at path (at the pages' host: the cookie the first one got lets it in).
func (a *App) NewWindow(path string) error {
	_, err := a.call(map[string]any{"op": "newWindow", "url": "http://gerdr" + path})
	return err
}

// SetBadge: the Dock icon's label ("" = none).
func (a *App) SetBadge(label string) { _, _ = a.call(map[string]any{"op": "badge", "label": label}) }

// SetMenu: the menu bar.
func (a *App) SetMenu(items []MenuItem) {
	a.mu.Lock()
	for k := range a.clicks {
		if len(k) > 2 && k[:2] == "m:" {
			delete(a.clicks, k)
		}
	}
	a.mu.Unlock()
	_, _ = a.call(map[string]any{"op": "menu", "items": a.menuData(items, "m")})
}

// SetDockMenu: the Dock icon's menu.
func (a *App) SetDockMenu(items []MenuItem) {
	a.mu.Lock()
	for k := range a.clicks {
		if len(k) > 2 && k[:2] == "d:" {
			delete(a.clicks, k)
		}
	}
	a.mu.Unlock()
	_, _ = a.call(map[string]any{"op": "dockMenu", "items": a.menuData(items, "d")})
}

func (a *App) menuData(items []MenuItem, prefix string) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for i, it := range items {
		if it.Separator {
			out = append(out, map[string]any{"separator": true})
			continue
		}
		m := map[string]any{"label": it.Label}
		if it.Accelerator != "" {
			m["accelerator"] = it.Accelerator
		}
		if it.Role != "" {
			m["role"] = it.Role
		}
		id := fmt.Sprintf("%s:%d", prefix, i)
		if it.Submenu != nil {
			m["submenu"] = a.menuData(it.Submenu, id)
		}
		if it.Click != nil {
			a.mu.Lock()
			a.clicks[id] = it.Click
			a.mu.Unlock()
			m["id"] = id
		}
		out = append(out, m)
	}
	return out
}

// OpenExternal opens a link in the default browser (http, https, mailto only).
func (a *App) OpenExternal(u string) { _, _ = a.call(map[string]any{"op": "openExternal", "url": u}) }

// OpenPath opens a file with its default program, or shows it in Finder / Explorer (reveal).
func (a *App) OpenPath(p string, reveal bool) {
	_, _ = a.call(map[string]any{"op": "openPath", "path": p, "reveal": reveal})
}

// Quit quits the App.
func (a *App) Quit() { _, _ = a.call(map[string]any{"op": "quit"}) }

// Relaunch quits and starts the App again (a backend update with a new core or shell script: docs/dev/SHELL.md).
func (a *App) Relaunch() error {
	_, err := a.call(map[string]any{"op": "relaunch"})
	return err
}

// Focused: one of the App's windows is the active one (the person may be using it). An error (a script without the
// op) counts as focused.
func (a *App) Focused() bool {
	raw, err := a.call(map[string]any{"op": "focused"})
	return err != nil || string(raw) != "false"
}

// Notify shows a notification; its click comes back to Options.OnNoteClick.
func (a *App) Notify(w *Window, title, body, sessionID, space string) {
	win := ""
	if w != nil {
		win = w.id
	}
	_, _ = a.call(map[string]any{"op": "notify", "win": win, "title": title, "body": body, "sessionId": sessionID, "space": space})
}

// Dialog asks with buttons and returns the index of the one picked.
func (a *App) Dialog(kind, title, detail string, buttons []string, defaultID, cancelID int) int {
	raw, err := a.call(map[string]any{"op": "dialog", "kind": kind, "title": title, "detail": detail, "buttons": buttons,
		"defaultId": defaultID, "cancelId": cancelID})
	if err != nil {
		return cancelID
	}
	n, _ := strconv.Atoi(string(raw))
	return n
}

// WebPid: the process of the window's page.
func (a *App) WebPid(w *Window) int {
	raw, err := a.call(map[string]any{"op": "webPid", "win": w.id})
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(string(raw))
	return n
}

// Engine: the Electron program, what it needs before its own arguments (a development build: the app's path) and its
// userData (the projects' partitions are there). The backend runs it as the projects' browser while the App is closed.
func (a *App) Engine() (exe string, args []string, dataDir string) {
	raw, err := a.call(map[string]any{"op": "engine"})
	if err != nil {
		return "", nil, ""
	}
	var e struct {
		Exe     string   `json:"exe"`
		Args    []string `json:"args"`
		DataDir string   `json:"dataDir"`
	}
	_ = json.Unmarshal(raw, &e)
	return e.Exe, e.Args, e.DataDir
}

// ViewOptions: a project's page.
type ViewOptions struct {
	Profile string // the partition (persist:<Profile>)
	Tab     int    // the backend's id for it (the UI asks Electron for a still by it)
	URL     string
	OnEvent func(method string, params json.RawMessage)
	OnPopup func(url string)
	OnClose func()
	// OnFocus: the page took the focus (a click in it).
	OnFocus func()
}

// View is a project's page (a WebContentsView).
type View struct {
	app  *App
	id   string
	opts ViewOptions
	once sync.Once
}

// OpenView opens a project's page in its partition, parked (painting, not on screen) until placed.
func (a *App) OpenView(o ViewOptions) (*View, error) {
	raw, err := a.call(map[string]any{"op": "openView", "profile": o.Profile, "url": o.URL, "tab": o.Tab})
	if err != nil {
		return nil, err
	}
	var id string
	if json.Unmarshal(raw, &id) != nil || id == "" {
		return nil, errors.New("el: no view id")
	}
	v := &View{app: a, id: id, opts: o}
	a.mu.Lock()
	a.views[id] = v
	a.mu.Unlock()
	return v, nil
}

func (v *View) event(kind, method string, params json.RawMessage, url string) {
	switch kind {
	case "cdp":
		if v.opts.OnEvent != nil {
			v.opts.OnEvent(method, params)
		}
	case "focus":
		if v.opts.OnFocus != nil {
			go v.opts.OnFocus()
		}
	case "popup":
		if v.opts.OnPopup != nil {
			go v.opts.OnPopup(url)
		}
	case "closed":
		v.app.mu.Lock()
		delete(v.app.views, v.id)
		v.app.mu.Unlock()
		v.once.Do(func() {
			if v.opts.OnClose != nil {
				go v.opts.OnClose()
			}
		})
	}
}

// Call sends a DevTools protocol command to the page.
func (v *View) Call(method string, params any) (json.RawMessage, error) {
	return v.app.call(map[string]any{"op": "viewCdp", "view": v.id, "method": method, "params": params})
}

// Place puts the page over a box of window w (CSS px of w's page; Electron applies the page's zoom), its corners
// rounded by radius (CSS px; the pane's inside).
func (v *View) Place(w *Window, x, y, width, height, radius float64) error {
	_, err := v.app.call(map[string]any{"op": "viewPlace", "view": v.id, "win": w.id, "rect": []float64{x, y, width, height}, "radius": radius})
	return err
}

// Hide takes the page off screen; it keeps painting.
func (v *View) Hide() error {
	_, err := v.app.call(map[string]any{"op": "viewHide", "view": v.id})
	return err
}

// Mask: nothing to cut in Electron (the pane shows a still under our menus instead).
func (v *View) Mask(holes [][5]float64, dpr float64) error { return nil }

// Close closes the page.
func (v *View) Close() { _, _ = v.app.call(map[string]any{"op": "viewClose", "view": v.id}) }
