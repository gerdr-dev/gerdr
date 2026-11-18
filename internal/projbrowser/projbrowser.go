// Package projbrowser is each project's own browser: the Chrome (or Edge, Brave, Chromium) installed here, headless,
// with a profile of its own under the project's folder (<data dir>/projects/<project>/browser), so a site signed in
// to once stays signed in for that project and no other. Its pages join chromeext's tab list (Bridge.Adopt): every
// `gerdr browser` action works on them, and the Browser pane shows them through the remote screen's stream
// (chromeext.View, server/screenstream.go). Design and what was ruled out: docs/dev/BROWSER-PANE.md.
package projbrowser

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gerdr-dev/gerdr/internal/chromeext"
	"github.com/gerdr-dev/gerdr/internal/i18n"
)

// noBoundSessions: Chrome's device-bound sessions (DBSC) tie a Google sign-in to a key the signed browser program keeps
// in the system keychain. The project's profile is used by two programs in turn (this headless Chrome, and the App's
// re-signed copy while the App runs: chromeext/app.go), and each hand-over broke the binding: Google asked to sign in
// again, with two-step verification, every time (seen 2026-11-18). Off in both, so the sign-in is plain cookies, which
// both read. The App (desktop/main.js) passes the same switch.
const noBoundSessions = "--disable-features=EnableBoundSessionCredentials,DeviceBoundSessions"

// idleClose: a browser with no page open for this long exits (its profile stays).
const idleClose = 10 * time.Minute

// Manager runs one browser per project, started on the first page and stopped when idle.
type Manager struct {
	Dir string // Gerdr's data dir
	// BackendLink: this backend's login link (http://127.0.0.1:7870/auth?token=…), for the browser-only Electron to
	// connect back with (electron.go); set once it listens
	BackendLink string
	Bridge      *chromeext.Bridge
	Log         *slog.Logger

	mu      sync.Mutex
	inst    map[string]*instance
	started time.Time
}

func New(dir string, b *chromeext.Bridge, log *slog.Logger) *Manager {
	if log == nil {
		log = slog.Default()
	}
	m := &Manager{Dir: dir, Bridge: b, Log: log, inst: map[string]*instance{}, started: time.Now()}
	b.OnApp = m.appChanged
	return m
}

// appChanged: the desktop App's browser connected. Pages open in headless browsers move into it, at the address
// they're at, each replacing its old tab (a pane showing one follows: Tab.Replaces), so they show as real pages
// right away instead of on the next open. The headless browser stops first: the profile is one browser's at a time.
func (m *Manager) appChanged(connected bool) {
	if !connected {
		return
	}
	// Remember the App's browser (program, data directory): the headless browser runs the same (location.go)
	exe, dataDir := m.Bridge.AppEngine()
	kind, args := m.Bridge.AppKind()
	m.saveAppInfo(exe, dataDir, kind, args)
	if m.Bridge.AppBrowserOnly() {
		return // the projects' browser this package started (electron.go): nothing to move
	}
	// The App came while the browser-only one had pages: they open again in the App, in their tabs' places
	for _, o := range m.Bridge.TakeOrphans() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		if _, err := m.Bridge.OpenNative(ctx, o.Project, profileName(o.Project), o.URL, 0, 0, o.ID); err != nil {
			m.Log.Warn("project browser: move a page into the App", "url", o.URL, "err", err)
		}
		cancel()
	}
	m.mu.Lock()
	running := make([]*instance, 0, len(m.inst))
	for _, in := range m.inst {
		running = append(running, in)
	}
	m.mu.Unlock()
	for _, in := range running {
		pages := m.Bridge.ProjectTabs(in.project)
		m.stopKey(in.key)
		loc := m.location(in.project)
		m.adopt(in.project, loc)
		for _, p := range pages {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			if _, err := m.Bridge.OpenNative(ctx, in.project, loc.profile, p.URL, 0, 0, p.ID); err != nil {
				m.Log.Warn("project browser: move a page into the App", "url", p.URL, "err", err)
			}
			cancel()
		}
	}
}

// Release stops the headless browser on the App's data directory: the App is starting its own on it
// (desktop/core asks at start; the directory is one process's at a time).
func (m *Manager) Release() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	m.Bridge.QuitApp(ctx)
	cancel()
	m.stopKey(appKey)
}

