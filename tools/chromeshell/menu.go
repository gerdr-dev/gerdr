package main

import (
	"bufio"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/gerdr-dev/gerdr/internal/i18n"
)

// Gerdr's menu bar on the engine (macOS). Chrome's own menu bar (File → New Tab, Bookmarks, Profiles…) is replaced by
// a library loaded into the browser process (menu/gerdrmenu.m); this side decides what is in it and runs what is
// clicked, the way desktop/main.js buildMacMenu does: most items are the UI's commands (window.gerdr.runCommand), shown
// with the keys the UI's settings give them (the page's "menuKeys" message). The keys are mostly for show: a key press
// reaches the page first and the page handles it (our items carry no Chrome command, so Chrome doesn't keep them for itself).

//go:embed menu/gerdrmenu.m
var menuSource []byte

type menu struct {
	sh    *shell
	mu    sync.Mutex
	conns []net.Conn
	keys  map[string]*string // the UI's command → accelerator ("Cmd+Shift+]"); nil value = no key
	lang  string             // the page's language (its "locale" message); "" = the system's
}

// menuLibrary: the library to load into the engine. The App carries it (package.sh); from the repo it is compiled
// here, again when the source changes (needs clang: Xcode's command line tools).
func menuLibrary(packaged bool, exePath, profile string) (string, error) {
	if packaged {
		p := filepath.Join(filepath.Dir(exePath), "../Resources/gerdrmenu.dylib")
		_, err := os.Stat(p)
		return p, err
	}
	sum := sha256.Sum256(menuSource)
	out := filepath.Join(filepath.Dir(profile), "gerdr-chromeshell-menu-"+hex.EncodeToString(sum[:6])+".dylib")
	if _, err := os.Stat(out); err == nil {
		return out, nil
	}
	src := out + ".m"
	if err := os.WriteFile(src, menuSource, 0o644); err != nil {
		return "", err
	}
	defer os.Remove(src)
	if o, err := exec.Command("clang", "-dynamiclib", "-fobjc-arc", "-framework", "Cocoa", "-O2", "-o", out, src).CombinedOutput(); err != nil {
		return "", fmt.Errorf("clang: %v %s", err, o)
	}
	_ = exec.Command("codesign", "--force", "-s", "-", out).Run()
	return out, nil
}

func (m *menu) serve(sock string) error {
	_ = os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return err
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			m.mu.Lock()
			m.conns = append(m.conns, c)
			m.mu.Unlock()
			m.send(c)
			go m.read(c)
		}
	}()
	return nil
}

func (m *menu) read(c net.Conn) {
	sc := bufio.NewScanner(c)
	for sc.Scan() {
		kind, arg, _ := strings.Cut(sc.Text(), " ")
		log.Printf("menu: %s %s", kind, arg)
		if m.sh == nil || m.sh.c == nil { // clicked before the page's address was known
			continue
		}
		switch kind {
		case "cmd":
			if w := m.sh.focusedWin(); w != nil {
				b, _ := json.Marshal(arg)
				_, _ = m.sh.c.eval(w.session, "window.gerdr && window.gerdr.runCommand("+string(b)+")")
			}
		case "action":
			m.sh.menuAction(arg)
		}
	}
	m.mu.Lock()
	for i, x := range m.conns {
		if x == c {
			m.conns = append(m.conns[:i], m.conns[i+1:]...)
			break
		}
	}
	m.mu.Unlock()
}

// update: the page sent its keys or language; rebuild the menu.
func (m *menu) update(keys map[string]*string, lang string) {
	m.mu.Lock()
	if keys != nil {
		m.keys = keys
	}
	if lang != "" {
		m.lang = lang
	}
	conns := append([]net.Conn(nil), m.conns...)
	m.mu.Unlock()
	for _, c := range conns {
		m.send(c)
	}
}

func (m *menu) send(c net.Conn) {
	b, _ := json.Marshal(m.spec())
	_, _ = c.Write(append(b, '\n'))
}

type item map[string]any

var sep = item{"separator": true}

