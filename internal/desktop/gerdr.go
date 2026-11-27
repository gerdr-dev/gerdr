package desktop

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gerdr-dev/gerdr/internal/desktop/el"

	"github.com/gerdr-dev/gerdr/internal/selfupdate"
)

// shell: what desktop/main.js did for the UI, on Lightron: the messages of window.gerdrNative (bridge.js), the
// backend and its updates (app.go, backend.go), the menus (menu.go).
type shell struct {
	app *el.App

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

	views *appBrowser // the App's own browser for the Browser pane (views.go)
	bwins browserWins // windows showing one page of it (browserwin.go)

	mu     sync.Mutex
	spaces map[string]string // window id → the session (layout.ts Space) its page says it shows
	notes  map[string]time.Time
	keys   map[string]*string // menu.go: the UI's command → accelerator; nil value = no key
	lang   string             // menu.go: the page's language; "" = the system's
}

func (s *shell) setApp(a *el.App) {
	s.mu.Lock()
	s.app = a
	s.mu.Unlock()
}

// startBackend: Options.URLFunc. The backend's login link, as the page's first address.
func (s *shell) startBackend() (string, error) {
	// Placed only here, once this is the one instance: a second launch must not replace the file the first one's
	// backend runs from (selfupdate Place).
	if c := s.up.Backend(); s.up.Enabled() && !c.Bundled {
		s.bin = s.up.Place(c)
	}
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
	releaseBrowser(entry)
	return windowLink(entry), nil
}

// releaseBrowser: the backend's headless browser runs on this App's browser data directory while the App is closed
// (projects' profiles are there: internal/projbrowser location.go); it must let go before Lightron starts the
// browser on it, which happens right after this (URLFunc). Lightron also waits for any browser on it to quit.
func releaseBrowser(entry string) {
	u, err := url.Parse(entry)
	if err != nil || u.Host == "" {
		return
	}
	req, _ := http.NewRequest("POST", "http://"+u.Host+"/api/browser/release", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer "+u.Query().Get("token"))
	req.Header.Set("X-Gerdr", "1")
	req.Header.Set("Content-Type", "application/json")
	c := &http.Client{Timeout: 15 * time.Second}
	if resp, err := c.Do(req); err != nil {
		log.Printf("release the browser data directory: %v", err)
	} else {
		resp.Body.Close()
	}
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
func (s *shell) message(w *el.Window, raw json.RawMessage) {
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
	if typ != "menuKeys" && typ != "locale" && typ != "badge" && typ != "browserView" {
		log.Printf("page → %s", typ)
	}
	switch typ {
	case "notify":
		s.notify(w, str("title"), str("body"), str("sessionId"), str("space"), str("inboxItem"))
	case "__noteClick":
		s.noteClick(w, str("sessionId"), str("space"), str("inboxItem"))
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
		// path: a window for something other than a project's usual view (a page of its browser on its own,
		// ui/src/core/SessionBrowser.tsx openBrowserWindow)
		if p := str("path"); strings.HasPrefix(p, "/") && !strings.HasPrefix(p, "//") {
			if err := s.app.NewWindow(p); err != nil {
				log.Printf("new window: %v", err)
			}
		} else {
			s.newWindow(str("space"))
		}
	case "browserWindow", "browserWindowTab", "browserWindowClose", "browserWindows":
		s.browserWinMessage(w, str("type"), m)
	case "open":
		s.openExternal(str("url"))
	case "openPath":
		s.openPath(str("path"), str("cwd"), m["reveal"] == true)
	case "webPid":
		_ = w.Eval(fmt.Sprintf("window.__GERDR_WEB_PID__ = %d", s.app.WebPid(w)))
	case "update":
		s.updateMessage(m)
	case "browserView":
		if s.views != nil {
			s.views.placeView(w, m)
		}
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
func (s *shell) space(w *el.Window) string {
	if w == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.spaces[w.ID()]
}

// notify shows a notification for the page that asked (Electron's, from the App); its click comes back as noteClick.
func (s *shell) notify(w *el.Window, title, body, sessionID, space, item string) {
	key := title + "\n" + body + "\n" + sessionID + "\n" + item
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
		title = appName
	}
	s.app.Notify(w, title, body, sessionID, space, item)
}

// noteClick: the window showing the notification's session if there is one, else the one that notified; up front,
// on the conversation, or on the inbox item when it has none (a panel's to-do: ui/src/lib/inbox.ts openInboxItemById).
func (s *shell) noteClick(from *el.Window, sessionID, space, item string) {
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
	} else if item != "" {
		b, _ := json.Marshal(item)
		_ = target.Eval("window.gerdr && window.gerdr.openInboxItem && window.gerdr.openInboxItem(" + string(b) + ")")
	}
}

// openExternal: a link in the default browser (Electron's shell.openExternal: open on macOS, the default handler on
// Windows).
func (s *shell) openExternal(u string) {
	if pu, err := url.Parse(u); err == nil && (pu.Scheme == "http" || pu.Scheme == "https" || pu.Scheme == "mailto") && s.app != nil {
		s.app.OpenExternal(u)
	}
}

// openPath: the open / reveal buttons under a picture or video in the chat. Relative paths are the session's.
func (s *shell) openPath(p, cwd string, reveal bool) {
	home, _ := os.UserHomeDir()
	if strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		p = filepath.Join(home, p[2:])
	}
	if !filepath.IsAbs(p) {
		if cwd == "" {
			cwd = home
		}
		p = filepath.Join(cwd, p)
	}
	if _, err := os.Stat(p); err != nil || s.app == nil {
		return
	}
	s.app.OpenPath(p, reveal)
}

func originOf(u string) string {
	pu, err := url.Parse(u)
	if err != nil {
		return u
	}
	return pu.Scheme + "://" + pu.Host
}
