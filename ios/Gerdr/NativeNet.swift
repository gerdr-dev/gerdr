import Foundation

/// Add-ons' g.socket / g.fetch made by the phone itself, straight to the service; the iOS twin of Android's
/// NativeNet.kt, same bridge messages (`ws` / `http`, docs/dev/ANDROID.md "桥"), page half ui/src/lib/appnet.ts.
/// Otherwise they go through the computer's backend, which remotely is phone → relay → computer → service and
/// back: streaming speech-to-text (the voice add-on) lagged behind the voice. The page can't connect itself: its
/// CSP is `connect-src 'self'`, a browser WebSocket can't set the headers these APIs authenticate with, and their
/// HTTP APIs send no CORS headers.
///
/// It gives the page nothing it couldn't already do through the backend, and the bridge only answers the
/// server's origin, main frame. Same limits as the backend: http(s) / ws(s) only, 30s per request, 10 MB per answer.
///
/// When the phone can't reach the host at all (no DNS answer, refused) and nothing of the request went out, the
/// failure says `unreachable`: the page then makes the same request through the backend, for hosts only the
/// computer reaches (behind its VPN). Anything the service answered is never retried.
///
/// Delegate callbacks run on the main queue, so messages reach the page in order (`post` needs the main thread).
final class NativeNet: NSObject, URLSessionWebSocketDelegate {
    private let post: ([String: Any]) -> Void
    private var session: URLSession!
    /// Open sockets by the page's id, and the reverse for the delegate callbacks
    private var sockets: [String: URLSessionWebSocketTask] = [:]
    private var ids: [Int: String] = [:]
    private var opened: Set<String> = []

    private static let maxBody = 10 << 20

    init(post: @escaping ([String: Any]) -> Void) {
        self.post = post
        super.init()
        let cfg = URLSessionConfiguration.default
        cfg.timeoutIntervalForRequest = 30
        cfg.httpCookieStorage = nil // no shared cookie jar: add-ons don't share sessions, like the backend's client
        cfg.httpShouldSetCookies = false
        session = URLSession(configuration: cfg, delegate: self, delegateQueue: .main)
    }

    // MARK: - WebSocket

    func open(_ m: [String: Any]) {
        let id = m["id"] as? String ?? ""
        let s = m["url"] as? String ?? ""
        guard !id.isEmpty, let u = URL(string: s), ["ws", "wss"].contains(u.scheme?.lowercased() ?? "") else {
            post(ev(id, "error", ["msg": "url must be ws(s): \(s)"]))
            post(ev(id, "close", ["code": 1006, "reason": ""]))
            return
        }
        var req = URLRequest(url: u)
        for (k, v) in m["headers"] as? [String: String] ?? [:] { req.setValue(v, forHTTPHeaderField: k) }
        if let p = m["protocols"] as? [String], !p.isEmpty { req.setValue(p.joined(separator: ", "), forHTTPHeaderField: "Sec-WebSocket-Protocol") }
        let task = session.webSocketTask(with: req)
        task.maximumMessageSize = NativeNet.maxBody
        sockets[id] = task
        ids[task.taskIdentifier] = id
        task.resume()
        receive(id, task)
    }

    private func receive(_ id: String, _ task: URLSessionWebSocketTask) {
        task.receive { [weak self] r in
            // receive's handler isn't on the delegate queue
            DispatchQueue.main.async {
                guard let self, self.sockets[id] === task else { return }
                switch r {
                case .success(.string(let t)):
                    self.post(self.ev(id, "message", ["text": t]))
                    self.receive(id, task)
                case .success(.data(let d)):
                    self.post(self.ev(id, "message", ["b64": d.base64EncodedString()]))
                    self.receive(id, task)
                case .success:
                    self.receive(id, task)
                case .failure:
                    break // the task's completion (didCompleteWithError / didClose) says how it ended
                }
            }
        }
    }

    func send(_ m: [String: Any]) {
        guard let task = sockets[m["id"] as? String ?? ""] else { return }
        let msg: URLSessionWebSocketTask.Message
        if let b = m["b64"] as? String, let d = Data(base64Encoded: b) { msg = .data(d) } else { msg = .string(m["text"] as? String ?? "") }
        task.send(msg) { _ in } // a failed send ends the task, reported there
    }

    func close(_ m: [String: Any]) {
        guard let task = sockets[m["id"] as? String ?? ""] else { return }
        let n = m["code"] as? Int ?? 1000
        // 1005 / 1006 can't be sent; a page's close() without a code means 1000
        let code = URLSessionWebSocketTask.CloseCode(rawValue: n == 1000 || (3000...4999).contains(n) ? n : 1000) ?? .normalClosure
        task.cancel(with: code, reason: (m["reason"] as? String).flatMap { $0.isEmpty ? nil : $0.data(using: .utf8) })
    }

