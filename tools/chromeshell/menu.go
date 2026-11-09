package main

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/zbysir/lightron"

	"github.com/gerdr-dev/gerdr/internal/i18n"
)

// Gerdr's menu bar, as desktop/main.js buildMacMenu had it: most items are the UI's commands
// (window.gerdr.runCommand), shown with the keys the UI's settings give them (the page's "menuKeys" message), in the
// page's language (its "locale" message). The keys are mostly for show: a key press reaches the page first and the
// page handles it.

// refreshMenu: the keys, the language or the update changed.
func (s *shell) refreshMenu() {
	s.mu.Lock()
	a := s.app
	s.mu.Unlock()
	if a != nil {
		a.SetMenu(s.menu())
		a.SetDockMenu(s.dockMenu())
	}
}

// zh: the menus' language: the page's own setting, else the system's.
func (s *shell) zh() bool {
	s.mu.Lock()
	lang := s.lang
	s.mu.Unlock()
	if lang != "" {
		return strings.HasPrefix(lang, "zh")
	}
	return i18n.Current() == i18n.ZH
}

func (s *shell) dockMenu() []lightron.MenuItem {
	label := "New Window"
	if s.zh() {
		label = "新窗口"
	}
	return []lightron.MenuItem{{Label: label, Click: func(w *lightron.Window) { s.newWindow(s.space(w)) }}}
}

