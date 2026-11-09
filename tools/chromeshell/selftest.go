package main

import (
	"encoding/json"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	_ "embed"
)

//go:embed page.html
var pageHTML []byte

// serveTestPage: a page on a fixed origin (the window's size and place are remembered per address).
func serveTestPage() string {
	ln, err := net.Listen("tcp", "127.0.0.1:17871")
	if err != nil {
		log.Fatal(err)
	}
	icon, _ := os.ReadFile(filepath.Join(repoRoot(), "desktop/res/icon.png"))
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(pageHTML)
	})
	mux.HandleFunc("/manifest.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/manifest+json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "/", "name": "Gerdr Spike", "start_url": "/", "display": "standalone",
			"icons": []map[string]string{{"src": "/icon.png", "sizes": "1024x1024", "type": "image/png"}},
		})
	})
	mux.HandleFunc("/icon.png", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(icon) })
	mux.HandleFunc("/icon.svg", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/svg+xml")
		http.ServeFile(w, r, filepath.Join(repoRoot(), "ui/public/icon.svg"))
	})
	go func() { _ = http.Serve(ln, mux) }()
	return "http://" + ln.Addr().String() + "/"
}

func repoRoot() string {
	_, f, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(f), "../..")
}

func (s *shell) anyWin() *win {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, w := range s.wins {
		return w
	}
	return nil
}

// pickFile puts a real path into the page's first <input type=file>, the way picking it in the dialog would.
func (s *shell) pickFile(session, selector, path string) error {
	_, _ = s.c.call(session, "DOM.enable", nil)
	doc, err := s.c.call(session, "DOM.getDocument", map[string]any{"depth": 0})
	if err != nil {
		return err
	}
	var d struct {
		Root struct {
			NodeID int `json:"nodeId"`
		} `json:"root"`
	}
	_ = json.Unmarshal(doc, &d)
	q, err := s.c.call(session, "DOM.querySelector", map[string]any{"nodeId": d.Root.NodeID, "selector": selector})
	if err != nil {
		return err
	}
	var n struct {
		NodeID int `json:"nodeId"`
	}
	_ = json.Unmarshal(q, &n)
	_, err = s.c.call(session, "DOM.setFileInputFiles", map[string]any{"nodeId": n.NodeID, "files": []string{path}})
	return err
}

func dock() string {
	out, _ := exec.Command("osascript", "-e", `tell application "System Events" to tell process "Dock" to get name of every UI element of list 1`).Output()
	var keep []string
	for _, n := range strings.Split(strings.TrimSpace(string(out)), ", ") {
		if strings.Contains(n, "Chrome") || strings.Contains(n, "Gerdr") {
			keep = append(keep, n)
		}
	}
	return strings.Join(keep, " | ")
}

func (s *shell) selftestPage() {
	w := s.anyWin()
	if w == nil {
		log.Print("selftest: no window")
		return
	}
	v0, err := s.c.eval(w.session, `[typeof window.__gerdrPost, typeof window.gerdrNative, (()=>{try{window.gerdrNative.postMessage({type:"hello"});return "ok"}catch(e){return String(e)}})()].join(" ")`)
	log.Printf("bridge probe on %s: %s %v", w.target[:8], v0, err)
	for _, id := range []string{"post", "badge", "pid"} {
		_, _ = s.c.eval(w.session, "document.getElementById('"+id+"').click()")
		time.Sleep(300 * time.Millisecond)
	}
	if err := s.pickFile(w.session, "#pick", filepath.Join(repoRoot(), "tools/chromeshell/main.go")); err != nil {
		log.Printf("pick: %v", err)
	}
	time.Sleep(time.Second)
	v, _ := s.c.eval(w.session, "document.getElementById('log').textContent")
	var text string
	_ = json.Unmarshal(v, &text)
	log.Printf("Dock: %s\npage log:\n%s", dock(), text)
}

