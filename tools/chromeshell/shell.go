package main

import (
	"encoding/json"
	"fmt"
	"github.com/gerdr-dev/gerdr/internal/selfupdate"
	"log"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// shell: what desktop/main.js does, over CDP. Every page of our origin gets the bridge; messages from it are
// handled here. Windows are --app windows of the engine, the browser copy that is Gerdr to macOS (engine_darwin.go).
type shell struct {
	c            *cdp
	entry        string // the pages' login link (http://gerdr/auth?token=…, main.go pageHost), or the test page
	origin       string // the pages' origin (http://gerdr)
	backendEntry string // the backend's own login link (http://127.0.0.1:7870/auth?token=…): for the shell's own requests

	engine   string // the browser executable, to open windows (launch)
	profile  string
	engineID string // its bundle id, to bring the app to the front
	menu     *menu  // Gerdr's menu bar in the engine (menu.go); nil = Chrome's

	// The backend and updates (app.go, updater.go)
	up        *selfupdate.Updater
	bin       string // the backend to run when the updater has no say (-gerdr, no bundled one)
	beMu      sync.Mutex
	be        *backend
	quitting  atomic.Bool
	upMu      sync.Mutex
	lastBusy  *selfupdate.Busy // the running gerdr's /api/busy, from the last poll
	applying  bool
	upgrading upgradeMark
	idleOK    string // "update when idle" was picked for this version (automatic updates off)

	mu       sync.Mutex
	wins     map[string]*win // CDP session → window
	armed    bool            // set once the first window is up: from then on, no window left = quit
	launched time.Time       // last launch: its window may not have attached yet, so "no windows" isn't final for a bit
	notes    map[string]time.Time
	newTabAt time.Time // the last new-tab window turned into a Gerdr one

	allClosed     chan struct{}
	closeOnce     sync.Once
	quitForUpdate chan struct{} // a whole-App update is staged: quit so it can be swapped in (app.go installApp)
}

type win struct {
	session, target string
	space           string // the session (layout.ts Space) its page says it shows
}

func newShell(entry string) *shell {
	o := originOf(entry)
	return &shell{entry: entry, origin: o, wins: map[string]*win{}, notes: map[string]time.Time{}, allClosed: make(chan struct{}), quitForUpdate: make(chan struct{}, 1)}
}

func (s *shell) event(m *msg) {
	switch m.Method {
	case "Target.attachedToTarget":
		var p struct {
			SessionID  string `json:"sessionId"`
			TargetInfo struct {
				TargetID string `json:"targetId"`
				Type     string `json:"type"`
				URL      string `json:"url"`
				OpenerID string `json:"openerId"`
			} `json:"targetInfo"`
			Waiting bool `json:"waitingForDebugger"`
		}
		_ = json.Unmarshal(m.Params, &p)
		t := p.TargetInfo
		go s.attach(p.SessionID, t.TargetID, t.Type, t.URL, t.OpenerID, p.Waiting)
	case "Target.detachedFromTarget":
		var p struct {
			SessionID string `json:"sessionId"`
		}
		_ = json.Unmarshal(m.Params, &p)
		s.mu.Lock()
		delete(s.wins, p.SessionID)
		s.mu.Unlock()
		s.checkClosed()
	case "Runtime.bindingCalled":
		var p struct{ Payload string }
		_ = json.Unmarshal(m.Params, &p)
		go s.message(m.SessionID, p.Payload)
	case "Network.requestWillBeSent":
		if os.Getenv("CHROMESHELL_NAV") != "" && strings.Contains(string(m.Params), `"type":"Document"`) {
			var p struct {
				Request   struct{ URL string } `json:"request"`
				Initiator json.RawMessage      `json:"initiator"`
			}
			_ = json.Unmarshal(m.Params, &p)
			log.Printf("document %s initiator %s", noQuery(p.Request.URL), tail(string(p.Initiator), 600))
		}
	case "Page.frameNavigated", "Page.frameRequestedNavigation":
		if os.Getenv("CHROMESHELL_NAV") != "" {
			log.Printf("nav %s %s", m.Method, tail(string(m.Params), 300))
		}
	case "Inspector.targetCrashed":
		// The page's process died (crash, or macOS reclaimed it): load it again, the sessions live in the backend.
		log.Printf("page crashed: reloading")
		go func(session string) {
			time.Sleep(500 * time.Millisecond)
			_, _ = s.c.call(session, "Page.reload", nil)
		}(m.SessionID)
	}
}

// launchGrace: how long after launch() its window may take to attach.
const launchGrace = 5 * time.Second

// checkClosed: no window left (the person closed the last one, ⌘Q) → the shell quits. Not while a window just
// launched hasn't shown up yet.
func (s *shell) checkClosed() {
	s.mu.Lock()
	none, armed, wait := len(s.wins) == 0, s.armed, launchGrace-time.Since(s.launched)
	s.mu.Unlock()
	if !none || !armed {
		return
	}
	if wait > 0 {
		time.AfterFunc(wait, s.checkClosed)
		return
	}
	s.closeOnce.Do(func() { close(s.allClosed) })
}

func (s *shell) attach(session, target, typ, u, opener string, waiting bool) {
	run := func() {
		if waiting {
			_, _ = s.c.call(session, "Runtime.runIfWaitingForDebugger", nil)
		}
	}
	if typ != "page" {
		run()
		_, _ = s.c.call("", "Target.detachFromTarget", map[string]any{"sessionId": session})
		return
	}
	if opener == "" && newTabPage(u) {
		// The browser opening its new-tab page in a tabbed window: a click on the Dock icon (the app "reopened"),
		// or ⌘N / ⌘T from Chrome's own menu when ours isn't there. Not ours to show: bring up a Gerdr window instead,
		// the one there is (a Dock click opened a second window before), or a new one if none.
		run()
		log.Printf("the browser opened its new-tab page: closing it, a Gerdr window instead")
		_, _ = s.c.call("", "Target.closeTarget", map[string]any{"targetId": target})
		s.mu.Lock()
		again := time.Since(s.newTabAt) < 2*time.Second
		s.newTabAt = time.Now()
		s.mu.Unlock()
		if !again { // one at a time: should something answer our focus with another new-tab window, don't loop
			s.focusAny()
		}
		return
	}
	if strings.Contains(u, storagePage) { // migrateStorage's page: its own business
		run()
		return
	}
	if opener != "" && !s.ours(u) {
		run()
		go s.popup(target)
		return
	}
	for _, step := range []struct {
		method string
		params any
	}{
		// Without Runtime.enable first, a binding added while the target waits for us is gone after its next
		// navigation (seen on Chrome 156: addBinding succeeds, window.__gerdrPost is undefined after the reload).
		{"Runtime.enable", nil},
		{"Runtime.addBinding", map[string]any{"name": "__gerdrPost"}},
		{"Page.enable", nil},
		{"Inspector.enable", nil},
		{"Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": seedJS + bridgeJS}},
	} {
		if _, err := s.c.call(session, step.method, step.params); err != nil {
			log.Printf("attach %s: %v", step.method, err)
		}
	}
	log.Printf("window attached %s %s", target[:8], noQuery(u))
	s.mu.Lock()
	s.wins[session] = &win{session: session, target: target}
	s.mu.Unlock()
	if os.Getenv("CHROMESHELL_NAV") != "" {
		_, _ = s.c.call(session, "Network.enable", nil) // who starts each document load (initiator stack)
	}
	run()
	go s.ensureBridge(session)
}