func (s *shell) menu() []lightron.MenuItem {
	zh := s.zh()
	t := func(z, e string) string {
		if zh {
			return z
		}
		return e
	}
	s.mu.Lock()
	keys := s.keys
	s.mu.Unlock()
	// cmd: one of the UI's commands, with the key the UI's settings give it (fallback: desktop/main.js's default).
	cmd := func(label, id, def string) lightron.MenuItem {
		acc := def
		if k, ok := keys[id]; ok {
			acc = ""
			if k != nil {
				acc = *k
			}
		}
		return lightron.MenuItem{Label: label, Accelerator: acc, Click: func(w *lightron.Window) {
			if w != nil {
				b, _ := json.Marshal(id)
				_ = w.Eval("window.gerdr && window.gerdr.runCommand(" + string(b) + ")")
			}
		}}
	}
	act := func(label, acc string, f func(w *lightron.Window)) lightron.MenuItem {
		return lightron.MenuItem{Label: label, Accelerator: acc, Click: f}
	}
	role := func(label, r, acc string) lightron.MenuItem {
		return lightron.MenuItem{Label: label, Role: r, Accelerator: acc}
	}
	tabs := []lightron.MenuItem{}
	for i := 1; i <= 9; i++ {
		tabs = append(tabs, cmd(t(fmt.Sprintf("tab %d", i), fmt.Sprintf("Tab %d", i)), fmt.Sprintf("tab%d", i), fmt.Sprintf("Cmd+%d", i)))
	}
	// desktop/main.js: "Check for Updates…", or "Update to x…" once one is downloaded
	updateLabel := t("检查更新…", "Check for Updates…")
	if s.up != nil {
		if p := s.up.Pending(); p != nil {
			updateLabel = fmt.Sprintf(t("更新到 %s…", "Update to %s…"), p.Version)
		}
	}
	sep := lightron.Separator
	return []lightron.MenuItem{
		{Label: appName, Submenu: []lightron.MenuItem{
			act(updateLabel, "", func(*lightron.Window) { go s.checkFromMenu() }),
			sep,
			cmd(t("设置…", "Settings…"), "settings", "Cmd+,"),
			sep,
			role(t("隐藏 ", "Hide ")+appName, "hide", ""),
			role(t("隐藏其他", "Hide Others"), "hideOthers", ""),
			role(t("全部显示", "Show All"), "unhide", ""),
			sep,
			role(t("退出 ", "Quit ")+appName, "quit", ""),
		}},
		{Label: t("文件", "File"), Submenu: []lightron.MenuItem{
			act(t("新窗口", "New Window"), "Cmd+N", func(w *lightron.Window) { s.newWindow(s.space(w)) }),
			sep,
			cmd(t("新建 tab", "New Tab"), "newTab", "Cmd+T"),
			cmd(t("新工作空间", "New Workspace"), "newWorkspace", "Cmd+Shift+N"),
			cmd(t("会话列表", "Sessions"), "sessions", "Cmd+O"),
			sep,
			cmd(t("关闭 pane", "Close Pane"), "closePane", "Cmd+W"),
			cmd(t("重新打开刚关的 tab / pane", "Reopen Closed Tab / Pane"), "reopenClosed", "Cmd+Shift+T"),
			role(t("关闭窗口", "Close Window"), "close", "Cmd+Shift+W"),
		}},
		// The browser's own Edit menu (copy / paste / undo… and spelling, emoji), in its place.
		{Label: t("编辑", "Edit"), Role: "editMenu"},
		{Label: t("显示", "View"), Submenu: []lightron.MenuItem{
			cmd(t("命令面板", "Command Palette"), "palette", "Cmd+K"),
			cmd(t("显示 / 隐藏侧栏", "Show / Hide Sidebar"), "toggleSidebar", "Cmd+Ctrl+S"),
			cmd(t("复制到右边", "Duplicate Right"), "duplicateRight", "Cmd+D"),
			cmd(t("复制到下面", "Duplicate Down"), "duplicateDown", "Cmd+Shift+D"),
			cmd(t("左右分屏（空面板）", "Split Right (Blank Pane)"), "splitRight", ""),
			cmd(t("上下分屏（空面板）", "Split Down (Blank Pane)"), "splitDown", ""),
			cmd(t("放大 / 还原 pane", "Zoom / Restore Pane"), "zoom", "Cmd+Shift+Enter"),
			sep,
			role(t("放大", "Zoom In"), "zoomIn", ""),
			role(t("缩小", "Zoom Out"), "zoomOut", ""),
			role(t("实际大小", "Actual Size"), "resetZoom", ""),
			role(t("进入全屏", "Enter Full Screen"), "toggleFullScreen", ""),
			sep,
			act(t("重新载入", "Reload"), "Cmd+R", s.reload),
			role(t("开发者工具", "Developer Tools"), "devTools", ""),
			// the backend's own address: the user's browser doesn't know the pages' host
			act(t("在浏览器里打开", "Open in Browser"), "", func(*lightron.Window) { openExternal(s.backendEntry) }),
		}},
		{Label: t("前往", "Go"), Submenu: append(append([]lightron.MenuItem{
			cmd(t("下一个 tab", "Next Tab"), "nextTab", "Cmd+Shift+]"),
			cmd(t("上一个 tab", "Previous Tab"), "prevTab", "Cmd+Shift+["),
		}, tabs...), sep,
			cmd(t("跳到在等你的会话", "Jump to Session Waiting for You"), "focusAttention", "Cmd+J"),
			sep,
			cmd(t("下一个 session", "Next Session"), "nextSpace", "Cmd+Ctrl+]"),
			cmd(t("上一个 session", "Previous Session"), "prevSpace", "Cmd+Ctrl+["),
		)},
		// The browser's own Window menu (minimize, zoom, the list of windows).
		{Label: t("窗口", "Window"), Role: "windowMenu"},
		{Label: t("帮助", "Help"), Submenu: []lightron.MenuItem{
			cmd(t("快捷键", "Keyboard Shortcuts"), "help", "Cmd+/"),
			sep,
			act(t("急救：界面坏了时叫 agent 修", "Rescue: Have an Agent Fix a Broken UI"), "", s.rescue),
		}},
	}
}

// reload: the window's page again, through the login link (a reload of a page the backend now refuses would stay
// refused).
func (s *shell) reload(w *lightron.Window) {
	if w != nil {
		if _, err := w.Call("Page.navigate", map[string]any{"url": s.pageEntry(spacePath(s.space(w)))}); err != nil {
			log.Printf("reload: %v", err)
		}
	}
}

// rescue: the rescue panel is in the backend, not the UI directory: it opens even when the UI is broken. Pulled out in
// place if the page has it, else /rescue through the login link (it shows the UI with the panel open).
func (s *shell) rescue(w *lightron.Window) {
	if w == nil {
		return
	}
	if v, err := eval(w, "!!(window.__gerdrRescue && (window.__gerdrRescue.open(), 1))"); err == nil && string(v) == "true" {
		return
	}
	_, _ = w.Call("Page.navigate", map[string]any{"url": s.pageEntry("/rescue")})
}