// spec: the menu bar, as menu/gerdrmenu.m puts it into Chrome's: "dock" is the Dock icon's menu, "app" the items of
// the app menu, "edit" / "window" are Chrome's own menus in that place, the rest are ours. Same items as desktop/main.js buildMacMenu, minus
// what this prototype doesn't have (About, check for updates).
func (m *menu) spec() []item {
	m.mu.Lock()
	keys, lang := m.keys, m.lang
	m.mu.Unlock()
	zh := i18n.Current() == i18n.ZH
	if lang != "" {
		zh = strings.HasPrefix(lang, "zh")
	}
	s := func(z, e string) string {
		if zh {
			return z
		}
		return e
	}
	// cmd: one of the UI's commands, with the key the UI's settings give it (fallback: desktop/main.js's default).
	cmd := func(title, id, def string) item {
		acc := def
		if k, ok := keys[id]; ok {
			acc = ""
			if k != nil {
				acc = *k
			}
		}
		it := item{"title": title, "send": "cmd " + id}
		if key, mods := accelerator(acc); key != "" {
			it["key"], it["mods"] = key, mods
		}
		return it
	}
	act := func(title, name, acc string) item {
		it := item{"title": title, "send": "action " + name}
		if key, mods := accelerator(acc); key != "" {
			it["key"], it["mods"] = key, mods
		}
		return it
	}
	sel := func(title, selector, acc string) item {
		it := item{"title": title, "sel": selector}
		if key, mods := accelerator(acc); key != "" {
			it["key"], it["mods"] = key, mods
		}
		return it
	}
	// chrome: a copy of Chrome's own item (its behaviour is Chrome's): found by action, or by its key.
	chrome := func(title, how, acc string) item {
		it := item{"title": title, "chrome": how}
		if key, mods := accelerator(acc); key != "" {
			it["key"], it["mods"] = key, mods
		}
		return it
	}
	tabs := []item{}
	for i := 1; i <= 9; i++ {
		tabs = append(tabs, cmd(s(fmt.Sprintf("tab %d", i), fmt.Sprintf("Tab %d", i)), fmt.Sprintf("tab%d", i), fmt.Sprintf("Cmd+%d", i)))
	}
	name := appName
	// desktop/main.js: "Check for Updates…", or "Update to x…" once one is downloaded
	updateItem := act(s("检查更新…", "Check for Updates…"), "checkUpdate", "")
	if m.sh != nil && m.sh.up != nil {
		if p := m.sh.up.Pending(); p != nil {
			updateItem = act(fmt.Sprintf(s("更新到 %s…", "Update to %s…"), p.Version), "checkUpdate", "")
		}
	}
	return []item{
		// Right-click on the Dock icon (AppKit adds the window list, Options, Hide, Quit).
		{"role": "dock", "items": []item{act(s("新窗口", "New Window"), "newWindow", "")}},
		{"role": "app", "items": []item{
			updateItem,
			sep,
			cmd(s("设置…", "Settings…"), "settings", "Cmd+,"),
			sep,
			sel(s("隐藏 ", "Hide ")+name, "hide:", "Cmd+H"),
			sel(s("隐藏其他", "Hide Others"), "hideOtherApplications:", "Cmd+Alt+H"),
			sel(s("全部显示", "Show All"), "unhideAllApplications:", ""),
			sep,
			sel(s("退出 ", "Quit ")+name, "terminate:", "Cmd+Q"),
		}},
		{"title": s("文件", "File"), "items": []item{
			act(s("新窗口", "New Window"), "newWindow", "Cmd+N"),
			sep,
			cmd(s("新建 tab", "New Tab"), "newTab", "Cmd+T"),
			cmd(s("新工作空间", "New Workspace"), "newWorkspace", "Cmd+Shift+N"),
			cmd(s("会话列表", "Sessions"), "sessions", "Cmd+O"),
			sep,
			cmd(s("关闭 pane", "Close Pane"), "closePane", "Cmd+W"),
			cmd(s("重新打开刚关的 tab / pane", "Reopen Closed Tab / Pane"), "reopenClosed", "Cmd+Shift+T"),
			sel(s("关闭窗口", "Close Window"), "performClose:", "Cmd+Shift+W"),
		}},
		// Chrome's own Edit menu (copy / paste / undo… and spelling, emoji), in its place.
		{"role": "edit", "title": s("编辑", "Edit")},
		{"title": s("显示", "View"), "items": []item{
			cmd(s("命令面板", "Command Palette"), "palette", "Cmd+K"),
			cmd(s("显示 / 隐藏侧栏", "Show / Hide Sidebar"), "toggleSidebar", "Cmd+Ctrl+S"),
			cmd(s("复制到右边", "Duplicate Right"), "duplicateRight", "Cmd+D"),
			cmd(s("复制到下面", "Duplicate Down"), "duplicateDown", "Cmd+Shift+D"),
			cmd(s("左右分屏（空面板）", "Split Right (Blank Pane)"), "splitRight", ""),
			cmd(s("上下分屏（空面板）", "Split Down (Blank Pane)"), "splitDown", ""),
			cmd(s("放大 / 还原 pane", "Zoom / Restore Pane"), "zoom", "Cmd+Shift+Enter"),
			sep,
			chrome(s("放大", "Zoom In"), "key", "Cmd+="),
			chrome(s("缩小", "Zoom Out"), "key", "Cmd+-"),
			chrome(s("实际大小", "Actual Size"), "key", "Cmd+0"),
			chrome(s("进入全屏", "Enter Full Screen"), "toggleFullScreen:", ""),
			sep,
			act(s("重新载入", "Reload"), "reload", "Cmd+R"),
			chrome(s("开发者工具", "Developer Tools"), "key", "Cmd+Alt+I"),
			act(s("在浏览器里打开", "Open in Browser"), "openInBrowser", ""),
		}},
		{"title": s("前往", "Go"), "items": append(append([]item{
			cmd(s("下一个 tab", "Next Tab"), "nextTab", "Cmd+Shift+]"),
			cmd(s("上一个 tab", "Previous Tab"), "prevTab", "Cmd+Shift+["),
		}, tabs...), sep,
			cmd(s("跳到在等你的会话", "Jump to Session Waiting for You"), "focusAttention", "Cmd+J"),
			sep,
			cmd(s("下一个 session", "Next Session"), "nextSpace", "Cmd+Ctrl+]"),
			cmd(s("上一个 session", "Previous Session"), "prevSpace", "Cmd+Ctrl+["),
		)},
		// Chrome's own Window menu (minimize, zoom, the list of windows).
		{"role": "window", "title": s("窗口", "Window")},
		{"title": s("帮助", "Help"), "items": []item{
			cmd(s("快捷键", "Keyboard Shortcuts"), "help", "Cmd+/"),
			sep,
			act(s("急救：界面坏了时叫 agent 修", "Rescue: Have an Agent Fix a Broken UI"), "rescue", ""),
		}},
	}
}

