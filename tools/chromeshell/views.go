package main

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/zbysir/lightron"
)

// The App's own browser as one the backend drives (internal/chromeext/app.go, docs/dev/BROWSER-PANE.md): the shell
// connects to the backend like Gerdr's browser extension (GET /api/browser/ext, the same protocol, "app": true in its
// hello) and opens a project's pages as Lightron views (lightron.App.OpenView) in the project's profile. The UI's
// Browser pane then shows the real page: it posts its box ({type: "browserView"}) and the view is placed over it.
// Agents drive the page through the same protocol (cdp), and a viewer elsewhere (a phone) still gets its screencast.

// appTabBase: the App's tabs' ids, apart from the extension's (Chrome's, small) and adopted pages' (100000…).
const appTabBase = 2000000

type appBrowser struct {
	s *shell

	mu    sync.Mutex
	ws    *websocket.Conn
	next  int
	views map[int]*appView

	out chan any // what goes to the backend, in order (events come on Lightron's reading goroutine: never block it)
}

type appView struct {
	v       *lightron.View
	profile string      // its profile's name in the App's browser data directory
	place   chan func() // the latest placement, done in order and coalesced
	placed  bool        // logged the first time
}

func newAppBrowser(s *shell) *appBrowser {
	// Numbers start somewhere else at each start: a pane keeps its page's number in the layout, and after the App
	// reopened a new page under the same number showed up in an old pane ("tab 2000001, which isn't one of the
	// App's pages", seen in the log)
	start := int(time.Now().Unix()%100000) * 10
	return &appBrowser{s: s, views: map[int]*appView{}, out: make(chan any, 4096), next: start}
}

// run keeps a connection to the backend while the App runs (the backend restarts on updates).
func (b *appBrowser) run() {
	for !b.s.quitting.Load() {
		if err := b.serve(); err != nil && !b.s.quitting.Load() {
			log.Printf("app browser: %v", err)
		}
		time.Sleep(2 * time.Second)
	}
}

func (b *appBrowser) serve() error {
	entry := b.s.backendEntry
	u, err := url.Parse(entry)
	if err != nil || u.Host == "" {
		return errors.New("no backend yet")
	}
	token := u.Query().Get("token")
	ws, _, err := websocket.DefaultDialer.Dial("ws://"+u.Host+"/api/browser/ext", http.Header{"Authorization": {"Bearer " + token}})
	if err != nil {
		return err
	}
	ws.SetReadLimit(64 << 20)
	b.mu.Lock()
	b.ws = ws
	b.mu.Unlock()
	log.Printf("app browser: connected to the backend")
	done := make(chan struct{})
	go func() {
		for {
			select {
			case m := <-b.out:
				_ = ws.SetWriteDeadline(time.Now().Add(30 * time.Second))
				if err := ws.WriteJSON(m); err != nil {
					ws.Close()
					return
				}
			case <-done:
				return
			}
		}
	}()
	defer func() {
		close(done)
		ws.Close()
		// The backend went away: its tabs are gone with it (it forgets them), so are the pages
		b.mu.Lock()
		views := b.views
		b.views = map[int]*appView{}
		b.ws = nil
		b.mu.Unlock()
		for _, v := range views {
			v.v.Close()
		}
	}()
	for {
		var m struct {
			ID   *int64          `json:"id"`
			Op   string          `json:"op"`
			Args json.RawMessage `json:"args"`
		}
		if err := ws.ReadJSON(&m); err != nil {
			return err
		}
		if m.ID == nil {
			continue
		}
		go func(id int64, op string, args json.RawMessage) {
			res, err := b.op(op, args)
			reply := map[string]any{"id": id, "ok": err == nil}
			if err != nil {
				reply["error"] = err.Error()
			} else if res != nil {
				reply["result"] = res
			}
			b.send(reply)
		}(*m.ID, m.Op, m.Args)
	}
}

func (b *appBrowser) send(m any) {
	select {
	case b.out <- m:
	default:
		// the backend isn't reading: drop rather than hold up the browser
	}
}

