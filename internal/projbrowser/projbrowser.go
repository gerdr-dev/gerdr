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

// idleClose: a browser with no page open for this long exits (its profile stays).
const idleClose = 10 * time.Minute

// Manager runs one browser per project, started on the first page and stopped when idle.
type Manager struct {
	Dir    string // Gerdr's data dir
	Bridge *chromeext.Bridge
	Log    *slog.Logger

	mu   sync.Mutex
	inst map[string]*instance
}

func New(dir string, b *chromeext.Bridge, log *slog.Logger) *Manager {
	if log == nil {
		log = slog.Default()
	}
	return &Manager{Dir: dir, Bridge: b, Log: log, inst: map[string]*instance{}}
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
	in, err := m.instance(project)
	if err != nil {
		return 0, err
	}
	return in.open(ctx, raw, width, height)
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
	m.mu.Lock()
	defer m.mu.Unlock()
	if in := m.inst[project]; in != nil && !in.dead() {
		return in, nil
	}
	in, err := m.start(project)
	if err != nil {
		return nil, err
	}
	m.inst[project] = in
	return in, nil
}

type instance struct {
	m       *Manager
	project string
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

func (m *Manager) start(project string) (*instance, error) {
	exe, err := findBrowser()
	if err != nil {
		return nil, err
	}
	profile := m.ProfileDir(project)
	if err := os.MkdirAll(profile, 0o700); err != nil {
		return nil, err
	}
	killStale(profile)
	cmd := exec.Command(exe,
		"--user-data-dir="+profile,
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
	in := &instance{m: m, project: project, proc: cmd.Process, exited: make(chan struct{}), pages: map[string]*page{}}
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
	m.Log.Info("project browser started", "project", project, "exe", exe)
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
	if in.m.inst[in.project] == in {
		delete(in.m.inst, in.project)
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
