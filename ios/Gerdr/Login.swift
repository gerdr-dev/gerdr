import Foundation

/// Signing in / re-verifying with a passkey from the App: the device flow (server `/api/remote/cli/…`,
/// internal/auth/cliapprove.go; docs/dev/IOS.md "passkey"). WKWebView can only use a passkey when the App's
/// signature carries an Associated Domains entitlement for the domain, which a free Apple ID can't have and which
/// would bake one person's domain into the build. So the passkey is used in Safari instead: the App starts a request
/// and gets a display code → Safari opens the approve page, the person pastes the code, checks it's this iPhone and
/// approves with the passkey → the App polls and collects the device credential. Same flow as android/…/Login.kt.
///
/// Three rules to keep:
/// 1. The code never goes into the approve page's URL (`/?cli` only turns the page; a pre-filled link is exactly
///    what phishing wants).
/// 2. Re-verifying sends `Cookie: gerdr_dev=…`, not a Bearer token: the remote path only accepts the device
///    cookie (docs/dev/REMOTE.md). The session is ephemeral and never stores cookies, so it's sent by hand here only.
/// 3. Every POST carries `X-Gerdr: 1`: the server's CSRF gate, a 403 without it.
struct Login {
    let origin: String
    let ua: String

    /// Must match the server's `auth.CookieName` exactly: a mismatch shows as "approved, still on the pairing page".
    static let cookie = "gerdr_dev"

    struct Start {
        let id: String
        let poll: String
        let code: String
        let url: String
    }

    enum Poll {
        case pending
        case denied
        /// token: only for a new device; re-verifying renews the existing credential on the server
        case approved(token: String?)
    }

    struct Failure: Error {
        let message: String
        /// Couldn't reach the server (not the server saying no). Polling keeps waiting through these: switching
        /// networks or locking the phone for a moment hits them.
        var offline = false
    }

    private static let session = URLSession(configuration: .ephemeral)

    /// cookie: non-nil = re-verify this device (with its current credential); nil = ask for a new device.
    func start(cookie: String?) async throws -> Start {
        let (status, b) = try await post("/api/remote/cli/start", [:], cookie: cookie)
        if status == 409, b["reason"] as? String == "nopasskey" {
            throw Failure(message: L("这台 Gerdr 还没注册 passkey，只能用配对码", "This Gerdr has no passkey yet; use a pairing code"))
        }
        if status == 404 {
            throw Failure(message: L("这台 Gerdr 太旧了，不认 passkey 批准，先更新它", "This Gerdr is too old for passkey approval; update it first"))
        }
        guard status == 200, let id = b["id"] as? String, let poll = b["poll"] as? String,
              let code = b["code"] as? String, let url = b["url"] as? String else {
            throw Failure(message: (b["error"] as? String) ?? L("发起失败（HTTP \(status)）", "Couldn't start (HTTP \(status))"))
        }
        return Start(id: id, poll: poll, code: code, url: url)
    }

    func poll(_ s: Start) async throws -> Poll {
        let (status, b) = try await post("/api/remote/cli/poll", ["id": s.id, "poll": s.poll], cookie: nil)
        guard status == 200 else {
            throw Failure(message: (b["error"] as? String) ?? L("码过期了，再来一次", "The code expired; try again"))
        }
        switch b["state"] as? String {
        case "approved":
            let t = b["token"] as? String
            return .approved(token: (t?.isEmpty ?? true) ? nil : t)
        case "denied":
            return .denied
        default:
            return .pending
        }
    }

    // MARK: App key (AppKey.swift, server appkey.go): every call carries the device cookie

    /// Register this device's App key; only right after a presence proof (an approval), which is when it's called.
    func registerAppKey(pub: Data, cookie: String) async throws {
        let (status, b) = try await post("/api/remote/appkey/register", ["pub": pub.base64EncodedString()], cookie: cookie)
        guard status == 200 else { throw Failure(message: (b["error"] as? String) ?? "HTTP \(status)") }
    }

    struct Challenge {
        let nonce, host, deviceID: String
    }

    func appKeyChallenge(cookie: String) async throws -> Challenge {
        let (status, b) = try await post("/api/remote/appkey/challenge", [:], cookie: cookie)
        guard status == 200, let n = b["nonce"] as? String, let h = b["host"] as? String, let d = b["deviceId"] as? String else {
            throw Failure(message: (b["error"] as? String) ?? "HTTP \(status)")
        }
        return Challenge(nonce: n, host: h, deviceID: d)
    }

    func verifyAppKey(nonce: String, sig: Data, cookie: String) async throws {
        let (status, b) = try await post("/api/remote/appkey/verify", ["nonce": nonce, "sig": sig.base64EncodedString()], cookie: cookie)
        guard status == 200 else { throw Failure(message: (b["error"] as? String) ?? "HTTP \(status)") }
    }

    private func post(_ path: String, _ body: [String: Any], cookie: String?) async throws -> (Int, [String: Any]) {
        guard let url = URL(string: origin + path) else { throw Failure(message: "bad address \(origin)") }
        var r = URLRequest(url: url, timeoutInterval: 15)
        r.httpMethod = "POST"
        r.httpShouldHandleCookies = false
        r.setValue("application/json", forHTTPHeaderField: "Content-Type")
        r.setValue("1", forHTTPHeaderField: "X-Gerdr")
        // The WebView's own UA: the server names the new device from it ("iPhone · App"), and the approve page
        // shows it so the person can tell who is asking
        r.setValue(ua, forHTTPHeaderField: "User-Agent")
        if let cookie { r.setValue("\(Login.cookie)=\(cookie)", forHTTPHeaderField: "Cookie") }
        r.httpBody = try? JSONSerialization.data(withJSONObject: body)
        let data: Data, resp: URLResponse
        do {
            (data, resp) = try await Login.session.data(for: r)
        } catch {
            if error is CancellationError || (error as? URLError)?.code == .cancelled { throw CancellationError() }
            throw Failure(message: L("连不上 \(origin)：", "Can't reach \(origin): ") + error.localizedDescription, offline: true)
        }
        let status = (resp as? HTTPURLResponse)?.statusCode ?? 0
        let json = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any] ?? [:]
        return (status, json)
    }
}