func (b *appBrowser) op(op string, raw json.RawMessage) (any, error) {
	var a struct {
		Tab     int             `json:"tab"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
		Profile string          `json:"profile"`
		URL     string          `json:"url"`
	}
	_ = json.Unmarshal(raw, &a)
	switch op {
	case "hello":
		// engine: the browser program this App runs, so the backend's headless browser (while the App is closed) is the
		// same program on the same profiles (internal/projbrowser findBrowser)
		engine, dataDir := "", ""
		if b.s.app != nil {
			engine, dataDir = b.s.app.EnginePath(), b.s.app.BrowserDataDir()
		}
		return map[string]any{"app": true, "id": "gerdr-app", "version": "app", "name": "Gerdr App", "engine": engine, "dataDir": dataDir}, nil
	case "open":
		tab, err := b.open(a.Profile, a.URL, 0)
		if err != nil {
			return nil, err
		}
		return map[string]any{"tab": tab, "window": 0}, nil
	case "cdp":
		v := b.view(a.Tab)
		if v == nil {
			return nil, errors.New("no such page")
		}
		var params any
		if len(a.Params) > 0 && string(a.Params) != "null" {
			params = a.Params
		}
		res, err := v.v.Call(a.Method, params)
		if err != nil {
			return nil, err
		}
		return res, nil
	case "close", "release":
		if v := b.view(a.Tab); v != nil {
			v.v.Close()
		}
		return map[string]any{}, nil
	case "focus":
		return map[string]any{}, nil
	}
	return nil, errors.New("not in the App's browser: " + op)
}

// open a page (a view) in the profile; opener: the tab whose page opened it (0 = none).
func (b *appBrowser) open(profile, raw string, opener int) (int, error) {
	b.mu.Lock()
	app := b.s.app
	b.next++
	tab := appTabBase + b.next
	b.mu.Unlock()
	if app == nil {
		return 0, errors.New("the App isn't ready")
	}
	av := &appView{profile: profile, place: make(chan func(), 1)}
	v, err := app.OpenView(lightron.ViewOptions{
		Profile: profile,
		URL:     raw,
		OnEvent: func(method string, params json.RawMessage) {
			b.send(map[string]any{"event": "cdp", "tab": tab, "method": method, "params": params})
		},
		OnPopup: func(u string) {
			child, err := b.open(profile, u, tab)
			if err != nil {
				log.Printf("app browser: popup: %v", err)
				return
			}
			b.send(map[string]any{"event": "popup", "tab": child, "opener": tab, "url": u})
		},
		OnClose: func() {
			b.mu.Lock()
			delete(b.views, tab)
			b.mu.Unlock()
			b.send(map[string]any{"event": "closed", "tab": tab})
		},
	})
	if err != nil {
		return 0, err
	}
	av.v = v
	b.mu.Lock()
	b.views[tab] = av
	b.mu.Unlock()
	go func() {
		for f := range av.place {
			f()
		}
	}()
	return tab, nil
}

func (b *appBrowser) view(tab int) *appView {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.views[tab]
}

// placeView: the UI's Browser pane says where its box is ({type: "browserView", tab, rect: [x, y, w, h], dpr}) or
// that it's gone ({…, hide: true}). Only the latest counts: a split being dragged sends many.
func (b *appBrowser) placeView(w *lightron.Window, m map[string]any) {
	n, _ := m["tab"].(float64)
	v := b.view(int(n))
	if v == nil {
		log.Printf("app browser: the pane shows tab %d, which isn't one of the App's pages", int(n))
		return
	}
	var f func()
	if m["hide"] == true {
		f = func() {
			_ = v.v.Hide()
			if v.placed {
				v.placed = false
				log.Printf("app browser: tab %d hidden", int(n))
				b.send(map[string]any{"event": "shown", "tab": int(n), "reason": "off"})
			}
		}
	} else {
		r, _ := m["rect"].([]any)
		dpr, _ := m["dpr"].(float64)
		if len(r) != 4 {
			return
		}
		x, _ := r[0].(float64)
		y, _ := r[1].(float64)
		width, _ := r[2].(float64)
		height, _ := r[3].(float64)
		// holes: the UI's own menus / dialogs over the box ([x, y, w, h, radius] from its top-left): cut out of the
		// page, which stays live around them (Lightron's View.Mask)
		var holes [][5]float64
		if hs, ok := m["holes"].([]any); ok {
			for _, h := range hs {
				a, _ := h.([]any)
				var r [5]float64
				for i := 0; i < len(a) && i < 5; i++ {
					r[i], _ = a[i].(float64)
				}
				holes = append(holes, r)
			}
		}
		f = func() {
			if err := v.v.Mask(holes, dpr); err != nil {
				log.Printf("app browser: mask: %v", err)
			}
			if err := v.v.Place(w, x, y, width, height, dpr); err != nil {
				log.Printf("app browser: place: %v", err)
			} else if !v.placed {
				v.placed = true
				log.Printf("app browser: tab %d shown in the pane", int(n))
				// the desktop wins: a phone's layout of it ends (internal/chromeext/phone.go)
				b.send(map[string]any{"event": "shown", "tab": int(n), "reason": "on"})
			}
		}
	}
	// replace what's waiting: only the newest placement matters
	select {
	case <-v.place:
	default:
	}
	select {
	case v.place <- f:
	default:
	}
}
