// gerdr 的 mac 外壳：一个窗口 + 一个 WKWebView，界面全在网页里。
//
// 壳只做网页做不了的事：起 / 收后端进程、系统通知、Dock 角标、菜单（尤其是「编辑」菜单 ——
// 没有它 WKWebView 里 ⌘C / ⌘V 不工作）、外部链接交给默认浏览器。
import AppKit
import UserNotifications
import WebKit

final class ServerProcess {
    private var proc: Process?
    private(set) var url: URL?
    var onReady: ((URL) -> Void)?
    var onFail: ((String) -> Void)?

    func start() {
        guard let bin = Bundle.main.path(forResource: "gerdr", ofType: nil) else {
            onFail?("App 包里找不到 gerdr 后端（Contents/Resources/gerdr）。")
            return
        }
        let p = Process()
        p.executableURL = URL(fileURLWithPath: bin)
        // --app：就绪后打印一行 GERDR_READY <url>；已经有一个 gerdr 在跑（比如终端里起的）就直接
        // 连它，两边看到的是同一份会话。--parent-pid：App 退出（哪怕是被强杀）时后端跟着退。
        p.arguments = ["--app", "--parent-pid", String(ProcessInfo.processInfo.processIdentifier)]
        var env = ProcessInfo.processInfo.environment
        env["GERDR_FROM_APP"] = "1"
        p.environment = env
        let out = Pipe()
        let err = Pipe()
        p.standardOutput = out
        p.standardError = err
        var buf = Data()
        var errBuf = Data()
        err.fileHandleForReading.readabilityHandler = { h in
            let d = h.availableData
            if !d.isEmpty { errBuf.append(d); if errBuf.count > 16_000 { errBuf.removeFirst(errBuf.count - 16_000) } }
        }
        out.fileHandleForReading.readabilityHandler = { [weak self] h in
            let d = h.availableData
            if d.isEmpty { return }
            buf.append(d)
            guard let s = String(data: buf, encoding: .utf8) else { return }
            for line in s.split(separator: "\n") where line.hasPrefix("GERDR_READY ") {
                let u = String(line.dropFirst("GERDR_READY ".count)).trimmingCharacters(in: .whitespaces)
                if let url = URL(string: u), self?.url == nil {
                    self?.url = url
                    DispatchQueue.main.async { self?.onReady?(url) }
                }
            }
        }
        p.terminationHandler = { [weak self] proc in
            DispatchQueue.main.async {
                guard let self = self else { return }
                if self.url == nil || proc.terminationReason == .uncaughtSignal || proc.terminationStatus != 0 {
                    let tail = String(data: errBuf, encoding: .utf8) ?? ""
                    self.onFail?("后端退出了（\(proc.terminationStatus)）\n\n\(tail.suffix(2000))")
                }
            }
        }
        do {
            try p.run()
            proc = p
        } catch {
            onFail?("起不来后端：\(error.localizedDescription)")
        }
        DispatchQueue.main.asyncAfter(deadline: .now() + 30) { [weak self] in
            if self?.url == nil { self?.onFail?("后端 30 秒没就绪。") }
        }
    }

    func stop() {
        guard let p = proc, p.isRunning else { return }
        p.terminate()
        let deadline = Date().addingTimeInterval(4)
        while p.isRunning && Date() < deadline { usleep(50_000) }
        if p.isRunning { kill(p.processIdentifier, SIGKILL) }
    }
}

final class AppDelegate: NSObject, NSApplicationDelegate, WKNavigationDelegate, WKUIDelegate, WKScriptMessageHandler, UNUserNotificationCenterDelegate {
    var window: NSWindow!
    var web: WKWebView!
    let server = ServerProcess()
    var origin: URL?

