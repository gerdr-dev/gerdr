//go:build windows

// Gerdr exp for Windows: the UI in WebView2 (the system's Chromium, Edge's) inside a window of our own, around the Go
// backend. The counterpart of tools/chromeshell (macOS), and much less of it: WebView2 embeds in our window, so the
// taskbar icon, name, menu and notifications are ours without the clone / rename / injection macOS needs.
//
// Built and packaged on macOS by package.sh; not run on Windows yet (see README.md there for what is untested).
package main

import (
	"bufio"
	_ "embed"
	"encoding/json"
	"log"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unsafe"

	"github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"

	"github.com/gerdr-dev/gerdr/internal/selfupdate"
)

//go:embed bridge.js
var bridgeJS string

// The window's icon (desktop/res/icon.png at 256px). PNG straight into an icon at runtime (CreateIconFromResourceEx),
// no resource compiler in the build.
//
//go:embed icon.png
var iconPNG []byte

// appName, shellID: put in by package.sh (-X main.appName=…, -X main.shellID=$(tools/shell-id.sh)).
var (
	appName = "Gerdr exp"
	shellID = ""
)

var (
	user32          = windows.NewLazySystemDLL("user32.dll")
	procMessageBox  = user32.NewProc("MessageBoxW")
	procSendMessage = user32.NewProc("SendMessageW")
	procShowWindow  = user32.NewProc("ShowWindow")
	procSetFore     = user32.NewProc("SetForegroundWindow")
	procIsIconic    = user32.NewProc("IsIconic")
	procGetMetrics  = user32.NewProc("GetSystemMetrics")
	procIconFromRes = user32.NewProc("CreateIconFromResourceEx")
	procDpiContext  = user32.NewProc("SetProcessDpiAwarenessContext")
)

func main() {
	// Sharp on high-DPI screens (what a manifest's <dpiAwareness>PerMonitorV2</dpiAwareness> would say).
	_, _, _ = procDpiContext.Call(^uintptr(3)) // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 = -4
	data := filepath.Join(os.Getenv("LOCALAPPDATA"), appName)
	_ = os.MkdirAll(data, 0o755)
	if f, err := os.OpenFile(filepath.Join(data, "log.txt"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
		log.SetOutput(f) // a GUI program (-H windowsgui): nothing reads stderr
	}
	log.SetFlags(log.Ltime | log.Lmicroseconds)
	exe, _ := os.Executable()
	log.Printf("---- %s starting (%s)", appName, exe)

	// One instance: a second launch asks the first for its window and leaves (the Electron shell: second-instance).
	sock := filepath.Join(data, "shell.sock")
	if c, err := net.Dial("unix", sock); err == nil {
		_, _ = c.Write([]byte("focus\n"))
		c.Close()
		return
	}
	_ = os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		log.Printf("single-instance socket: %v", err)
	}

	// The backend, as the Electron shell runs it: `gerdr.exe --app` (print GERDR_READY; attach to a gerdr already
	// serving the data dir, else serve; --parent-pid: exit with us). The binary: the newer of the one next to this exe
	// and a downloaded update (internal/selfupdate).
	s := &shell{u: &updates{up: selfupdate.New(data, filepath.Join(filepath.Dir(exe), "gerdr.exe"), version, shellID, true)}}
	entry, err := s.runBackend()
	if err != nil {
		fail(err.Error())
	}
	s.entry = entry
	log.Printf("page: %s", origin(entry))

	w := webview2.NewWithOptions(webview2.WebViewOptions{
		// Debug turns on WebView2's own right-click menu and F12 (the developer tools, as the Electron shell had):
		// bridge.js drops the menu where it isn't useful. Off, both are gone, cut / copy / paste in fields included.
		Debug:     true,
		AutoFocus: true,
		DataPath:  filepath.Join(data, "WebView2"),
		WindowOptions: webview2.WindowOptions{
			Title: appName, Width: 1280, Height: 820, Center: true,
		},
	})
	if w == nil {
		// No WebView2 runtime (rare: Windows 11 has it, Windows 10 gets it with updates).
		msg := "需要 Microsoft Edge WebView2 运行时，点「确定」去下载安装，装好后重新打开。\n\n" +
			"Gerdr needs the Microsoft Edge WebView2 Runtime. Click OK to download it, then open Gerdr again."
		messageBox(msg)
		openURL("https://go.microsoft.com/fwlink/p/?LinkId=2124703")
		os.Exit(1)
	}
	defer w.Destroy()
	hwnd := uintptr(w.Window())
	setIcon(hwnd)

	s.w, s.hwnd = w, hwnd
	if err := w.Bind("__gerdrPost", s.message); err != nil {
		fail(err.Error())
	}
	w.Init(bridgeJS)
	w.Navigate(entry)
	go s.supervise()
	s.u.up.Start(s.pushUpdate)
	go s.pollBusy()

	if ln != nil {
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				line, _ := bufio.NewReader(c).ReadString('\n')
				c.Close()
				if line == "focus\n" {
					w.Dispatch(s.focus)
				}
			}
		}()
	}
	w.Run()
	// The backend notices we're gone (--parent-pid; on Windows it waits on our process handle) and exits on its own.
	// No signal: Windows has no SIGTERM, and a kill would cut its session logs short (docs/dev/WHY-ELECTRON.md).
}

