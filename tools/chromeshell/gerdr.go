package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zbysir/lightron"

	"github.com/gerdr-dev/gerdr/internal/selfupdate"
)

// shell: what desktop/main.js did for the UI, on Lightron: the messages of window.gerdrNative (bridge.js), the
// backend and its updates (app.go, backend.go), the menus (menu.go).
type shell struct {
	app *lightron.App

	// The backend and updates (app.go)
	up           *selfupdate.Updater
	bin          string // the backend to run when the updater has no say (-gerdr, no bundled one)
	backendEntry string // the backend's own login link (http://127.0.0.1:7870/auth?token=…): the shell's requests
	beMu         sync.Mutex
	be           *backend
	quitting     atomic.Bool
	upMu         sync.Mutex
	lastBusy     *selfupdate.Busy // the running gerdr's /api/busy, from the last poll
	applying     bool
	upgrading    upgradeMark
	idleOK       string // "update when idle" was picked for this version (automatic updates off)

	mu     sync.Mutex
	spaces map[string]string // window id → the session (layout.ts Space) its page says it shows
	notes  map[string]time.Time
	keys   map[string]*string // menu.go: the UI's command → accelerator; nil value = no key
	lang   string             // menu.go: the page's language; "" = the system's
}

func (s *shell) setApp(a *lightron.App) {
	s.mu.Lock()
	s.app = a
	s.mu.Unlock()
}

// startBackend: Options.URLFunc. The backend's login link, as the page's first address.
func (s *shell) startBackend() (string, error) {
	log.Printf("starting backend %s", s.bin)
	be, err := startBackend(s.bin, nil)
	if err != nil {
		return "", err
	}
	entry, err := be.waitReady(30 * time.Second)
	if err != nil {
		return "", err
	}
	s.backendEntry = entry
	s.setBackend(be)
	return windowLink(entry), nil
}

// windowLink: the link the windows open at, with a one-time code instead of the token (/auth?code=…). It goes on the
// command line that opens a window (Lightron's --app), which any process on the Mac can read: a code is spent by the
// time anyone could use it, the token would be a key to the backend for good. Later windows open at the same link and
// get in with the cookie the first one got (internal/server/logincode.go). A gerdr too old to give codes (the
// background service not updated yet): the token, as before.
func windowLink(entry string) string {
	u, err := url.Parse(entry)
	if err != nil {
		return entry
	}
	req, _ := http.NewRequest("POST", originOf(entry)+"/api/auth/code", nil)
	req.Header.Set("Authorization", "Bearer "+u.Query().Get("token"))
	req.Header.Set("X-Gerdr", "1")
	c := http.Client{Timeout: 5 * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		log.Printf("login code: %v; the window opens with the token", err)
		return entry
	}
	defer resp.Body.Close()
	var out struct{ Code string }
	if resp.StatusCode != 200 || json.NewDecoder(resp.Body).Decode(&out) != nil || out.Code == "" {
		log.Printf("login code: %s; the window opens with the token", resp.Status)
		return entry
	}
	q := u.Query()
	q.Del("token")
	q.Set("code", out.Code)
	u.RawQuery = q.Encode()
	return u.String()
}

// pageEntry: the backend's login link at the pages' host (http://gerdr/auth?token=…), going to page.
func (s *shell) pageEntry(page string) string {
	u, err := url.Parse(s.backendEntry)
	if err != nil {
		return s.backendEntry
	}
	u.Host = "gerdr"
	q := u.Query()
	q.Set("next", page)
	u.RawQuery = q.Encode()
	return u.String()
}

// spacePath: the page of one project ("" = the last one).
func spacePath(space string) string {
	if space == "" {
		return "/"
	}
	return "/" + url.PathEscape(space)
}

// message: what the page posts (window.gerdrNative.postMessage), the set desktop/main.js handled.
func (s *shell) message(w *lightron.Window, raw json.RawMessage) {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return
	}
	str := func(k string) string { v, _ := m[k].(string); return v }
	typ := str("type")
	s.mu.Lock()
	ready := s.app != nil
	s.mu.Unlock()
	if !ready { // before OnReady: nothing the page asks for can be done yet
		return
	}
	// The type only: payloads carry notification texts (what agents said).
	if typ != "menuKeys" && typ != "locale" && typ != "badge" {
		log.Printf("page → %s", typ)
	}
	switch typ {
	case "notify":
		s.notify(w, str("title"), str("body"), str("sessionId"), str("space"))
	case "__noteClick":
		s.noteClick(w, str("sessionId"), str("space"))
	case "badge":
		// The engine is the app: its Dock tile is Gerdr's (navigator.setAppBadge is for installed web apps only).
		n, _ := m["count"].(float64)
		label := ""
		if n > 0 {
			label = fmt.Sprint(int(n))
		}
		s.app.SetBadge(label)
	case "space":
		s.mu.Lock()
		s.spaces[w.ID()] = str("id")
		s.mu.Unlock()
	case "newWindow":
		s.newWindow(str("space"))
	case "open":
		openExternal(str("url"))
	case "openPath":
		openPath(str("path"), str("cwd"), m["reveal"] == true)
	case "webPid":
		_ = w.Eval(fmt.Sprintf("window.__GERDR_WEB_PID__ = %d", rendererPid(s.app, w)))
	case "update":
		s.updateMessage(m)
	case "menuKeys":
		// The menu shows the keys the UI's settings give its commands (null = no key).
		keys := map[string]*string{}
		if km, ok := m["keys"].(map[string]any); ok {
			for k, v := range km {
				if acc, ok := v.(string); ok {
					keys[k] = &acc
				} else {
					keys[k] = nil
				}
			}
		}
		s.mu.Lock()
		s.keys = keys
		s.mu.Unlock()
		s.refreshMenu()
	case "locale":
		// The menu follows the page's language (its own per-device setting).
		s.mu.Lock()
		s.lang = str("locale")
		s.mu.Unlock()
		s.refreshMenu()
	}
}

