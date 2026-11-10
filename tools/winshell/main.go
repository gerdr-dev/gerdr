//go:build windows

// The Windows desktop shell: Gerdr's UI in WebView2 (the system's Chromium, Edge's), through Lightron
// (github.com/zbysir/lightron), which owns the window, its icon and title, the bridge and the single instance. What is
// Gerdr's lives here: the backend and its updates (update.go), the window.gerdrNative bridge the UI knows (bridge.js).
//
// Built and packaged on macOS or Linux by package.sh.
package main

import (
	_ "embed"
	"encoding/json"
	"log"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"unsafe"

	"github.com/zbysir/lightron"
	"golang.org/x/sys/windows"

	"github.com/gerdr-dev/gerdr/internal/selfupdate"
)

//go:embed bridge.js
var bridgeJS string

// The window's icon (desktop/res/icon.png at 256px; the exe's own is in its resources).
//
//go:embed icon.png
var iconPNG []byte

// appName, shellID: put in by package.sh (-X main.appName=…, -X main.shellID=$(tools/shell-id.sh)).
var (
	appName = "Gerdr exp"
	shellID = ""
)

var (
	user32         = windows.NewLazySystemDLL("user32.dll")
	procMessageBox = user32.NewProc("MessageBoxW")
)

func main() {
	data := filepath.Join(os.Getenv("LOCALAPPDATA"), appName)
	_ = os.MkdirAll(data, 0o755)
	if f, err := os.OpenFile(filepath.Join(data, "log.txt"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
		log.SetOutput(f) // a GUI program (-H windowsgui): nothing reads stderr
	}
	log.SetFlags(log.Ltime | log.Lmicroseconds)
	exe, _ := os.Executable()
	log.Printf("---- %s starting (%s)", appName, exe)

	// The backend, as the Electron shell ran it: `gerdr.exe --app` (print GERDR_READY; attach to a gerdr already
	// serving the data dir, else serve; --parent-pid: exit with us). The binary: the newer of the one next to this exe
	// and a downloaded update (internal/selfupdate).
	s := &shell{u: &updates{up: selfupdate.New(data, filepath.Join(filepath.Dir(exe), "gerdr.exe"), version, shellID, true)}}
	err := lightron.Run(lightron.Options{
		Name: appName,
		ID:   "io.github.zbysir.gerdr",
		Icon: iconPNG,
		// The pages are at http://gerdr/ (Lightron maps it to the backend's address), as on macOS: the notification's
		// origin line reads "gerdr", and the UI's per-device settings (localStorage, kept by origin) survive a port
		// that changed between starts. Since 0.2.27; before, they were under 127.0.0.1:<port> and start over once.
		Host: pageHost,
		// Started only once this is the one instance (a second launch just brings the first one's window up).
		URLFunc: func() (string, error) {
			u, err := s.runBackend()
			if err == nil {
				s.setEntry(u)
				s.mapped = hostOf(u)
				log.Printf("page: %s", origin(u))
			}
			return u, err
		},
		Window:    lightron.WindowOptions{Width: 1280, Height: 820},
		Script:    bridgeJS,
		OnMessage: s.message,
		// The WebView2 profile stays where it was (data\WebView2): the UI's settings live in it.
		DataDir: data,
		Logf:    log.Printf,
		OnReady: func(a *lightron.App) {
			s.mu.Lock()
			s.app = a
			s.mu.Unlock()
			go s.supervise()
			s.u.up.Start(s.pushUpdate)
			go s.pollBusy()
		},
	})
	if err != nil && err != lightron.ErrNoBrowser {
		fail(err.Error())
	}
	// The backend notices we're gone (--parent-pid; on Windows it waits on our process handle) and exits on its own.
	// No signal: Windows has no SIGTERM, and a kill would cut its session logs short (docs/dev/WHY-ELECTRON.md).
}

type shell struct {
	mu     sync.Mutex
	app    *lightron.App
	entry  string
	mapped string   // the backend's host:port that pageHost maps to (fixed when WebView2 starts)
	u      *updates // the backend and its updates (update.go)
}

func (s *shell) setEntry(u string) {
	s.mu.Lock()
	s.entry = u
	s.mu.Unlock()
}

const pageHost = "gerdr"

// pageLink: JS that takes the window to a backend address, at pageHost when the window is there and the browser
// maps pageHost to that address (fixed when WebView2 starts). Otherwise the real address: WebView2 didn't take the
// mapping (Lightron then shows the pages there), or the backend came back on another port (rare: the port is fixed;
// the page has no bridge there and starts with no settings until Gerdr is opened again).
func (s *shell) pageLink(u string) string {
	b, _ := json.Marshal(u)
	m, _ := json.Marshal(s.mapped)
	h, _ := json.Marshal(pageHost)
	return "(() => { const u = new URL(" + string(b) + "); if (location.host === " + string(h) + " && u.host === " +
		string(m) + ") u.host = " + string(h) + "; location.replace(u.href); })()"
}

func hostOf(u string) string {
	if pu, err := url.Parse(u); err == nil {
		return pu.Host
	}
	return ""
}

func (s *shell) getEntry() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.entry
}

// eval runs js in the window, once there is one.
func (s *shell) eval(js string) {
	s.mu.Lock()
	a := s.app
	s.mu.Unlock()
	if a == nil {
		return
	}
	for _, w := range a.Windows() {
		_ = w.Eval(js)
	}
}

// message: what the page posts (window.gerdrNative.postMessage), the set desktop/main.js handled.
func (s *shell) message(_ *lightron.Window, raw json.RawMessage) {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return
	}
	str := func(k string) string { v, _ := m[k].(string); return v }
	switch str("type") {
	case "open":
		openURL(str("url"))
	case "openPath":
		openPath(str("path"), str("cwd"), m["reveal"] == true)
	case "update":
		s.updateMessage(m)
	case "newWindow", "badge", "webPid", "space", "menuKeys", "locale":
		// Not on Windows: one window; no taskbar badge (ITaskbarList3 overlay icon); no menu bar (the Electron shell
		// had none on Windows either: its accelerators would eat the page's Ctrl+C / Ctrl+W).
	}
}

func messageBox(text string) {
	t, _ := windows.UTF16PtrFromString(text)
	c, _ := windows.UTF16PtrFromString(appName)
	_, _, _ = procMessageBox.Call(0, uintptr(unsafe.Pointer(t)), uintptr(unsafe.Pointer(c)), 0x10 /* MB_ICONERROR */)
}

func fail(msg string) {
	log.Print("fatal: ", msg)
	messageBox(msg)
	os.Exit(1)
}

func openURL(u string) {
	pu, err := url.Parse(u)
	if err != nil || (pu.Scheme != "http" && pu.Scheme != "https" && pu.Scheme != "mailto") {
		return
	}
	_ = shellExecute(u)
}

func shellExecute(target string) error {
	verb, _ := windows.UTF16PtrFromString("open")
	t, _ := windows.UTF16PtrFromString(target)
	return windows.ShellExecute(0, verb, t, nil, nil, windows.SW_SHOWNORMAL)
}

// openPath: the open / reveal buttons under a picture or video in the chat. Relative paths are the session's.
func openPath(p, cwd string, reveal bool) {
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
	if _, err := os.Stat(p); err != nil {
		return
	}
	if reveal {
		_ = exec.Command("explorer.exe", "/select,"+p).Start()
	} else {
		_ = shellExecute(p)
	}
}

func origin(u string) string {
	pu, err := url.Parse(u)
	if err != nil {
		return "?"
	}
	return pu.Scheme + "://" + pu.Host
}