    func applicationDidFinishLaunching(_ n: Notification) {
        buildMenu()
        let cfg = WKWebViewConfiguration()
        let uc = WKUserContentController()
        uc.add(self, name: "gerdr")
        cfg.userContentController = uc
        cfg.preferences.setValue(true, forKey: "developerExtrasEnabled")
        cfg.websiteDataStore = .default()
        web = WKWebView(frame: .zero, configuration: cfg)
        web.navigationDelegate = self
        web.uiDelegate = self
        if #available(macOS 13.3, *) { web.isInspectable = true }
        web.setValue(false, forKey: "drawsBackground")

        window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 1280, height: 820),
                          styleMask: [.titled, .closable, .miniaturizable, .resizable],
                          backing: .buffered, defer: false)
        window.title = "gerdr"
        window.titlebarAppearsTransparent = true
        window.minSize = NSSize(width: 640, height: 420)
        window.contentView = web
        window.center()
        window.setFrameAutosaveName("gerdr.main")
        window.isReleasedWhenClosed = false
        window.makeKeyAndOrderFront(nil)
        showMessage("启动中…")

        UNUserNotificationCenter.current().delegate = self
        UNUserNotificationCenter.current().requestAuthorization(options: [.alert, .sound, .badge]) { _, _ in }

        server.onReady = { [weak self] url in
            self?.origin = url
            self?.web.load(URLRequest(url: url))
        }
        server.onFail = { [weak self] msg in self?.showMessage(msg) }
        server.start()
    }

    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool {
        if !flag { window.makeKeyAndOrderFront(nil) }
        return true
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool { false }

    func applicationWillTerminate(_ n: Notification) { server.stop() }

    func showMessage(_ text: String) {
        let esc = text.replacingOccurrences(of: "&", with: "&amp;").replacingOccurrences(of: "<", with: "&lt;")
        web.loadHTMLString("""
        <html><body style="font:13px -apple-system;color:#888;display:flex;height:100vh;margin:0;align-items:center;justify-content:center;background:transparent">
        <pre style="white-space:pre-wrap;max-width:640px;font:inherit">\(esc)</pre></body></html>
        """, baseURL: nil)
    }

    // ---- 网页 → 壳 ----

    func userContentController(_ c: WKUserContentController, didReceive m: WKScriptMessage) {
        guard let body = m.body as? [String: Any], let type = body["type"] as? String else { return }
        switch type {
        case "notify":
            let content = UNMutableNotificationContent()
            content.title = body["title"] as? String ?? "gerdr"
            content.body = body["body"] as? String ?? ""
            content.sound = .default
            if let sid = body["sessionId"] as? String { content.userInfo = ["sessionId": sid] }
            let req = UNNotificationRequest(identifier: UUID().uuidString, content: content, trigger: nil)
            UNUserNotificationCenter.current().add(req)
        case "badge":
            let n = body["count"] as? Int ?? 0
            NSApp.dockTile.badgeLabel = n > 0 ? String(n) : nil
            if n > 0 && !NSApp.isActive { NSApp.requestUserAttention(.informationalRequest) }
        case "open":
            if let s = body["url"] as? String, let u = URL(string: s) { NSWorkspace.shared.open(u) }
        case "webPid":
            // 设置里的「资源」要量界面这个 WebContent 进程的内存：它不是我们起的子进程，只能问 WKWebView（私有属性，取不到就算了）。
            let pid = (web.value(forKey: "_webProcessIdentifier") as? NSNumber)?.intValue ?? 0
            web.evaluateJavaScript("window.__GERDR_WEB_PID__ = \(pid)")
        default: break
        }
    }

    // 点通知：把窗口拉到前面，并跳到那个会话。
    func userNotificationCenter(_ c: UNUserNotificationCenter, didReceive r: UNNotificationResponse, withCompletionHandler done: @escaping () -> Void) {
        NSApp.activate(ignoringOtherApps: true)
        window.makeKeyAndOrderFront(nil)
        if let sid = r.notification.request.content.userInfo["sessionId"] as? String {
            let js = "window.gerdr && window.gerdr.focusSession(\(jsString(sid)))"
            web.evaluateJavaScript(js)
        }
        done()
    }

    // App 在前台时也显示通知（网页那边已经判断过「这个会话你看不见」才发）。
    func userNotificationCenter(_ c: UNUserNotificationCenter, willPresent n: UNNotification, withCompletionHandler done: @escaping (UNNotificationPresentationOptions) -> Void) {
        done([.banner, .sound])
    }

    // ---- 导航：外部链接交给默认浏览器 ----

    func webView(_ w: WKWebView, decidePolicyFor a: WKNavigationAction, decisionHandler: @escaping (WKNavigationActionPolicy) -> Void) {
        guard let u = a.request.url else { return decisionHandler(.allow) }
        if u.scheme == "about" || u.scheme == "blob" || u.scheme == "data" { return decisionHandler(.allow) }
        if let o = origin, u.host == o.host, u.port == o.port { return decisionHandler(.allow) }
        if a.navigationType == .linkActivated || a.targetFrame == nil {
            NSWorkspace.shared.open(u)
            return decisionHandler(.cancel)
        }
        decisionHandler(.allow)
    }

    func webView(_ w: WKWebView, createWebViewWith c: WKWebViewConfiguration, for a: WKNavigationAction, windowFeatures f: WKWindowFeatures) -> WKWebView? {
        if let u = a.request.url { NSWorkspace.shared.open(u) }
        return nil
    }

    func webView(_ w: WKWebView, runOpenPanelWith p: WKOpenPanelParameters, initiatedByFrame f: WKFrameInfo, completionHandler done: @escaping ([URL]?) -> Void) {
        let panel = NSOpenPanel()
        panel.allowsMultipleSelection = p.allowsMultipleSelection
        panel.canChooseDirectories = p.allowsDirectories
        panel.beginSheetModal(for: window) { r in done(r == .OK ? panel.urls : nil) }
    }

    func webViewWebContentProcessDidTerminate(_ w: WKWebView) {
        if let o = origin { w.load(URLRequest(url: o)) }
    }

    // ---- 菜单 ----

    @objc func runCommand(_ sender: NSMenuItem) {
        guard let id = sender.representedObject as? String else { return }
        web.evaluateJavaScript("window.gerdr && window.gerdr.runCommand(\(jsString(id)))")
    }

    @objc func reload(_ sender: Any?) {
        if let o = origin { web.load(URLRequest(url: o)) } else { web.reload() }
    }

    @objc func zoomIn(_ sender: Any?) { web.pageZoom = min(2.0, web.pageZoom + 0.1) }
    @objc func zoomOut(_ sender: Any?) { web.pageZoom = max(0.6, web.pageZoom - 0.1) }
    @objc func zoomReset(_ sender: Any?) { web.pageZoom = 1.0 }

    @objc func openInBrowser(_ sender: Any?) {
        if let o = origin { NSWorkspace.shared.open(o) }
    }

    func cmd(_ title: String, _ id: String, _ key: String = "", _ mods: NSEvent.ModifierFlags = [.command]) -> NSMenuItem {
        let it = NSMenuItem(title: title, action: #selector(runCommand(_:)), keyEquivalent: key)
        it.keyEquivalentModifierMask = mods
        it.representedObject = id
        it.target = self
        return it
    }

    func buildMenu() {
        let main = NSMenu()

        let appItem = NSMenuItem()
        let app = NSMenu()
        app.addItem(withTitle: "关于 gerdr", action: #selector(NSApplication.orderFrontStandardAboutPanel(_:)), keyEquivalent: "")
        app.addItem(.separator())
        app.addItem(cmd("设置…", "settings", ","))
        app.addItem(.separator())
        app.addItem(withTitle: "隐藏 gerdr", action: #selector(NSApplication.hide(_:)), keyEquivalent: "h")
        let others = app.addItem(withTitle: "隐藏其他", action: #selector(NSApplication.hideOtherApplications(_:)), keyEquivalent: "h")
        others.keyEquivalentModifierMask = [.command, .option]
        app.addItem(.separator())
        app.addItem(withTitle: "退出 gerdr", action: #selector(NSApplication.terminate(_:)), keyEquivalent: "q")
        appItem.submenu = app
        main.addItem(appItem)

        let fileItem = NSMenuItem()
        let file = NSMenu(title: "文件")
        file.addItem(cmd("新建 tab", "newTab", "t"))
        file.addItem(cmd("新工作空间", "newWorkspace", "n", [.command, .shift]))
        file.addItem(cmd("会话列表", "sessions", "o"))
        file.addItem(.separator())
        file.addItem(cmd("关闭 pane", "closePane", "w"))
        fileItem.submenu = file
        main.addItem(fileItem)

        // 编辑菜单是承重的：WKWebView 里的 ⌘C / ⌘V / ⌘A / ⌘Z 走的就是这些 selector。
        let editItem = NSMenuItem()
        let edit = NSMenu(title: "编辑")
        edit.addItem(withTitle: "撤销", action: Selector(("undo:")), keyEquivalent: "z")
        let redo = edit.addItem(withTitle: "重做", action: Selector(("redo:")), keyEquivalent: "z")
        redo.keyEquivalentModifierMask = [.command, .shift]
        edit.addItem(.separator())
        edit.addItem(withTitle: "剪切", action: #selector(NSText.cut(_:)), keyEquivalent: "x")
        edit.addItem(withTitle: "复制", action: #selector(NSText.copy(_:)), keyEquivalent: "c")
        edit.addItem(withTitle: "粘贴", action: #selector(NSText.paste(_:)), keyEquivalent: "v")
        edit.addItem(withTitle: "全选", action: #selector(NSText.selectAll(_:)), keyEquivalent: "a")
        editItem.submenu = edit
        main.addItem(editItem)

        let viewItem = NSMenuItem()
        let view = NSMenu(title: "显示")
        view.addItem(cmd("命令面板", "palette", "k"))
        view.addItem(cmd("显示 / 隐藏侧栏", "toggleSidebar", "s", [.command, .control]))
        view.addItem(cmd("左右分屏", "splitRight", "d"))
        view.addItem(cmd("上下分屏", "splitDown", "d", [.command, .shift]))
        view.addItem(cmd("放大 / 还原 pane", "zoom", "\r", [.command, .shift]))
        view.addItem(.separator())
        view.addItem(withTitle: "放大", action: #selector(zoomIn(_:)), keyEquivalent: "=")
        view.addItem(withTitle: "缩小", action: #selector(zoomOut(_:)), keyEquivalent: "-")
        view.addItem(withTitle: "实际大小", action: #selector(zoomReset(_:)), keyEquivalent: "0")
        view.addItem(.separator())
        view.addItem(withTitle: "重新载入", action: #selector(reload(_:)), keyEquivalent: "r")
        view.addItem(withTitle: "在浏览器里打开", action: #selector(openInBrowser(_:)), keyEquivalent: "")
        for it in view.items where it.action == #selector(zoomIn(_:)) || it.action == #selector(zoomOut(_:)) || it.action == #selector(zoomReset(_:)) || it.action == #selector(reload(_:)) || it.action == #selector(openInBrowser(_:)) {
            it.target = self
        }
        viewItem.submenu = view
        main.addItem(viewItem)

        let goItem = NSMenuItem()
        let go = NSMenu(title: "前往")
        go.addItem(cmd("下一个 tab", "nextTab", "]", [.command, .shift]))
        go.addItem(cmd("上一个 tab", "prevTab", "[", [.command, .shift]))
        for i in 1...9 { go.addItem(cmd("tab \(i)", "tab\(i)", "\(i)")) }
        go.addItem(.separator())
        go.addItem(cmd("跳到在等你的会话", "focusAttention", "j"))
        goItem.submenu = go
        main.addItem(goItem)

        let winItem = NSMenuItem()
        let win = NSMenu(title: "窗口")
        win.addItem(withTitle: "最小化", action: #selector(NSWindow.performMiniaturize(_:)), keyEquivalent: "m")
        win.addItem(withTitle: "缩放", action: #selector(NSWindow.performZoom(_:)), keyEquivalent: "")
        winItem.submenu = win
        main.addItem(winItem)
        NSApp.windowsMenu = win

        let helpItem = NSMenuItem()
        let help = NSMenu(title: "帮助")
        help.addItem(cmd("快捷键", "help", "/"))
        helpItem.submenu = help
        main.addItem(helpItem)

        NSApp.mainMenu = main
    }
}

func jsString(_ s: String) -> String {
    let data = try! JSONSerialization.data(withJSONObject: [s])
    let arr = String(data: data, encoding: .utf8)!
    return String(arr.dropFirst().dropLast())
}

let app = NSApplication.shared
let delegate = AppDelegate()
app.delegate = delegate
app.setActivationPolicy(.regular)
app.activate(ignoringOtherApps: true)
app.run()
