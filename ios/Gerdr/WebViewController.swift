import SafariServices
import LocalAuthentication
import Network
import UIKit
import UniformTypeIdentifiers
import WebKit

/// The page: a WKWebView on the Gerdr's remote address. The UI is the web page as is; the shell only does what the
/// page can't (docs/dev/IOS.md): a bridge only that server's page can use, links out to Safari, downloads, the
/// colour outside the safe area, and signing in with a passkey through Safari.
final class WebViewController: UIViewController, WKNavigationDelegate, WKUIDelegate, WKScriptMessageHandler, WKDownloadDelegate {
    private weak var root: RootViewController?
    private let server: URL
    private let start: URL
    /// `scheme://host[:port]` the bridge answers to (Server.origin)
    let origin: String

    private var web: WKWebView!
    /// The page said hello: only then is there someone to answer. Reset with every new document.
    private var helloed = false
    /// The WebView's user agent, for the device flow's requests (the server names the device from it)
    private var ua = ""
    private var light = true
    private var loading: UIView?
    private var errorView: UIView?
    private var login: LoginRun?
    /// LAN routing (iOS 17+, an https domain); nil = everything goes the usual way
    private var route: LanRoute?
    private var pendingRoute: [String: Any]?

    init(root: RootViewController, server: URL, start: URL) {
        self.root = root
        self.server = server
        self.start = start
        self.origin = Server.origin(server)
        super.init(nibName: nil, bundle: nil)
    }

    required init?(coder: NSCoder) { fatalError() }

    deinit {
        web?.configuration.userContentController.removeScriptMessageHandler(forName: "gerdr")
        NotificationCenter.default.removeObserver(self)
        route?.stop()
        // The default data store outlives this page: don't leave the next server behind this one's proxy
        if #available(iOS 17.0, *), route != nil { web?.configuration.websiteDataStore.proxyConfigurations = [] }
    }

    override var preferredStatusBarStyle: UIStatusBarStyle { light ? .darkContent : .lightContent }

    override func viewDidLoad() {
        super.viewDidLoad()
        view.backgroundColor = Look.bg

        let cfg = WKWebViewConfiguration()
        cfg.websiteDataStore = .default()
        // The server's device list names devices from the UA: this part tells "App" from "Safari on the phone"
        // (auth.LabelFromUA looks for GerdrApp/)
        cfg.applicationNameForUserAgent = "Mobile/15E148 GerdrApp/\(appVersion)"
        cfg.allowsInlineMediaPlayback = true
        cfg.mediaTypesRequiringUserActionForPlayback = []
        // window.open / target=_blank arrive at createWebViewWith, which hands them to Safari
        cfg.preferences.javaScriptCanOpenWindowsAutomatically = true
        cfg.userContentController.addUserScript(bridgeScript())
        cfg.userContentController.add(WeakHandler(self), name: "gerdr")

        let w = WKWebView(frame: .zero, configuration: cfg)
        w.navigationDelegate = self
        w.uiDelegate = self
        w.isOpaque = false
        w.backgroundColor = Look.bg
        w.scrollView.backgroundColor = Look.bg
        // Pinned inside the safe area below, so the scroll view mustn't add the insets again
        w.scrollView.contentInsetAdjustmentBehavior = .never
        // Edge swipes would walk the SPA's history, not close the top layer like Android's back key
        w.allowsBackForwardNavigationGestures = false
        w.allowsLinkPreview = false
        #if DEBUG
        if #available(iOS 16.4, *) { w.isInspectable = true } // Safari → Develop → this iPhone
        #endif
        w.translatesAutoresizingMaskIntoConstraints = false
        view.addSubview(w)
        // The UI doesn't pad itself for notches / the home indicator (it never used env(safe-area-inset-*)), so it
        // goes inside the safe area and the strips around it take the page's background (theme)
        let g = view.safeAreaLayoutGuide
        NSLayoutConstraint.activate([
            w.topAnchor.constraint(equalTo: g.topAnchor),
            w.bottomAnchor.constraint(equalTo: g.bottomAnchor),
            w.leadingAnchor.constraint(equalTo: g.leadingAnchor),
            w.trailingAnchor.constraint(equalTo: g.trailingAnchor),
        ])
        web = w
        showLoading()
        // Back in the foreground: they may have come home meanwhile, probe the LAN again
        NotificationCenter.default.addObserver(self, selector: #selector(foreground), name: UIApplication.willEnterForegroundNotification, object: nil)
        guard #available(iOS 17.0, *), let r = LanRoute.make(server: server) else {
            w.load(URLRequest(url: start))
            return
        }
        route = r
        r.onChange = { [weak self] m in self?.routeChanged(m) }
        r.onProxy = { [weak self] port in self?.setProxy(port) }
        // The proxy has to be in place before the first request: what's sent before it doesn't go through it
        r.start { [weak self] port in
            guard let self else { return }
            if let port { self.setProxy(port) }
            self.web.load(URLRequest(url: self.start))
        }
    }