// accelerator: an Electron-style accelerator ("Cmd+Shift+]", the UI's key settings and desktop/main.js) as an AppKit
// key equivalent and modifiers.
func accelerator(acc string) (string, []string) {
	if acc == "" {
		return "", nil
	}
	parts := strings.Split(acc, "+")
	key := parts[len(parts)-1]
	if key == "" && len(parts) > 1 { // "Cmd++"
		key = "+"
	}
	var mods []string
	for _, p := range parts[:len(parts)-1] {
		switch strings.ToLower(p) {
		case "cmd", "command", "cmdorctrl", "commandorcontrol", "super", "meta":
			mods = append(mods, "cmd")
		case "ctrl", "control":
			mods = append(mods, "ctrl")
		case "shift":
			mods = append(mods, "shift")
		case "alt", "option":
			mods = append(mods, "alt")
		}
	}
	named := map[string]string{
		"enter": "\r", "return": "\r", "tab": "\t", "space": " ", "backspace": "\b", "delete": "\u007f",
		"esc": "\u001b", "escape": "\u001b", "up": "", "down": "", "left": "", "right": "",
		"plus": "+", "home": "", "end": "", "pageup": "", "pagedown": "",
	}
	lk := strings.ToLower(key)
	if v, ok := named[lk]; ok {
		return v, mods
	}
	if len(lk) >= 2 && lk[0] == 'f' {
		var n int
		if _, err := fmt.Sscanf(lk[1:], "%d", &n); err == nil && n >= 1 && n <= 20 {
			return string(rune(0xf704 + n - 1)), mods
		}
	}
	return lk, mods
}

// focusedWin: the window a menu command is for: the one with focus, else any.
func (s *shell) focusedWin() *win {
	s.mu.Lock()
	ws := make([]*win, 0, len(s.wins))
	for _, w := range s.wins {
		ws = append(ws, w)
	}
	s.mu.Unlock()
	for _, w := range ws {
		if v, err := s.c.eval(w.session, "document.hasFocus()"); err == nil && string(v) == "true" {
			return w
		}
	}
	if len(ws) > 0 {
		return ws[0]
	}
	return nil
}

// menuAction: the menu items that are the shell's, not the UI's (desktop/main.js: newWindow, Reload, Open in Browser,
// the rescue panel).
func (s *shell) menuAction(name string) {
	w := s.focusedWin()
	space := ""
	if w != nil {
		s.mu.Lock()
		space = w.space
		s.mu.Unlock()
	}
	switch name {
	case "newWindow":
		s.launch(space)
	case "checkUpdate":
		go s.checkFromMenu()
	case "reload":
		if w != nil {
			_, _ = s.c.call(w.session, "Page.navigate", map[string]any{"url": pageURL(s.entry, space)})
		}
	case "openInBrowser":
		openExternal(s.entry)
	case "rescue":
		// The rescue panel is in the backend, not the UI directory: it opens even when the UI is broken. Pulled out
		// in place if the page has it, else /rescue through the login link (it shows the UI with the panel open).
		if w == nil {
			return
		}
		v, _ := s.c.eval(w.session, "!!(window.__gerdrRescue && (window.__gerdrRescue.open(), 1))")
		if string(v) != "true" {
			u, err := url.Parse(s.entry)
			if err == nil {
				if u.Path == "/auth" {
					q := u.Query()
					q.Set("next", "/rescue")
					u.RawQuery = q.Encode()
				} else {
					u.Path, u.RawQuery = "/rescue", ""
				}
				_, _ = s.c.call(w.session, "Page.navigate", map[string]any{"url": u.String()})
			}
		}
	}
}

// activate asks the engine to bring itself to the front (menu/gerdrmenu.m). Not `open -a`: that is a reopen, which
// Chrome answers with a new-tab window, which the shell answers with focus, which reopened again (a loop that ended
// with the window closed).
func (m *menu) activate() {
	m.mu.Lock()
	conns := append([]net.Conn(nil), m.conns...)
	m.mu.Unlock()
	for _, c := range conns {
		_, _ = c.Write([]byte("activate\n"))
	}
}