// ValidProject: a project id is also a directory name (ext.ValidProject).
func ValidProject(id string) bool {
	if id == "" || len([]rune(id)) > 64 || strings.TrimSpace(id) != id {
		return false
	}
	return !strings.ContainsFunc(id, func(c rune) bool { return c == '/' || c == '\\' || c == '.' || c < 0x20 })
}

// ProfileDir is where a project's browser keeps its profile.
func (m *Manager) ProfileDir(project string) string {
	return filepath.Join(m.Dir, "projects", project, "browser")
}

// Open a page on raw in the project's browser and return its tab id. width / height: the window (= viewport) in
// CSS px; 0 = 1280 x 900.
func (m *Manager) Open(ctx context.Context, project, raw string, width, height int) (int, error) {
	if !ValidProject(project) {
		return 0, fmt.Errorf(i18n.S("项目名 %q 不合法", "invalid project %q"), project)
	}
	if raw == "" {
		raw = "about:blank"
	} else if u, err := url.Parse(raw); err != nil || (u.Scheme != "http" && u.Scheme != "https" && raw != "about:blank") {
		return 0, fmt.Errorf("%s: %q", i18n.S("只能打开 http / https 地址", "Only http / https URLs can be opened"), raw)
	}
	// The desktop App running: the page opens in its browser, in the same profile, and the pane shows the real page
	// (chromeext/app.go). A profile is one browser's at a time, so this project's headless browser goes first; its
	// pages close (they come back in the App when opened again).
	// Just started (a restart, an update): the App's browser reconnects within seconds and may still hold the
	// profile; a headless browser opening it meanwhile would be two browsers writing one profile. Wait for it a moment.
	if !m.Bridge.AppConnected() && time.Since(m.started) < 15*time.Second {
		for i := 0; i < 50 && !m.Bridge.AppConnected(); i++ {
			select {
			case <-ctx.Done():
				return 0, ctx.Err()
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
	// The Electron App closed: its program serves the pages, windowless, on the same partitions (electron.go)
	if !m.Bridge.AppConnected() {
		if a, ok := m.app(); ok && a.Kind == "electron" {
			if err := m.startElectron(ctx, a); err != nil {
				return 0, err
			}
		}
	}
	if m.Bridge.AppConnected() {
		m.stopHeadless(project)
		loc := m.location(project)
		if !loc.shared {
			// the App connected but its data directory isn't known (an older App): not shown natively
			loc.profile = ""
		}
		m.adopt(project, loc)
		t, err := m.Bridge.OpenNative(ctx, project, loc.profile, raw, width, height, 0)
		if err == nil {
			return t.ID, nil
		}
		if m.Bridge.AppConnected() {
			// it's there but failed: don't open the profile headless under it
			return 0, err
		}
		m.Log.Warn("project browser: the App's browser went away, opening headless", "err", err)
	}
	in, err := m.instance(project)
	if err != nil {
		return 0, err
	}
	return in.open(ctx, raw, width, height)
}

// stopHeadless stops the headless browser a project's profile is open in (it's about to open in the App's).
func (m *Manager) stopHeadless(project string) {
	m.stopKey(m.location(project).key)
}

func (m *Manager) stopKey(key string) {
	m.mu.Lock()
	in := m.inst[key]
	delete(m.inst, key)
	m.mu.Unlock()
	if in != nil {
		m.Log.Info("project browser: stopping the headless browser", "project", in.project)
		in.stop()
	}
}

// Close every browser (Gerdr is exiting).
func (m *Manager) Close() {
	m.mu.Lock()
	all := make([]*instance, 0, len(m.inst))
	for _, in := range m.inst {
		all = append(all, in)
	}
	m.mu.Unlock()
	var wg sync.WaitGroup
	for _, in := range all {
		wg.Add(1)
		go func() { defer wg.Done(); in.stop() }()
	}
	wg.Wait()
}

func (m *Manager) instance(project string) (*instance, error) {
	loc := m.location(project)
	m.mu.Lock()
	in := m.inst[loc.key]
	if in != nil && !in.dead() && in.profile == loc.profile {
		m.mu.Unlock()
		return in, nil
	}
	m.mu.Unlock()
	if in != nil && !in.dead() {
		// the App's data directory runs one profile at a time (location.go): this project's replaces the other's
		m.stopKey(loc.key)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if in := m.inst[loc.key]; in != nil && !in.dead() && in.profile == loc.profile {
		return in, nil
	}
	in, err := m.start(project, loc)
	if err != nil {
		return nil, err
	}
	m.inst[loc.key] = in
	return in, nil
}

type instance struct {
	m       *Manager
	project string
	key     string // in Manager.inst
	profile string
	proc    *os.Process
	c       *conn
	exited  chan struct{}
	ua      string // the browser's user agent without "Headless": sites treat HeadlessChrome as a bot

	mu    sync.Mutex
	pages map[string]*page // by session id
	idle  *time.Timer
}

type page struct {
	target, session string
	owned           *chromeext.Owned
}

func (m *Manager) start(project string, loc location) (*instance, error) {
	exe := loc.exe
	if exe == "" {
		var err error
		if exe, err = findBrowser(); err != nil {
			return nil, err
		}
	}
	if err := os.MkdirAll(loc.dataDir, 0o700); err != nil {
		return nil, err
	}
	if loc.shared {
		// The App's browser has it (it's open, or quitting): never kill that one
		if lockHeld(loc.dataDir) {
			return nil, errAppHasIt
		}
		m.adopt(project, loc)
	} else {
		killStale(loc.dataDir)
	}
	cmd := exec.Command(exe,
		"--user-data-dir="+loc.dataDir,
		"--profile-directory="+loc.profile,
		"--headless",
		"--no-first-run",
		"--no-default-browser-check",
		// Cookies are encrypted with a key of the profile's own instead of the keychain's: the same profile may later
		// be opened by the App's browser copy (Lightron, re-signed), which the keychain would ask the user about
		"--use-mock-keychain",
		"--window-size=1280,900",
		// a 2x picture, so the pane is sharp on a Retina screen (the stream shrinks it for smaller ones)
		"--force-device-scale-factor=2",
		// Headless windows still cover each other: a page whose window is under another (the user clicked in a
		// second page) counted as hidden, stopped painting, and its pane froze (Page.screencastVisibilityChanged
		// false, no more frames). Every page here is on show in some pane, so none is ever in the background.
		"--disable-backgrounding-occluded-windows",
		noBoundSessions,
		"--disable-renderer-backgrounding",
		"--disable-background-timer-throttling",
		"--accept-lang="+acceptLang(),
	)
	w, r, after, err := pipeArgs(cmd)
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		after()
		return nil, fmt.Errorf("start %s: %w", exe, err)
	}
	after()
	in := &instance{m: m, project: project, key: loc.key, profile: loc.profile, proc: cmd.Process, exited: make(chan struct{}), pages: map[string]*page{}}
	go func() {
		cmd.Wait()
		close(in.exited)
		in.closedAll(i18n.S("项目浏览器退出了", "The project browser exited"))
	}()
	in.c = newConn(w, r, in.event)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	raw, err := in.c.call(ctx, "", "Browser.getVersion", nil)
	if err != nil {
		in.proc.Kill()
		return nil, fmt.Errorf(i18n.S("项目浏览器没有响应：%w", "The project browser didn't answer: %w"), err)
	}
	var v struct {
		UserAgent string `json:"userAgent"`
	}
	json.Unmarshal(raw, &v)
	in.ua = strings.Replace(v.UserAgent, "HeadlessChrome", "Chrome", 1)
	// pages opened by a page (target=_blank, window.open) are shown and driven too
	in.c.call(ctx, "", "Target.setDiscoverTargets", map[string]any{"discover": true})
	m.Log.Info("project browser started", "project", project, "exe", exe, "profile", filepath.Join(loc.dataDir, loc.profile))
	in.armIdle()
	return in, nil
}

func (in *instance) dead() bool {
	select {
	case <-in.exited:
		return true
	default:
		return false
	}
}

func (in *instance) stop() {
	if in.dead() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	in.c.call(ctx, "", "Browser.close", nil)
	cancel()
	select {
	case <-in.exited:
	case <-time.After(5 * time.Second):
		in.proc.Kill()
	}
}

// armIdle (re)starts the idle countdown while no page is open.
func (in *instance) armIdle() {
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.idle != nil {
		in.idle.Stop()
		in.idle = nil
	}
	if len(in.pages) > 0 {
		return
	}
	in.idle = time.AfterFunc(idleClose, func() {
		in.mu.Lock()
		n := len(in.pages)
		in.mu.Unlock()
		if n == 0 {
			in.m.Log.Info("project browser idle, closing", "project", in.project)
			in.stop()
		}
	})
}

func (in *instance) open(ctx context.Context, raw string, width, height int) (int, error) {
	if width <= 0 || height <= 0 {
		width, height = 1280, 900
	}
	res, err := in.c.call(ctx, "", "Target.createTarget", map[string]any{"url": "about:blank", "newWindow": true, "width": width, "height": height})
	if err != nil {
		return 0, err
	}
	var t struct {
		TargetID string `json:"targetId"`
	}
	json.Unmarshal(res, &t)
	p, err := in.attach(ctx, t.TargetID, raw, 0)
	if err != nil {
		return 0, err
	}
	return p.owned.ID(), nil
}

// attach takes a page over: a session on it, the logs on, then (when go is set) loads go with location.replace, so
// about:blank stays out of the history and the first load is logged.
func (in *instance) attach(ctx context.Context, target, goTo string, opener int) (*page, error) {
	res, err := in.c.call(ctx, "", "Target.attachToTarget", map[string]any{"targetId": target, "flatten": true})
	if err != nil {
		return nil, err
	}
	var a struct {
		SessionID string `json:"sessionId"`
	}
	json.Unmarshal(res, &a)
	p := &page{target: target, session: a.SessionID}
	send := func(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
		return in.c.call(ctx, p.session, method, params)
	}
	p.owned = in.m.Bridge.Adopt(chromeext.Page{
		Project: in.project,
		Opener:  opener,
		URL:     goTo,
		Send:    send,
		Close: func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			in.c.call(ctx, "", "Target.closeTarget", map[string]any{"targetId": target})
		},
		Resize: func(ctx context.Context, w, h int) error {
			res, err := in.c.call(ctx, "", "Browser.getWindowForTarget", map[string]any{"targetId": target})
			if err != nil {
				return err
			}
			var win struct {
				WindowID int `json:"windowId"`
			}
			json.Unmarshal(res, &win)
			// A headless window still keeps room for the browser's own bars (87 px tall on Chrome 157): ask for the
			// viewport's size plus the difference. Measured from the browser's side (the window's bounds against
			// Page.getLayoutMetrics), not with JavaScript, which doesn't answer while the page is navigating.
			if dw, dh, ok := in.frameSize(ctx, p.session, win.WindowID); ok {
				w, h = w+dw, h+dh
			}
			_, err = in.c.call(ctx, "", "Browser.setWindowBounds", map[string]any{"windowId": win.WindowID, "bounds": map[string]any{"width": w, "height": h}})
			return err
		},
	})
	in.mu.Lock()
	in.pages[p.session] = p
	if in.idle != nil {
		in.idle.Stop()
		in.idle = nil
	}
	in.mu.Unlock()
	if in.ua != "" {
		in.c.call(ctx, p.session, "Network.setUserAgentOverride", map[string]any{"userAgent": in.ua, "acceptLanguage": acceptLang()})
	}
	for _, m := range []string{"Page.enable", "Runtime.enable", "Network.enable", "Log.enable"} {
		in.c.call(ctx, p.session, m, nil)
	}
	in.c.call(ctx, p.session, "Runtime.runIfWaitingForDebugger", nil)
	if goTo != "" && goTo != "about:blank" {
		target, _ := json.Marshal(goTo)
		if _, err := in.c.call(ctx, p.session, "Runtime.evaluate", map[string]any{"expression": "location.replace(" + string(target) + ")"}); err != nil {
			p.owned.Gone(err.Error())
			return nil, err
		}
	}
	return p, nil
}

// frameSize: how much bigger the window is than the page's viewport. Only the height: a headless window has no side
// frame, and the layout viewport's width leaves out a scrollbar, which would count it twice.
func (in *instance) frameSize(ctx context.Context, session string, window int) (dw, dh int, ok bool) {
	raw, err := in.c.call(ctx, "", "Browser.getWindowBounds", map[string]any{"windowId": window})
	if err != nil {
		return 0, 0, false
	}
	var wb struct {
		Bounds struct{ Width, Height int } `json:"bounds"`
	}
	json.Unmarshal(raw, &wb)
	raw, err = in.c.call(ctx, session, "Page.getLayoutMetrics", nil)
	if err != nil {
		return 0, 0, false
	}
	var lm struct {
		V struct {
			ClientHeight float64 `json:"clientHeight"`
		} `json:"cssLayoutViewport"`
	}
	json.Unmarshal(raw, &lm)
	dh = wb.Bounds.Height - int(lm.V.ClientHeight+0.5)
	if lm.V.ClientHeight <= 0 || dh < 0 || dh > 400 {
		return 0, 0, false
	}
	return 0, dh, true
}

func (in *instance) event(m *msg) {
	if m.SessionID != "" {
		in.mu.Lock()
		p := in.pages[m.SessionID]
		in.mu.Unlock()
		if p != nil {
			p.owned.Event(m.Method, m.Params)
		}
		return
	}
	switch m.Method {
	case "Target.targetCreated":
		var e struct {
			TargetInfo struct {
				TargetID string `json:"targetId"`
				Type     string `json:"type"`
				OpenerID string `json:"openerId"`
				Attached bool   `json:"attached"`
			} `json:"targetInfo"`
		}
		json.Unmarshal(m.Params, &e)
		t := e.TargetInfo
		if t.Type != "page" || t.OpenerID == "" || t.Attached || !in.hasTarget(t.OpenerID) {
			return
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			// A new tab of the same window: the opener becomes a background tab (hidden, no more screencast frames)
			// until it is brought to the front again (chromeext caster.acquire), so the pane follows the new one
			if _, err := in.attach(ctx, t.TargetID, "", in.ownedOf(t.OpenerID)); err != nil {
				in.m.Log.Warn("project browser: popup", "err", err)
			}
		}()
	case "Target.detachedFromTarget":
		var e struct {
			SessionID string `json:"sessionId"`
		}
		json.Unmarshal(m.Params, &e)
		in.drop(e.SessionID, i18n.S("页面关了", "The page was closed"))
	case "Target.targetDestroyed":
		var e struct {
			TargetID string `json:"targetId"`
		}
		json.Unmarshal(m.Params, &e)
		in.mu.Lock()
		var sess string
		for s, p := range in.pages {
			if p.target == e.TargetID {
				sess = s
			}
		}
		in.mu.Unlock()
		if sess != "" {
			in.drop(sess, i18n.S("页面关了", "The page was closed"))
		}
	}
}

// ownedOf: the tab id of the page on target id (0 if it isn't one of ours).
func (in *instance) ownedOf(id string) int {
	in.mu.Lock()
	defer in.mu.Unlock()
	for _, p := range in.pages {
		if p.target == id {
			return p.owned.ID()
		}
	}
	return 0
}

func (in *instance) hasTarget(id string) bool {
	in.mu.Lock()
	defer in.mu.Unlock()
	for _, p := range in.pages {
		if p.target == id {
			return true
		}
	}
	return false
}

func (in *instance) drop(session, why string) {
	in.mu.Lock()
	p := in.pages[session]
	delete(in.pages, session)
	in.mu.Unlock()
	if p != nil {
		p.owned.Gone(why)
		in.armIdle()
	}
}

func (in *instance) closedAll(why string) {
	in.mu.Lock()
	ps := in.pages
	in.pages = map[string]*page{}
	if in.idle != nil {
		in.idle.Stop()
	}
	in.mu.Unlock()
	for _, p := range ps {
		p.owned.Gone(why)
	}
	in.m.mu.Lock()
	if in.m.inst[in.key] == in {
		delete(in.m.inst, in.key)
	}
	in.m.mu.Unlock()
}

// acceptLang: the system's language first, so sites answer in it (a new profile says en-US whatever the system's).
func acceptLang() string {
	for _, k := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		v := os.Getenv(k)
		if v == "" || v == "C" || v == "POSIX" {
			continue
		}
		v = strings.SplitN(v, ".", 2)[0]
		v = strings.ReplaceAll(v, "_", "-")
		if strings.HasPrefix(v, "zh") {
			return v + ",zh,en-US,en"
		}
		return v + ",en-US,en"
	}
	if i18n.S("zh", "en") == "zh" {
		return "zh-CN,zh,en-US,en"
	}
	return "en-US,en"
}