// ensureBridge: a window's page must start with the bridge in place, and attaching early isn't enough: the login
// link's redirect is decided in the browser process, so its page can commit while attach is still adding the script
// (and with PWA windows, PWA.launch committed the document before we could attach at all). Whichever happened: once
// the page has loaded on our origin without the bridge, reload it, once. Only after a launch: earlier pages aren't ours
// to reload (PWA.install's hidden page was, and reloading it failed the install).
func (s *shell) ensureBridge(session string) {
	s.mu.Lock()
	launching := !s.launched.IsZero()
	s.mu.Unlock()
	if !launching {
		return
	}
	for i := 0; i < 100; i++ {
		v, err := s.c.eval(session, `[document.readyState, location.pathname, !!window.gerdrNative].join("|")`)
		if err != nil {
			return // gone
		}
		var st string
		_ = json.Unmarshal(v, &st)
		parts := strings.Split(st, "|")
		if len(parts) == 3 && parts[2] == "true" {
			return
		}
		if len(parts) == 3 && parts[0] != "loading" && parts[1] != "/auth" && parts[1] != "blank" && parts[1] != "" {
			log.Printf("page loaded without the bridge (%s): reloading", parts[1])
			_, _ = s.c.call(session, "Page.reload", nil)
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func newTabPage(u string) bool {
	return strings.HasPrefix(u, "chrome://newtab") || strings.HasPrefix(u, "chrome://new-tab-page") || strings.HasPrefix(u, "chrome-search://")
}

func (s *shell) ours(u string) bool {
	return u == "" || u == "about:blank" || strings.HasPrefix(u, s.origin+"/")
}

// popup: a window the page opened past the bridge's window.open (e.g. noopener): once it knows where it goes, an
// outside address goes to the default browser instead.
func (s *shell) popup(target string) {
	for i := 0; i < 30; i++ {
		r, err := s.c.call("", "Target.getTargetInfo", map[string]any{"targetId": target})
		if err != nil {
			return
		}
		var ti struct {
			TargetInfo struct{ URL string } `json:"targetInfo"`
		}
		_ = json.Unmarshal(r, &ti)
		if u := ti.TargetInfo.URL; u != "" && u != "about:blank" {
			if !s.ours(u) {
				_, _ = s.c.call("", "Target.closeTarget", map[string]any{"targetId": target})
				openExternal(u)
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (s *shell) message(session, payload string) {
	var m map[string]any
	if json.Unmarshal([]byte(payload), &m) != nil {
		return
	}
	str := func(k string) string { v, _ := m[k].(string); return v }
	typ := str("type")
	// The type only: payloads carry notification texts (what agents said). CHROMESHELL_NAV=1 for all of it.
	if os.Getenv("CHROMESHELL_NAV") != "" {
		log.Printf("page → %s", tail(payload, 200))
	} else if typ != "menuKeys" && typ != "locale" && typ != "badge" && typ != "__filePath" {
		log.Printf("page → %s", typ)
	}
	switch typ {
	case "__filePath":
		p := s.filePath(session, m["id"])
		b, _ := json.Marshal(p)
		_, _ = s.c.eval(session, fmt.Sprintf("window.__gerdrResolve(%v, %s)", m["id"], b))
	case "notify":
		s.notify(session, str("title"), str("body"), str("sessionId"), str("space"))
	case "__noteClick":
		s.noteClick(session, str("sessionId"), str("space"))
	case "badge":
		// The engine is the app: its Dock tile is Gerdr's (navigator.setAppBadge is for installed web apps only).
		n, _ := m["count"].(float64)
		label := ""
		if n > 0 {
			label = fmt.Sprint(int(n))
		}
		if _, err := s.c.call("", "Browser.setDockTile", map[string]any{"badgeLabel": label}); err != nil {
			log.Printf("badge: %v", err)
		}
	case "space":
		s.mu.Lock()
		if w := s.wins[session]; w != nil {
			w.space = str("id")
		}
		s.mu.Unlock()
	case "newWindow":
		s.launch(str("space"))
	case "open":
		openExternal(str("url"))
	case "openPath":
		openPath(str("path"), str("cwd"), m["reveal"] == true)
	case "webPid":
		_, _ = s.c.eval(session, fmt.Sprintf("window.__GERDR_WEB_PID__ = %d", s.rendererPid(session)))
	case "update":
		s.updateMessage(m)
	case "menuKeys":
		// The menu shows the keys the UI's settings give its commands (null = no key).
		if s.menu != nil {
			keys := map[string]*string{}
			if km, ok := m["keys"].(map[string]any); ok {
				for k, v := range km {
					if str, ok := v.(string); ok {
						keys[k] = &str
					} else {
						keys[k] = nil
					}
				}
			}
			s.menu.update(keys, "")
		}
	case "locale":
		// The menu follows the page's language (its own per-device setting).
		if s.menu != nil {
			s.menu.update(nil, str("locale"))
		}
	}
}

// notify shows a web notification from the page that asked. With AppShimNotificationAttribution it comes from the
// app (its name and icon), not from Chrome; the click comes back through the bridge.
func (s *shell) notify(session, title, body, sessionID, space string) {
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
		const n = new Notification(t, { body: b });
		const live = (window.__gerdrNotes ||= new Set()); // a collected Notification loses its click handler
		live.add(n);
		if (live.size > 50) live.delete(live.values().next().value);
		n.onclick = () => { live.delete(n); window.__gerdrPost(JSON.stringify({ type: "__noteClick", sessionId: sid, space: sp })); };
		n.onclose = () => live.delete(n);
	})(` + string(args) + `)`
	if _, err := s.c.eval(session, js); err != nil {
		log.Printf("notify: %v", err)
	}
}

func (s *shell) noteClick(session, sessionID, space string) {
	// The window showing the conversation's session if there is one, else the one that notified.
	s.mu.Lock()
	target := s.wins[session]
	for _, w := range s.wins {
		if space != "" && w.space == space {
			target = w
		}
	}
	s.mu.Unlock()
	if target == nil {
		return
	}
	s.focus(target)
	if sessionID != "" {
		b, _ := json.Marshal(sessionID)
		_, _ = s.c.eval(target.session, "window.gerdr && window.gerdr.focusSession("+string(b)+")")
	}
}

func (s *shell) focus(w *win) {
	log.Printf("focus window %s", w.target[:8])
	if _, err := s.c.call("", "Target.activateTarget", map[string]any{"targetId": w.target}); err != nil {
		log.Printf("focus: %v", err)
	}
	// activateTarget raises the window within the app; the app itself comes forward from inside (menu.go activate).
	if s.menu != nil && !dryOpen {
		s.menu.activate()
	}
}

// focusAny: Gerdr.app opened again (second instance) → bring up a window, or open one if there is none.
func (s *shell) focusAny() {
	s.mu.Lock()
	var w *win
	for _, x := range s.wins {
		w = x
		break
	}
	s.mu.Unlock()
	if w != nil {
		s.focus(w)
	} else {
		s.launch("")
	}
}

// launch opens a window: the engine started again with --app hands it to the running browser (its process
// singleton, per profile), which opens an app window: no tabs, no toolbar, a bare title bar. CDP has no way to make
// one (Target.createTarget opens a tabbed browser window).
func (s *shell) launch(space string) {
	s.mu.Lock()
	s.launched = time.Now()
	s.mu.Unlock()
	// --test-type here too: the bars Chrome adds to a new window (main.go launchBrowser) are decided on the command
	// line that asked for the window, this one, not the browser's own.
	args := []string{"--user-data-dir=" + s.profile, "--app=" + pageURL(s.entry, space)}
	if os.Getenv("CHROMESHELL_NO_TEST_TYPE") == "" {
		args = append(args, "--test-type")
	}
	cmd := exec.Command(s.engine, args...)
	if err := cmd.Start(); err != nil {
		log.Printf("launch: %v", err)
		return
	}
	go func() { _ = cmd.Wait() }()
	s.mu.Lock()
	s.armed = true
	s.mu.Unlock()
}

// filePath: the page parked the File in window.__gerdrFiles; DOM.getFileInfo turns its remote object into a path.
func (s *shell) filePath(session string, id any) string {
	r, err := s.c.call(session, "Runtime.evaluate", map[string]any{"expression": fmt.Sprintf("window.__gerdrFiles.get(%v)", id)})
	if err != nil {
		return ""
	}
	var ev struct {
		Result struct {
			ObjectID string `json:"objectId"`
		} `json:"result"`
	}
	_ = json.Unmarshal(r, &ev)
	if ev.Result.ObjectID == "" {
		return ""
	}
	r, err = s.c.call(session, "DOM.getFileInfo", map[string]any{"objectId": ev.Result.ObjectID})
	if err != nil {
		return ""
	}
	var fi struct{ Path string }
	_ = json.Unmarshal(r, &fi)
	return fi.Path
}

// rendererPid: the process of the page in this session (Settings → Resources measures its memory). CDP has no
// target → process mapping, and a browser holds more renderers than windows (a spare one Chrome keeps warm, the
// hidden install page's): taking the first renderer reported a 73MB idle one next to the UI's 430MB. So ask the page
// to keep its thread busy for a moment and see whose CPU time went up.
func (s *shell) rendererPid(session string) int {
	renderers := func() map[int]float64 {
		r, err := s.c.call("", "SystemInfo.getProcessInfo", nil)
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
	_, _ = s.c.eval(session, `(() => { const t = performance.now(); while (performance.now() - t < 150); })()`)
	after := renderers()
	best, most := 0, 0.0
	for id, t := range after {
		if d := t - before[id]; d > most {
			best, most = id, d
		}
	}
	return best
}

// dryOpen: the self-test checks that a link would leave for the default browser without opening one.
var dryOpen bool

func openExternal(u string) {
	if pu, err := url.Parse(u); err == nil && (pu.Scheme == "http" || pu.Scheme == "https" || pu.Scheme == "mailto") {
		if dryOpen {
			log.Printf("would open in the default browser: %s", u)
			return
		}
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

// noQuery: an address fit for the log. The login link carries the backend's token in its query.
func noQuery(u string) string {
	if i := strings.IndexByte(u, '?'); i >= 0 {
		return u[:i] + "?…"
	}
	return u
}