    @objc private func foreground() {
        route?.reprobe()
    }

    /// Only this domain goes through the proxy (matchDomains). allowFailover: if the proxy is gone (the App was
    /// suspended and its socket with it), WebKit connects directly, which is the relay path anyway. It also covers the
    /// iOS Simulator, where any proxyConfigurations fails with NSURLErrorBadURL (-1000, EINVAL) before connecting
    /// (measured: same code works in a macOS process on the same Mac), so LAN routing only shows on a real device.
    @available(iOS 17.0, *)
    private func setProxy(_ port: Int) {
        guard let r = route, let p = NWEndpoint.Port(rawValue: UInt16(clamping: port)), port > 0 else { return }
        var pc = ProxyConfiguration(httpCONNECTProxy: .hostPort(host: "127.0.0.1", port: p))
        pc.matchDomains = [r.host]
        pc.allowFailover = true
        web.configuration.websiteDataStore.proxyConfigurations = [pc]
        NSLog("LanRoute: WebKit proxy 127.0.0.1:%d for %@", port, r.host)
    }

    /// Tunnels open and close in bursts (a page load is a dozen): tell the page at most every 150 ms.
    private func routeChanged(_ m: [String: Any]) {
        let first = pendingRoute == nil
        pendingRoute = m
        guard first else { return }
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.15) { [weak self] in
            guard let self, let m = self.pendingRoute else { return }
            self.pendingRoute = nil
            self.post(m)
        }
    }

    // MARK: - Bridge (the other half: ui/src/lib/android.ts and pairpage.html; table in docs/dev/ANDROID.md "桥")

    /// Defines `window.gerdrIOS` with the same shape as Android's `gerdrAndroid` (postMessage(string) + onmessage),
    /// only on the server's own origin and only in the main frame. The message handler itself is visible to every
    /// frame, so [userContentController] checks the frame and origin again: agents' output is rendered in this page.
    private func bridgeScript() -> WKUserScript {
        let o = String(data: try! JSONSerialization.data(withJSONObject: origin, options: .fragmentsAllowed), encoding: .utf8)!
        let js = """
        (function () {
          if (location.origin !== \(o) || window.gerdrIOS) return;
          var h = window.webkit.messageHandlers.gerdr;
          window.gerdrIOS = { onmessage: null, postMessage: function (s) { h.postMessage(String(s)); } };
        })();
        """
        return WKUserScript(source: js, injectionTime: .atDocumentStart, forMainFrameOnly: true)
    }

    func userContentController(_ uc: WKUserContentController, didReceive msg: WKScriptMessage) {
        let so = msg.frameInfo.securityOrigin
        guard msg.frameInfo.isMainFrame, Server.format(scheme: so.protocol, host: so.host, port: so.port) == origin,
              let raw = msg.body as? String, let data = raw.data(using: .utf8),
              let m = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any],
              let t = m["t"] as? String else { return }
        switch t {
        case "hello":
            helloed = true
            hideLoading()
            web.evaluateJavaScript("navigator.userAgent") { [weak self] v, _ in self?.ua = v as? String ?? "" }
            // webauthn false: the pairing page hands its passkey button to us (startLogin). copyImage: the
            // page's long-press "copy image" goes through the shell (UIPasteboard)
            // lan: this App routes over the LAN, so the page sends candidates and draws the LAN / relay label
            // appKey: this phone can re-verify with Face ID (Secure Enclave + biometrics set up); the pairing page
            // offers it when the server says this device registered one
            post(["t": "hello", "platform": "ios", "version": appVersion, "webauthn": false, "scan": false, "copyImage": true,
                  "lan": route != nil, "appKey": AppKey.available])
            route?.announce() // a new document: its label starts from the current state
        case "theme":
            applyTheme(m["bg"] as? String ?? "", light: m["light"] as? Bool ?? true)
        case "open":
            if let s = m["url"] as? String, let u = URL(string: s) { openExternal(u) }
        case "setup":
            root?.showSetup(nil)
        case "login":
            startLogin(reauth: m["reauth"] as? Bool ?? false)
        case "appkey":
            verifyWithAppKey()
        case "lan":
            route?.setCandidates(m["origins"] as? [String] ?? [])
        case "copyImage":
            copyImage(m["data"] as? String ?? "", mime: m["mime"] as? String ?? "image/png")
        default:
            break // Android-only messages (lan, volkeys, notify…) and anything newer than this App
        }
    }

    /// Shell → page. Same format as Android: a JSON string in `{data}`, dispatched on `t`.
    func post(_ m: [String: Any]) {
        guard helloed, let u = web.url, Server.origin(u) == origin,
              let d = try? JSONSerialization.data(withJSONObject: m), let s = String(data: d, encoding: .utf8),
              let lit = try? JSONSerialization.data(withJSONObject: s, options: .fragmentsAllowed),
              let jsString = String(data: lit, encoding: .utf8) else { return }
        web.evaluateJavaScript("window.gerdrIOS && window.gerdrIOS.onmessage && window.gerdrIOS.onmessage({ data: \(jsString) })")
    }

    private func applyTheme(_ bg: String, light: Bool) {
        self.light = light
        // Native sheets / alerts / the login overlay follow the page's theme, not the phone's
        view.window?.overrideUserInterfaceStyle = light ? .light : .dark
        if let c = Css.color(bg) {
            view.backgroundColor = c
            web.backgroundColor = c
            web.scrollView.backgroundColor = c
        }
        setNeedsStatusBarAppearanceUpdate()
    }

    private func copyImage(_ b64: String, mime: String) {
        guard let data = Data(base64Encoded: b64) else {
            return post(["t": "copyImage", "ok": false, "error": L("图片数据不对", "Bad image data")])
        }
        let type = UTType(mimeType: mime) ?? .png
        UIPasteboard.general.setData(data, forPasteboardType: type.identifier)
        post(["t": "copyImage", "ok": true])
    }

    // MARK: - Navigation lock

    func webView(_ w: WKWebView, decidePolicyFor action: WKNavigationAction, preferences: WKWebpagePreferences,
                 decisionHandler: @escaping (WKNavigationActionPolicy, WKWebpagePreferences) -> Void) {
        guard let u = action.request.url else { return decisionHandler(.cancel, preferences) }
        if action.shouldPerformDownload { return decisionHandler(.download, preferences) }
        // iframes (previews in a reply) load what they load: the bridge never answers them
        if let f = action.targetFrame, !f.isMainFrame { return decisionHandler(.allow, preferences) }
        switch u.scheme?.lowercased() ?? "" {
        case "http", "https":
            // Anything that isn't this server goes to Safari: otherwise one link in an agent's reply replaces the
            // App with someone else's page, with no way back
            if Server.origin(u) == origin || Server.isLocalHost(u.host) {
                decisionHandler(.allow, preferences)
            } else {
                openExternal(u)
                decisionHandler(.cancel, preferences)
            }
        case "about", "blob", "data":
            decisionHandler(.allow, preferences)
        default: // mailto:, tel:, other apps' schemes
            openExternal(u)
            decisionHandler(.cancel, preferences)
        }
    }

    func webView(_ w: WKWebView, decidePolicyFor resp: WKNavigationResponse, decisionHandler: @escaping (WKNavigationResponsePolicy) -> Void) {
        // An attachment (the UI asks with ?download=1) or something WebKit can't show: download it
        let disp = (resp.response as? HTTPURLResponse)?.value(forHTTPHeaderField: "Content-Disposition") ?? ""
        if !resp.canShowMIMEType || disp.lowercased().hasPrefix("attachment") {
            decisionHandler(.download)
        } else {
            decisionHandler(.allow)
        }
    }

    func webView(_ w: WKWebView, didCommit navigation: WKNavigation!) {
        helloed = false // a new document has to say hello again; the old one's channel is gone
    }

    func webView(_ w: WKWebView, didFinish navigation: WKNavigation!) {
        hideLoading() // fallback: an older server's page never says hello
    }

    func webView(_ w: WKWebView, didFailProvisionalNavigation navigation: WKNavigation!, withError error: Error) {
        failed(error)
    }

    func webView(_ w: WKWebView, didFail navigation: WKNavigation!, withError error: Error) {
        failed(error)
    }

    private func failed(_ error: Error) {
        let e = error as NSError
        // Cancelled (a newer navigation took over) and "frame load interrupted" (a response turned into a download)
        if e.domain == NSURLErrorDomain && e.code == NSURLErrorCancelled { return }
        if e.domain == "WebKitErrorDomain" && e.code == 102 { return }
        showError(L("连不上这台 Gerdr", "Can't reach this Gerdr"), e.localizedDescription)
    }

    func webViewWebContentProcessDidTerminate(_ w: WKWebView) {
        // iOS kills the web process under memory pressure while in the background: a blank page until reloaded
        showLoading()
        w.reload()
    }

    // TLS: no didReceive challenge override on purpose. Default handling rejects a bad certificate, and the
    // remote address always has a real one (ACME, docs/dev/REMOTE.md). Never accept one unconditionally: this page
    // drives agents that can do anything on the computer.

    // MARK: - UI delegate

    func webView(_ w: WKWebView, createWebViewWith cfg: WKWebViewConfiguration, for action: WKNavigationAction,
                 windowFeatures: WKWindowFeatures) -> WKWebView? {
        if let u = action.request.url, ["http", "https"].contains(u.scheme?.lowercased() ?? "") { openExternal(u) }
        return nil
    }

    // Without these, alert() shows nothing and confirm() / prompt() answer "no" silently
    func webView(_ w: WKWebView, runJavaScriptAlertPanelWithMessage message: String, initiatedByFrame frame: WKFrameInfo,
                 completionHandler: @escaping () -> Void) {
        let a = UIAlertController(title: nil, message: message, preferredStyle: .alert)
        a.addAction(UIAlertAction(title: "OK", style: .default) { _ in completionHandler() })
        presentOrElse(a) { completionHandler() }
    }

    func webView(_ w: WKWebView, runJavaScriptConfirmPanelWithMessage message: String, initiatedByFrame frame: WKFrameInfo,
                 completionHandler: @escaping (Bool) -> Void) {
        let a = UIAlertController(title: nil, message: message, preferredStyle: .alert)
        a.addAction(UIAlertAction(title: L("取消", "Cancel"), style: .cancel) { _ in completionHandler(false) })
        a.addAction(UIAlertAction(title: "OK", style: .default) { _ in completionHandler(true) })
        presentOrElse(a) { completionHandler(false) }
    }

    func webView(_ w: WKWebView, runJavaScriptTextInputPanelWithPrompt prompt: String, defaultText: String?,
                 initiatedByFrame frame: WKFrameInfo, completionHandler: @escaping (String?) -> Void) {
        let a = UIAlertController(title: nil, message: prompt, preferredStyle: .alert)
        a.addTextField { $0.text = defaultText }
        a.addAction(UIAlertAction(title: L("取消", "Cancel"), style: .cancel) { _ in completionHandler(nil) })
        a.addAction(UIAlertAction(title: "OK", style: .default) { [weak a] _ in completionHandler(a?.textFields?.first?.text) })
        presentOrElse(a) { completionHandler(nil) }
    }

    /// WebKit insists every panel's handler is called exactly once: when something is already presented (the
    /// Safari sheet), answer right away instead.
    private func presentOrElse(_ vc: UIViewController, fallback: () -> Void) {
        if presentedViewController != nil || view.window == nil { return fallback() }
        present(vc, animated: true)
    }

    func openExternal(_ u: URL) {
        UIApplication.shared.open(u)
    }

    // MARK: - Downloads

    // Files (/api/fs/raw, /api/blobs) sit behind the device cookie only this WebView has, so the download runs
    // here (WKDownload shares its cookies), not in Safari (a 401 there). The finished file goes to the share sheet:
    // "Save to Files", AirDrop, another App.

    func webView(_ w: WKWebView, navigationAction: WKNavigationAction, didBecome download: WKDownload) {
        download.delegate = self
    }

    func webView(_ w: WKWebView, navigationResponse: WKNavigationResponse, didBecome download: WKDownload) {
        download.delegate = self
    }

    func download(_ download: WKDownload, decideDestinationUsing response: URLResponse, suggestedFilename: String,
                  completionHandler: @escaping (URL?) -> Void) {
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent("downloads/\(UUID().uuidString)", isDirectory: true)
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        let name = suggestedFilename.isEmpty ? "download" : suggestedFilename
        let dest = dir.appendingPathComponent(name)
        downloads[ObjectIdentifier(download)] = dest
        completionHandler(dest)
    }

    private var downloads: [ObjectIdentifier: URL] = [:]

    func downloadDidFinish(_ download: WKDownload) {
        guard let u = downloads.removeValue(forKey: ObjectIdentifier(download)) else { return }
        let share = UIActivityViewController(activityItems: [u], applicationActivities: nil)
        share.popoverPresentationController?.sourceView = view
        share.popoverPresentationController?.sourceRect = CGRect(x: view.bounds.midX, y: view.bounds.midY, width: 1, height: 1)
        share.completionWithItemsHandler = { _, _, _, _ in try? FileManager.default.removeItem(at: u.deletingLastPathComponent()) }
        if presentedViewController == nil { present(share, animated: true) }
    }

    func download(_ download: WKDownload, didFailWithError error: Error, resumeData: Data?) {
        downloads.removeValue(forKey: ObjectIdentifier(download))
        let a = UIAlertController(title: L("下载失败", "Download failed"), message: error.localizedDescription, preferredStyle: .alert)
        a.addAction(UIAlertAction(title: "OK", style: .default))
        presentOrElse(a) {}
    }

    // MARK: - Loading / error

    /// Cover the web view until the page draws: the first load goes through the relay and can take a while on a
    /// narrow tunnel, and a blank screen reads as "stuck" (Android saw exactly that report).
    private func showLoading() {
        errorView?.removeFromSuperview()
        errorView = nil
        guard loading == nil else { return }
        let cover = UIView()
        cover.backgroundColor = Look.bg
        let spin = UIActivityIndicatorView(style: .medium)
        spin.startAnimating()
        let text = Look.label(L("正在连接 \(server.host ?? origin)…", "Connecting to \(server.host ?? origin)…"), size: 13, color: Look.muted)
        text.textAlignment = .center
        let other = Look.button(L("换服务器", "Switch server")) { [weak self] in self?.root?.showSetup(nil) }
        other.isHidden = true
        let stack = UIStackView(arrangedSubviews: [spin, text, other])
        stack.axis = .vertical
        stack.spacing = 14
        stack.alignment = .center
        cover.frame = view.bounds
        cover.autoresizingMask = [.flexibleWidth, .flexibleHeight]
        view.addSubview(cover)
        Look.center(stack, in: cover)
        loading = cover
        // Still loading after a while: the address may be wrong, give a way out
        DispatchQueue.main.asyncAfter(deadline: .now() + 10) { [weak other] in other?.isHidden = false }
    }

    private func hideLoading() {
        guard let l = loading else { return }
        loading = nil
        UIView.animate(withDuration: 0.2, animations: { l.alpha = 0 }, completion: { _ in l.removeFromSuperview() })
    }

    private func showError(_ title: String, _ detail: String?) {
        loading?.removeFromSuperview()
        loading = nil
        errorView?.removeFromSuperview()
        let cover = UIView()
        cover.backgroundColor = Look.bg
        cover.frame = view.bounds
        cover.autoresizingMask = [.flexibleWidth, .flexibleHeight]
        let retry = Look.button(L("重试", "Retry"), primary: true) { [weak self] in
            guard let self else { return }
            self.showLoading()
            self.web.load(URLRequest(url: self.web.url.flatMap { Server.origin($0) == self.origin ? $0 : nil } ?? self.start))
        }
        let other = Look.button(L("换服务器", "Switch server")) { [weak self] in self?.root?.showSetup(nil) }
        let card = Look.card([
            Look.label(title, size: 17, weight: .semibold),
            Look.label(detail ?? "", size: 13, color: Look.muted),
            Look.label(server.host ?? origin, size: 12, color: Look.muted),
            retry, other,
        ])
        view.addSubview(cover)
        Look.center(card, in: cover)
        errorView = cover
    }

    // MARK: - Passkey sign-in (device flow, Login.swift)

    final class LoginRun {
        let reauth: Bool
        var cancelled = false
        var task: Task<Void, Never>?
        var overlay: UIView?
        var safari: SFSafariViewController?
        init(reauth: Bool) { self.reauth = reauth }
    }

    /// The pairing page's "sign in / verify with passkey" (the page knows from hello that we do it). reauth = this
    /// device is paired and only due for re-verification: start with its current credential, the server renews it
    /// and the cookie stays the same.
    private func startLogin(reauth: Bool) {
        guard login == nil else { return }
        let run = LoginRun(reauth: reauth)
        login = run
        let api = Login(origin: origin, ua: loginUA)
        run.task = Task { [weak self] in
            do {
                var cookie: String?
                if reauth { cookie = await self?.cookieToken() }
                let st: Login.Start
                do {
                    st = try await api.start(cookie: cookie)
                } catch let e as Login.Failure where cookie != nil && !e.offline {
                    // The credential is no longer accepted (revoked / expired): fall back to a new device. Not when
                    // merely offline: that would replace a credential that still works
                    st = try await api.start(cookie: nil)
                }
                guard let self, !run.cancelled else { return }
                // Copy the code for them: the keyboard in the approve page pushes the sheet up over the code
                // shown here. Still not "the code in the approve page": they paste it and check who's asking
                UIPasteboard.general.string = st.code
                self.showLoginOverlay(run, st)
                self.openApprove(run, st.url)
                // The code expires after 5 minutes (auth.CLITTL); one more cap for when the server stays unreachable
                let deadline = Date().addingTimeInterval(6 * 60)
                while !run.cancelled {
                    try await Task.sleep(nanoseconds: 2_000_000_000)
                    if run.cancelled { return }
                    let p: Login.Poll
                    do {
                        p = try await api.poll(st)
                    } catch let e as Login.Failure where e.offline {
                        if Date() > deadline { throw e }
                        continue
                    }
                    switch p {
                    case .pending:
                        continue
                    case .denied:
                        self.endLogin(run, "error", L("在浏览器里被拒绝了", "Denied in the browser"))
                        return
                    case .approved(let token):
                        await self.approved(run, token: token)
                        return
                    }
                }
            } catch let e as Login.Failure {
                self?.endLogin(run, "error", e.message)
            } catch {
                // cancelled
            }
        }
    }

    private func approved(_ run: LoginRun, token: String?) async {
        guard !run.cancelled else { return }
        guard let token else { // re-verified: same credential, renewed on the server
            if let c = await cookieToken() { await enrollAppKey(cookie: c) }
            return endLogin(run, "done", nil)
        }
        // A new device: the token is the device credential itself, the same table a pairing's cookie is in.
        // Attributes as the server's IssueCookie (host-only, HttpOnly, SameSite=Strict, Secure on https); 400 days,
        // the server decides the real lifetime and whoami sends it again anyway
        let store = web.configuration.websiteDataStore.httpCookieStore
        let host = server.host ?? ""
        for c in await store.allCookies() where c.name == Login.cookie && c.domain.trimmingCharacters(in: ["."]) == host {
            await store.deleteCookie(c)
        }
        var props: [HTTPCookiePropertyKey: Any] = [
            .name: Login.cookie, .value: token, .domain: host, .path: "/",
            .expires: Date().addingTimeInterval(400 * 24 * 3600),
            HTTPCookiePropertyKey("HttpOnly"): "TRUE",
            .sameSitePolicy: HTTPCookieStringPolicy.sameSiteStrict.rawValue,
        ]
        if server.scheme == "https" { props[.secure] = "TRUE" }
        guard let cookie = HTTPCookie(properties: props) else {
            return endLogin(run, "error", L("写不进登录凭据", "Couldn't store the credential"))
        }
        await store.setCookie(cookie)
        await enrollAppKey(cookie: token)
        endLogin(run, "done", nil)
    }

    // MARK: - App key (AppKey.swift): Face ID instead of the browser next time

    /// Right after an approval (the presence proof the server wants within minutes): make this device's key and
    /// register it, silently. Making a key needs no Face ID; using it does. Failing here only means the browser
    /// again next time, so it's logged, not shown.
    private func enrollAppKey(cookie: String) async {
        guard AppKey.available else { return }
        do {
            let pub = try AppKey.create(origin: origin)
            try await Login(origin: origin, ua: loginUA).registerAppKey(pub: pub, cookie: cookie)
        } catch {
            AppKey.delete(origin: origin)
            NSLog("AppKey: not registered: %@", "\(error)")
        }
    }

    /// The pairing page's "verify with Face ID": challenge → Face ID → signature → the server renews this device.
    /// Answers `appkey` {state: done | cancel | error, msg}; on error the page offers the browser approval.
    private func verifyWithAppKey() {
        Task { [weak self] in
            guard let self else { return }
            let reply = { (state: String, msg: String?) in
                var m: [String: Any] = ["t": "appkey", "state": state]
                if let msg { m["msg"] = msg }
                self.post(m)
            }
            guard AppKey.exists(origin: self.origin), let cookie = await self.cookieToken() else {
                return reply("error", L("这台手机上没有登记过的密钥，用浏览器批准一次", "No key on this phone yet; approve in the browser once"))
            }
            let api = Login(origin: self.origin, ua: self.loginUA)
            do {
                let c = try await api.appKeyChallenge(cookie: cookie)
                // The host signed is the server's; it must be the one this App was pointed at
                guard c.host.lowercased() == self.server.host?.lowercased() else {
                    return reply("error", L("服务器报的地址和 App 里的不一致", "The server's address doesn't match the App's"))
                }
                let sig = try AppKey.sign(origin: self.origin, host: c.host, deviceID: c.deviceID, nonce: c.nonce)
                try await api.verifyAppKey(nonce: c.nonce, sig: sig, cookie: cookie)
                reply("done", nil)
            } catch let e as LAError where e.code == .userCancel || e.code == .appCancel || e.code == .systemCancel {
                reply("cancel", nil)
            } catch let e as Login.Failure {
                reply("error", e.message)
            } catch {
                // Most likely the key was voided (a new face / finger enrolled): the browser approval makes a new one
                AppKey.delete(origin: self.origin)
                reply("error", L("Face ID 验证没成（密钥可能已失效），用浏览器批准一次", "Face ID verification failed (the key may be void); approve in the browser once"))
            }
        }
    }

    private var loginUA: String { ua.isEmpty ? "Mozilla/5.0 (iPhone) GerdrApp/\(appVersion)" : ua }

    private func cookieToken() async -> String? {
        let host = server.host ?? ""
        let all = await web.configuration.websiteDataStore.httpCookieStore.allCookies()
        return all.first { $0.name == Login.cookie && $0.domain.trimmingCharacters(in: ["."]) == host && !$0.value.isEmpty }?.value
    }

    /// Wrap up: remove the overlay, close the approve page, tell the page. On done the page goes in by itself.
    private func endLogin(_ run: LoginRun, _ state: String, _ msg: String?) {
        guard login === run else { return }
        run.cancelled = true
        login = nil
        run.overlay?.removeFromSuperview()
        if let sf = run.safari, sf.presentingViewController != nil { sf.dismiss(animated: true) }
        var m: [String: Any] = ["t": "login", "state": state]
        if let msg { m["msg"] = msg }
        post(m)
    }

    /// The approve page in Safari (SFSafariViewController: real Safari, so the domain's passkeys work there),
    /// as a half-height sheet so the code in the overlay above stays readable, like Android's partial custom tab.
    private func openApprove(_ run: LoginRun, _ url: String) {
        guard let u = URL(string: url), ["http", "https"].contains(u.scheme?.lowercased() ?? "") else {
            return endLogin(run, "error", L("批准页的地址不对：\(url)", "Bad approve page address: \(url)"))
        }
        if let old = run.safari, old.presentingViewController != nil { old.dismiss(animated: false) }
        let sf = SFSafariViewController(url: u)
        sf.dismissButtonStyle = .close
        sf.modalPresentationStyle = .pageSheet
        if let s = sf.sheetPresentationController {
            s.detents = [.medium(), .large()]
            s.prefersGrabberVisible = true
            s.largestUndimmedDetentIdentifier = .medium // the overlay's buttons stay tappable above it
        }
        run.safari = sf
        present(sf, animated: true)
    }

    private func showLoginOverlay(_ run: LoginRun, _ st: Login.Start) {
        let dim = UIView()
        dim.backgroundColor = Look.bg
        dim.frame = view.bounds
        dim.autoresizingMask = [.flexibleWidth, .flexibleHeight]

        let title = Look.label(run.reauth ? L("在浏览器里验证这台设备", "Verify this device in the browser")
                                          : L("在浏览器里批准这台设备", "Approve this device in the browser"),
                               size: 17, weight: .semibold)
        let lead = Look.label(
            L("在下面打开的页面里粘贴这个码（已经复制好了），看清是这台 iPhone 在请求，再用 passkey 批准。",
              "In the page below, paste this code (already copied), check that it's this iPhone asking, then approve with your passkey."),
            size: 13, color: Look.muted)
        let code = Look.label(st.code, size: 28, weight: .semibold)
        code.font = .monospacedSystemFont(ofSize: 28, weight: .semibold)
        code.textAlignment = .center
        let copy = Look.button(L("复制", "Copy")) { UIPasteboard.general.string = st.code }
        let reopen = Look.button(L("重新打开批准页", "Open the approve page again")) { [weak self, weak run] in
            guard let self, let run else { return }
            self.openApprove(run, st.url)
        }
        let cancel = Look.button(L("取消", "Cancel")) { [weak self, weak run] in
            guard let self, let run else { return }
            self.endLogin(run, "cancel", nil)
        }
        let row = UIStackView(arrangedSubviews: [copy, reopen])
        row.spacing = 8
        row.distribution = .fillEqually
        let card = Look.card([title, lead, code, row, cancel])
        card.translatesAutoresizingMaskIntoConstraints = false
        dim.addSubview(card)
        let g = dim.safeAreaLayoutGuide
        let w = card.widthAnchor.constraint(equalTo: g.widthAnchor, constant: -32)
        w.priority = .defaultHigh
        // Top half: the Safari sheet takes the bottom half
        NSLayoutConstraint.activate([
            card.topAnchor.constraint(equalTo: g.topAnchor, constant: 16),
            card.centerXAnchor.constraint(equalTo: g.centerXAnchor),
            card.widthAnchor.constraint(lessThanOrEqualToConstant: 400),
            w,
        ])
        view.addSubview(dim)
        run.overlay = dim
    }
}

/// WKUserContentController keeps its handlers strongly: going through this breaks the cycle
/// controller → handler → view controller → web view → controller.
private final class WeakHandler: NSObject, WKScriptMessageHandler {
    weak var target: WKScriptMessageHandler?
    init(_ t: WKScriptMessageHandler) { target = t }
    func userContentController(_ uc: WKUserContentController, didReceive m: WKScriptMessage) {
        target?.userContentController(uc, didReceive: m)
    }
}