type shell struct {
	w     webview2.WebView
	hwnd  uintptr
	entry string
	u     *updates // the backend and its updates (update.go)
}

// message: what the page posts (window.gerdrNative.postMessage), the set desktop/main.js handles. Called on the UI
// thread: anything slow goes to a goroutine, calls into the page through Dispatch.
func (s *shell) message(payload string) {
	var m map[string]any
	if json.Unmarshal([]byte(payload), &m) != nil {
		return
	}
	str := func(k string) string { v, _ := m[k].(string); return v }
	switch str("type") {
	case "open":
		go openURL(str("url"))
	case "openPath":
		go openPath(str("path"), str("cwd"), m["reveal"] == true)
	case "__focus":
		s.w.Dispatch(s.focus)
	case "__title":
		t := str("title")
		if t == "" {
			t = appName
		}
		s.w.Dispatch(func() { s.w.SetTitle(t) })
	case "update":
		s.updateMessage(m)
	case "newWindow", "badge", "webPid", "space", "menuKeys", "locale":
		// Not in this prototype: one window; no taskbar badge (ITaskbarList3 overlay icon); no menu bar (the Electron
		// shell has none on Windows either: its accelerators would eat the page's Ctrl+C / Ctrl+W).
	}
}

func (s *shell) focus() {
	if r, _, _ := procIsIconic.Call(s.hwnd); r != 0 {
		_, _, _ = procShowWindow.Call(s.hwnd, 9) // SW_RESTORE
	}
	_, _, _ = procShowWindow.Call(s.hwnd, 5) // SW_SHOW
	_, _, _ = procSetFore.Call(s.hwnd)
}

// setIcon: the window's big (Alt+Tab, taskbar) and small (title bar) icons, from the embedded PNG.
func setIcon(hwnd uintptr) {
	for _, size := range []struct{ which, metricX, metricY uintptr }{{1, 11, 12}, {0, 49, 50}} { // ICON_BIG SM_CXICON; ICON_SMALL SM_CXSMICON
		cx, _, _ := procGetMetrics.Call(size.metricX)
		cy, _, _ := procGetMetrics.Call(size.metricY)
		icon, _, _ := procIconFromRes.Call(uintptr(unsafe.Pointer(&iconPNG[0])), uintptr(len(iconPNG)), 1 /* icon */, 0x00030000, cx, cy, 0)
		if icon != 0 {
			_, _, _ = procSendMessage.Call(hwnd, 0x0080 /* WM_SETICON */, size.which, icon)
		}
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