// selftestGerdr: the real UI with the bridge in place. Checks what can be checked without a person at the screen.
func (s *shell) selftestGerdr(profile string) {
	w := s.anyWin()
	if w == nil {
		log.Print("selftest: no window")
		return
	}
	check := func(name, js string) {
		v, err := s.c.eval(w.session, js)
		if err != nil {
			log.Printf("✗ %s: %v", name, err)
			return
		}
		log.Printf("· %s: %s", name, tail(string(v), 300))
	}
	check("bridge", `!!window.gerdrNative && window.gerdrNative.shell`)
	check("standalone window", `matchMedia("(display-mode: standalone)").matches`)
	check("UI rendered", `document.getElementById("root")?.children.length > 0`)
	check("UI hooks", `Object.keys(window.gerdr || {}).join(",")`)
	check("page path", `location.pathname`)
	check("page origin (what a notification shows)", `location.origin + " secure=" + isSecureContext + " notifications=" + Notification.permission`)
	check("settings in localStorage", `Object.keys(localStorage).length`)
	// Above the page: the title bar only (~28px), or also a bar Chrome adds (a flags warning: ~40px more)
	check("height above the page", `outerHeight - innerHeight`)

	// Settings → Resources asks for the page's pid (lib/native.ts nativeWebPid).
	_, _ = s.c.eval(w.session, `window.gerdrNative.postMessage({ type: "webPid" })`)
	time.Sleep(300 * time.Millisecond)
	check("web pid", `window.__GERDR_WEB_PID__`)
	if v, err := s.c.eval(w.session, `window.__GERDR_WEB_PID__`); err == nil {
		out, _ := exec.Command("sh", "-c", "for p in $(pgrep -f 'gerdr-chromeshell-engine.*type=renderer'); do ps -o pid=,rss= -p $p; done").Output()
		log.Printf("· renderers (pid, rss KB), the page's should be the big one: page=%s\n%s", v, out)
	}
	check("badge", `window.gerdrNative.postMessage({ type: "badge", count: 2 }), "sent"`)
	time.Sleep(300 * time.Millisecond)
	_, _ = s.c.eval(w.session, `window.gerdrNative.postMessage({ type: "badge", count: 0 })`)

	// A file picked in the composer is attached by its path (ChatHost.localPath → gerdrNative.getPathForFile).
	// A picked file is attached by its path (ChatHost.localPath → gerdrNative.getPathForFile, synchronous). On an
	// input of our own: what the window shows (a composer or not) depends on the instance's layout.
	_, _ = s.c.eval(w.session, `(() => {
		const i = Object.assign(document.createElement("input"), { type: "file", id: "__selftest_pick" });
		i.style.display = "none";
		i.addEventListener("change", () => { window.__selftestPicked = [...i.files].map((f) => window.gerdrNative.getPathForFile(f)) });
		document.body.append(i);
	})()`)
	f := filepath.Join(repoRoot(), "tools/chromeshell/README.md")
	if err := s.pickFile(w.session, "#__selftest_pick", f); err != nil {
		log.Printf("✗ pick: %v", err)
	} else {
		time.Sleep(500 * time.Millisecond)
		check("picked file's path, read synchronously in the change handler", `window.__selftestPicked`)
	}
	_, _ = s.c.eval(w.session, `document.getElementById("__selftest_pick")?.remove()`)
	log.Printf("· Dock: %s", dock())

	// A notification, and a click on it: back through the bridge, focus the window, show that session.
	_, _ = s.c.eval(w.session, `window.gerdrNative.postMessage({ type: "notify", title: "chromeshell", body: "self-test", sessionId: "no-such-session", space: "default" })`)
	time.Sleep(500 * time.Millisecond)
	check("notification live", `window.__gerdrNotes ? window.__gerdrNotes.size : 0`)
	check("click it", `(() => { const n = [...window.__gerdrNotes].pop(); n.onclick(); n.close(); return "clicked" })()`)
	time.Sleep(500 * time.Millisecond)

	// Leaving Gerdr: window.open to another site goes to the default browser (logged, not opened, in the self-test).
	check("window.open outside", `String(window.open("https://example.com/from-window-open"))`)
	time.Sleep(300 * time.Millisecond)

	// Gerdr.app opened again: the second shell hands over and exits.
	self, _ := os.Executable()
	out, err := exec.Command(self, "-gerdr", "/nonexistent", "-profile", profile).CombinedOutput()
	log.Printf("· second instance: %v %s", err, strings.TrimSpace(string(out)))
	time.Sleep(500 * time.Millisecond)

	// The page's process dies: the shell loads it again.
	_, _ = s.c.call(w.session, "Page.crash", nil)
	time.Sleep(3 * time.Second)
	if w2 := s.anyWin(); w2 != nil {
		v, err := s.c.eval(w2.session, `document.getElementById("root")?.children.length > 0 && !!window.gerdrNative`)
		log.Printf("· after a crash, UI back with the bridge: %s %v", v, err)
	}

	// The browser's new-tab window (a Dock click "reopening" the app) brings up the Gerdr window there is, no second one.
	_, _ = s.c.call("", "Target.createTarget", map[string]any{"url": "chrome://newtab/", "newWindow": true})
	time.Sleep(2 * time.Second)
	s.mu.Lock()
	log.Printf("· after the browser's new-tab window: %d windows (want 1)", len(s.wins))
	s.mu.Unlock()
	// The menu's New Window (⌘N) opens a second Gerdr window.
	s.menuAction("newWindow")
	time.Sleep(3 * time.Second)
	s.mu.Lock()
	n := len(s.wins)
	var urls []string
	for _, x := range s.wins {
		if v, err := s.c.eval(x.session, "location.pathname"); err == nil {
			urls = append(urls, string(v))
		}
	}
	s.mu.Unlock()
	log.Printf("· after New Window: %d windows %v (want 2, both Gerdr)", n, urls)

	// Closing the last window quits (the backend stops with the shell).
	log.Print("· closing the windows")
	s.mu.Lock()
	var targets []string
	for _, x := range s.wins {
		targets = append(targets, x.target)
	}
	s.mu.Unlock()
	for _, t := range targets {
		r, err := s.c.call("", "Target.closeTarget", map[string]any{"targetId": t})
		log.Printf("· closeTarget: %s %v", r, err)
	}
}