func (s *shell) newWindow(space string) {
	if err := s.app.NewWindow(spacePath(space)); err != nil {
		log.Printf("new window: %v", err)
	}
}

// space: the session a window shows ("" = unknown).
func (s *shell) space(w *lightron.Window) string {
	if w == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.spaces[w.ID()]
}

// notify shows a web notification from the page that asked: it comes from the app (the engine is Gerdr to macOS), its
// name and icon; the click comes back through the bridge.
func (s *shell) notify(w *lightron.Window, title, body, sessionID, space string) {
	key := title + "\n" + body + "\n" + sessionID
	s.mu.Lock()
	for k, t := range s.notes {
		if time.Since(t) > 5*time.Second {
			delete(s.notes, k)
		}
	}
	_, seen := s.notes[key] // every window's page notifies on its own: show it once
	s.notes[key] = time.Now()
	s.mu.Unlock()
	if seen {
		return
	}
	if title == "" {
		title = "Gerdr"
	}
	args, _ := json.Marshal([]string{title, body, sessionID, space})
	js := `(([t, b, sid, sp]) => {
		const show = () => {
			const n = new Notification(t, { body: b });
			const live = (window.__gerdrNotes ||= new Set()); // a collected Notification loses its click handler
			live.add(n);
			if (live.size > 50) live.delete(live.values().next().value);
			n.onclick = () => { live.delete(n); lightron.send({ type: "__noteClick", sessionId: sid, space: sp }); };
			n.onclose = () => live.delete(n);
		};
		if (Notification.permission === "granted") show();
		else if (Notification.permission !== "denied") Notification.requestPermission().then((p) => p === "granted" && show());
	})(` + string(args) + `)`
	if err := w.Eval(js); err != nil {
		log.Printf("notify: %v", err)
	}
}

// noteClick: the window showing the notification's session if there is one, else the one that notified; up front,
// on the conversation.
func (s *shell) noteClick(from *lightron.Window, sessionID, space string) {
	target := from
	if space != "" {
		for _, w := range s.app.Windows() {
			if s.space(w) == space {
				target = w
			}
		}
	}
	target.Focus()
	if sessionID != "" {
		b, _ := json.Marshal(sessionID)
		_ = target.Eval("window.gerdr && window.gerdr.focusSession(" + string(b) + ")")
	}
}

// eval: a value from a window's page.
func eval(w *lightron.Window, js string) (json.RawMessage, error) {
	r, err := w.Call("Runtime.evaluate", map[string]any{"expression": js, "returnByValue": true, "awaitPromise": true})
	if err != nil {
		return nil, err
	}
	var ev struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text string `json:"text"`
		} `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(r, &ev); err != nil {
		return nil, err
	}
	if ev.ExceptionDetails != nil {
		return nil, fmt.Errorf("%s", ev.ExceptionDetails.Text)
	}
	return ev.Result.Value, nil
}

// rendererPid: the process of the page in this window (Settings → Resources measures its memory). The DevTools
// protocol has no target → process mapping, and a browser holds more renderers than windows (a spare one Chrome keeps
// warm): taking the first renderer reported a 73MB idle one next to the UI's 430MB. So ask the page to keep its thread
// busy for a moment and see whose CPU time went up.
func rendererPid(a *lightron.App, w *lightron.Window) int {
	renderers := func() map[int]float64 {
		r, err := a.Call("SystemInfo.getProcessInfo", nil)
		if err != nil {
			return nil
		}
		var pi struct {
			ProcessInfo []struct {
				Type    string  `json:"type"`
				ID      int     `json:"id"`
				CPUTime float64 `json:"cpuTime"`
			} `json:"processInfo"`
		}
		_ = json.Unmarshal(r, &pi)
		m := map[int]float64{}
		for _, p := range pi.ProcessInfo {
			if p.Type == "renderer" {
				m[p.ID] = p.CPUTime
			}
		}
		return m
	}
	before := renderers()
	if len(before) == 1 {
		for id := range before {
			return id
		}
	}
	_, _ = eval(w, `(() => { const t = performance.now(); while (performance.now() - t < 150); })()`)
	best, most := 0, 0.0
	for id, t := range renderers() {
		if d := t - before[id]; d > most {
			best, most = id, d
		}
	}
	return best
}

func openExternal(u string) {
	if pu, err := url.Parse(u); err == nil && (pu.Scheme == "http" || pu.Scheme == "https" || pu.Scheme == "mailto") {
		_ = exec.Command("open", u).Start()
	}
}

// openPath: the open / reveal buttons under a picture or video in the chat. Relative paths are the session's.
func openPath(p, cwd string, reveal bool) {
	home, _ := os.UserHomeDir()
	if strings.HasPrefix(p, "~/") {
		p = filepath.Join(home, p[2:])
	}
	if !filepath.IsAbs(p) {
		if cwd == "" {
			cwd = home
		}
		p = filepath.Join(cwd, p)
	}
	if _, err := os.Stat(p); err != nil {
		return
	}
	if reveal {
		_ = exec.Command("open", "-R", p).Start()
	} else {
		_ = exec.Command("open", p).Start()
	}
}

func originOf(u string) string {
	pu, err := url.Parse(u)
	if err != nil {
		return u
	}
	return pu.Scheme + "://" + pu.Host
}