    /// The page went away (a new document): its sockets have no one to talk to.
    func closeAll() {
        for t in sockets.values { t.cancel() }
        sockets.removeAll()
        ids.removeAll()
        opened.removeAll()
    }

    func urlSession(_ s: URLSession, webSocketTask: URLSessionWebSocketTask, didOpenWithProtocol p: String?) {
        guard let id = ids[webSocketTask.taskIdentifier] else { return }
        opened.insert(id)
        post(ev(id, "open", ["protocol": p ?? ""]))
    }

    func urlSession(_ s: URLSession, webSocketTask: URLSessionWebSocketTask, didCloseWith code: URLSessionWebSocketTask.CloseCode, reason: Data?) {
        guard let id = forget(webSocketTask) else { return }
        post(ev(id, "close", ["code": code.rawValue, "reason": reason.flatMap { String(data: $0, encoding: .utf8) } ?? ""]))
    }

    func urlSession(_ s: URLSession, task: URLSessionTask, didCompleteWithError error: Error?) {
        guard let ws = task as? URLSessionWebSocketTask else { return }
        let wasOpen = opened.contains(ids[ws.taskIdentifier] ?? "")
        guard let id = forget(ws) else { return } // already closed (didCloseWith) or cancelled by the page's closeAll
        // No answer to the handshake and never open: nothing reached the service, safe to try another way
        let resp = task.response as? HTTPURLResponse
        var why = error?.localizedDescription ?? "closed"
        if let r = resp, r.statusCode != 101 { why = "HTTP \(r.statusCode)" }
        post(ev(id, "error", ["msg": why, "unreachable": resp == nil && !wasOpen && NativeNet.unreachable(error)]))
        post(ev(id, "close", ["code": 1006, "reason": ""]))
    }

    private func forget(_ task: URLSessionWebSocketTask) -> String? {
        guard let id = ids.removeValue(forKey: task.taskIdentifier) else { return nil }
        sockets.removeValue(forKey: id)
        opened.remove(id)
        return id
    }

    private func ev(_ id: String, _ ev: String, _ extra: [String: Any] = [:]) -> [String: Any] {
        var m: [String: Any] = ["t": "ws", "id": id, "ev": ev]
        m.merge(extra) { $1 }
        return m
    }

    /// The controller is going away: the session keeps its delegate (this) alive until invalidated.
    func shutdown() {
        closeAll()
        session.invalidateAndCancel()
    }

    // MARK: - HTTP

    /// One request, answered as `http` with the backend's /api/addons/{id}/fetch shape, or `error`.
    func fetch(_ m: [String: Any]) {
        let id = m["id"] as? String ?? ""
        let s = m["url"] as? String ?? ""
        guard let u = URL(string: s), ["http", "https"].contains(u.scheme?.lowercased() ?? "") else {
            return post(["t": "http", "id": id, "error": "url must be http(s): \(s)"])
        }
        var req = URLRequest(url: u)
        req.httpMethod = ((m["method"] as? String).flatMap { $0.isEmpty ? nil : $0 } ?? "GET").uppercased()
        for (k, v) in m["headers"] as? [String: String] ?? [:] { req.setValue(v, forHTTPHeaderField: k) }
        if let b = m["body"] as? String {
            req.httpBody = (m["bodyBase64"] as? Bool ?? false) ? Data(base64Encoded: b) : b.data(using: .utf8)
        }
        let binary = m["binary"] as? Bool ?? false
        let task = session.dataTask(with: req) { [weak self] data, resp, err in
            DispatchQueue.main.async {
                guard let self else { return }
                guard let r = resp as? HTTPURLResponse, let data else {
                    return self.post(["t": "http", "id": id, "error": err?.localizedDescription ?? "no response",
                                      "unreachable": NativeNet.unreachable(err)])
                }
                if data.count > NativeNet.maxBody {
                    return self.post(["t": "http", "id": id, "error": "response larger than \(NativeNet.maxBody >> 20) MB"])
                }
                var h: [String: String] = [:]
                for (k, v) in r.allHeaderFields { h[String(describing: k).lowercased()] = String(describing: v) }
                var out: [String: Any] = ["t": "http", "id": id, "status": r.statusCode,
                                          "statusText": HTTPURLResponse.localizedString(forStatusCode: r.statusCode), "headers": h]
                if binary {
                    out["body"] = data.base64EncodedString()
                    out["base64"] = true
                } else {
                    out["body"] = String(decoding: data, as: UTF8.self)
                }
                self.post(out)
            }
        }
        task.resume()
    }

    /// Failures that happen before any byte of the request leaves: the host has no address or takes no connection.
    /// A timeout isn't one (the request may have gone out), so a host that drops packets doesn't fall back.
    private static func unreachable(_ e: Error?) -> Bool {
        guard let u = e as? URLError else { return false }
        return [.cannotFindHost, .dnsLookupFailed, .cannotConnectToHost, .notConnectedToInternet].contains(u.code)
    }
}
