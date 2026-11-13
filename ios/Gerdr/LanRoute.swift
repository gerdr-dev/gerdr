import Foundation
import Network
import Security
import WebKit

/// LAN routing: the **same address** (`https://gerdr.example.com`), with its connections going to the Mac's LAN port
/// at home and through the relay elsewhere. Port of android/…/LanRoute.kt; the design and why the origin never
/// changes are in docs/dev/ANDROID.md "局域网直连" and docs/dev/IOS.md "LAN routing".
///
/// WKWebView (iOS 17+, `WKWebsiteDataStore.proxyConfigurations`) sends this one domain through a CONNECT proxy in
/// the App, which dials the LAN port once a probe passed and the domain otherwise. TLS runs from WebKit to the Mac:
/// **WebKit checks the certificate against the domain itself** (the LAN port serves the same real certificate by
/// SNI). The proxy only sees ciphertext; cookies and passkeys stay with the one origin.
///
/// Three rules, same as Android's:
/// 1. Only dial the LAN after a probe passed, and the probe is a **full TLS handshake verifying the domain**, not
///    "TCP connects". TCP up with the wrong certificate (another machine on that IP) would be a certificate error
///    page in WebKit, not a fallback to the relay.
/// 2. **When the network changes, drop the LAN route first, then probe again.** Dialing an old address after
///    leaving the Wi-Fi costs every new connection a timeout before it falls back.
/// 3. **The moment the LAN probe passes, close every connection going through the relay** (closeRelay). New
///    connections alone aren't enough: the terminal's WebSocket and idle keep-alives stay on the relay forever.
///    Closed, WebKit reopens them and they come over the LAN.
final class LanRoute {
    /// LAN round trips are milliseconds: handshake + certificate check fit in 1.5 s; don't keep people waiting outside
    private static let probeTimeout: TimeInterval = 1.5
    /// Relaying and Wi-Fi up: probe again this often (some switches send no event at all)
    private static let retry: TimeInterval = 20

    struct Cand: Equatable {
        let host: String
        let port: UInt16
    }

    private struct Target {
        let host: String
        let port: UInt16
        let lan: Bool
    }

    /// The server's domain and port; only CONNECTs to exactly this are accepted
    let host: String
    let port: UInt16
    private let q = DispatchQueue(label: "gerdr.lanroute")
    private var listener: NWListener?
    private var monitor: NWPathMonitor?
    private var pathKey = ""
    private var onWifi = false
    /// What the page reported (`/api/remote/lan`), fresh each time
    private var candidates: [Cand] = []
    /// The probed one; nil = through the relay (dial the domain as usual)
    private var lan: Cand?
    /// Bumped on every network change / new candidates: a late probe result for an older one is dropped
    private var gen = 0
    /// Tunnels **open now** on each path. The page's "LAN / relay" label draws this, not [lan] (where new
    /// connections go): right after switching, the terminal's long connection can still be on the relay.
    private var openLan = 0
    private var openRelay = 0
    private var relayUps: [ObjectIdentifier: NWConnection] = [:]
    private var stopped = false

    /// The route changed: `{t:"route", lan, relay, next, proxied}` for the page. Called on the main queue.
    var onChange: (([String: Any]) -> Void)?
    /// The proxy's port changed (the listener died while suspended and came back): the WebView needs the new one.
    var onProxy: ((Int) -> Void)?

    private static let cacheKey = "lanCandidates."

    /// nil when there's nothing to route: not https, an IP / local address (a debug instance), or iOS before 17.
    static func make(server: URL) -> LanRoute? {
        guard #available(iOS 17.0, *), server.scheme == "https", let h = server.host?.lowercased(), !h.isEmpty,
              !Server.isLocalHost(h), !isIP(h) else { return nil }
        return LanRoute(host: h, port: UInt16(server.port ?? 443))
    }

    private init(host: String, port: UInt16) {
        self.host = host
        self.port = port
    }

    /// Start the proxy and, **before the page loads**, probe last time's candidates (at most probeTimeout). Without
    /// this the page and the terminal's WebSocket come up over the relay first and the terminal stays there.
    /// ready gets the proxy's port on the main queue, or nil if the proxy couldn't start (then load without it).
    func start(ready: @escaping (Int?) -> Void) {
        q.async {
            self.startListener { port in
                guard let port else { return DispatchQueue.main.async { ready(nil) } }
                let cached = self.loadCached()
                self.candidates = cached
                let g = self.gen
                self.probeFirst(cached) { hit in
                    if g == self.gen { self.lan = hit }
                    self.watchNetwork()
                    DispatchQueue.main.async { ready(port) }
                }
            }
        }
    }

    func stop() {
        q.async {
            self.stopped = true
            self.gen += 1
            self.listener?.cancel()
            self.listener = nil
            self.monitor?.cancel()
            self.monitor = nil
            self.relayUps.values.forEach { $0.cancel() }
            self.relayUps.removeAll()
        }
    }

    /// The page reported the LAN port's addresses (`https://192.168.1.5:7878`).
    func setCandidates(_ origins: [String]) {
        q.async {
            UserDefaults.standard.set(origins.joined(separator: ","), forKey: Self.cacheKey + self.host)
            let cs = origins.compactMap(Self.parse)
            // Same as what we have and already probed: don't drop and re-probe (the page reports on every load,
            // and new connections would go through the relay for that moment)
            if cs == self.candidates && self.lan != nil { return }
            self.candidates = cs
            self.reprobeLocked()
        }
    }

    /// Send the current state again (a new page). Not a re-probe: that would drop the LAN route on every load.
    func announce() {
        q.async { self.changed() }
    }

    /// Back in the foreground / the network changed: drop, then probe.
    func reprobe() {
        q.async { self.reprobeLocked() }
    }

    // MARK: - Probing

    private func reprobeLocked() {
        guard !stopped else { return }
        gen += 1
        let g = gen
        lan = nil
        changed()
        let cs = candidates
        if cs.isEmpty { return }
        probeFirst(cs) { hit in
            guard g == self.gen else { return }
            self.lan = hit
            if hit != nil { self.closeRelay() }
            self.changed()
            NSLog("LanRoute: %@", hit.map { "LAN \(self.host) → \($0.host):\($0.port)" } ?? "LAN unreachable, relay")
            if hit == nil && self.onWifi {
                self.q.asyncAfter(deadline: .now() + Self.retry) {
                    if g == self.gen && self.lan == nil && self.onWifi { self.reprobeLocked() }
                }
            }
        }
    }

    private func probeFirst(_ cs: [Cand], _ i: Int = 0, done: @escaping (Cand?) -> Void) {
        guard i < cs.count else { return done(nil) }
        probe(cs[i]) { ok in
            if ok { done(cs[i]) } else { self.probeFirst(cs, i + 1, done: done) }
        }
    }

    /// A TLS handshake with the domain as SNI (the LAN port picks the real certificate by it) and the chain checked
    /// **for that domain name** (SecPolicyCreateSSL with the host): any valid certificate for another name fails.
    /// Pinned to Wi-Fi: a LAN address is never reachable over cellular, and that's where "Wi-Fi Assist" may send it.
    private func probe(_ c: Cand, done: @escaping (Bool) -> Void) {
        let tls = NWProtocolTLS.Options()
        let so = tls.securityProtocolOptions
        let name = host
        sec_protocol_options_set_tls_server_name(so, name)
        sec_protocol_options_set_verify_block(so, { _, trust, complete in
            let t = sec_trust_copy_ref(trust).takeRetainedValue()
            SecTrustSetPolicies(t, SecPolicyCreateSSL(true, name as CFString))
            var err: CFError?
            complete(SecTrustEvaluateWithError(t, &err))
        }, q)
        let tcp = NWProtocolTCP.Options()
        tcp.connectionTimeout = 2
        let p = NWParameters(tls: tls, tcp: tcp)
        p.requiredInterfaceType = .wifi
        guard let port = NWEndpoint.Port(rawValue: c.port) else { return done(false) }
        let conn = NWConnection(host: NWEndpoint.Host(c.host), port: port, using: p)
        var finished = false
        let finish = { (ok: Bool) in
            if finished { return }
            finished = true
            conn.stateUpdateHandler = nil
            conn.cancel()
            if !ok { NSLog("LanRoute: probe %@:%d failed", c.host, Int(c.port)) }
            done(ok)
        }
        conn.stateUpdateHandler = { st in
            switch st {
            case .ready: finish(true)
            case .failed, .waiting, .cancelled: finish(false)
            default: break
            }
        }
        conn.start(queue: q)
        q.asyncAfter(deadline: .now() + Self.probeTimeout) { finish(false) }
    }

    private func watchNetwork() {
        guard monitor == nil, !stopped else { return }
        let m = NWPathMonitor()
        m.pathUpdateHandler = { [weak self] path in
            guard let self else { return }
            self.onWifi = path.status == .satisfied && path.usesInterfaceType(.wifi)
            let key = "\(path.status)|" + path.availableInterfaces.map(\.name).joined(separator: ",")
            // The first update is the state we just probed in: re-probing then would drop a fresh LAN route
            // while the page is loading
            if self.pathKey.isEmpty { self.pathKey = key; return }
            if key != self.pathKey {
                self.pathKey = key
                self.reprobeLocked()
            }
        }
        m.start(queue: q)
        monitor = m
    }

    private func changed() {
        let m: [String: Any] = ["t": "route", "lan": openLan, "relay": openRelay, "next": lan != nil ? "lan" : "relay", "proxied": true]
        DispatchQueue.main.async { self.onChange?(m) }
    }

    // MARK: - The proxy

    /// Only for this App's WebView, on 127.0.0.1, and only CONNECTs to the server's own host:port. It forwards
    /// bytes and adds nothing (cookies live in WebKit), so another app reaching the port gets nothing it couldn't
    /// do by connecting to the domain itself.
    private func startListener(_ ready: @escaping (Int?) -> Void) {
        let p = NWParameters.tcp
        p.requiredLocalEndpoint = NWEndpoint.hostPort(host: "127.0.0.1", port: .any)
        p.acceptLocalOnly = true
        guard let l = try? NWListener(using: p) else { return ready(nil) }
        var told = false
        l.newConnectionHandler = { [weak self] c in self?.accept(c) }
        l.stateUpdateHandler = { [weak self] st in
            guard let self else { return }
            switch st {
            case .ready:
                let port = Int(l.port?.rawValue ?? 0)
                if !told { told = true; ready(port) } else { DispatchQueue.main.async { self.onProxy?(port) } }
            case .failed:
                // A suspended App can come back with its listening socket gone: start another, tell the WebView
                l.cancel()
                if self.listener === l, !self.stopped {
                    self.listener = nil
                    if !told { told = true; ready(nil); return }
                    self.startListener { port in if let port { DispatchQueue.main.async { self.onProxy?(port) } } }
                }
            default:
                break
            }
        }
        listener = l
        l.start(queue: q)
    }

    private func accept(_ c: NWConnection) {
        c.start(queue: q)
        readHead(c, Data())
    }

    /// Up to the blank line. Whatever follows (WebKit waits for the 200 first, but just in case) goes upstream.
    private func readHead(_ c: NWConnection, _ buf: Data) {
        c.receive(minimumIncompleteLength: 1, maximumLength: 8192) { [weak self] data, _, complete, err in
            guard let self else { return c.cancel() }
            var b = buf
            if let data { b.append(data) }
            if let r = b.range(of: Data("\r\n\r\n".utf8)) {
                self.connect(c, head: String(decoding: b[..<r.lowerBound], as: UTF8.self), rest: Data(b[r.upperBound...]))
            } else if err != nil || complete || b.count > 8192 {
                c.cancel()
            } else {
                self.readHead(c, b)
            }
        }
    }

    private func reply(_ c: NWConnection, _ status: String) {
        c.send(content: Data("HTTP/1.1 \(status)\r\nContent-Length: 0\r\n\r\n".utf8), completion: .contentProcessed { _ in c.cancel() })
    }

    private func connect(_ c: NWConnection, head: String, rest: Data) {
        let line = head.split(separator: "\r\n", maxSplits: 1).first.map { $0.split(separator: " ") } ?? []
        guard line.count >= 2, line[0] == "CONNECT" else { return reply(c, "405 Method Not Allowed") }
        let target = String(line[1])
        guard let i = target.lastIndex(of: ":"), let p = UInt16(target[target.index(after: i)...]) else {
            return reply(c, "400 Bad Request")
        }
        let h = target[..<i].trimmingCharacters(in: ["[", "]"]).lowercased()
        guard h == host, p == port else { return reply(c, "403 Forbidden") }
        NSLog("LanRoute: CONNECT %@:%d, LAN route %@", h, Int(p), lan.map { "\($0.host):\($0.port)" } ?? "none")
        let direct = Target(host: h, port: p, lan: false)
        let targets = lan.map { [Target(host: $0.host, port: $0.port, lan: true), direct] } ?? [direct]
        dial(targets, 0) { [weak self] up, isLan in
            guard let self else { return c.cancel() }
            guard let up else {
                NSLog("LanRoute: CONNECT %@:%d unreachable", h, Int(p))
                return self.reply(c, "502 Bad Gateway")
            }
            // Dialed before the LAN route existed, connected after it was found: it slipped through the
            // "drop, then probe" gap and closeRelay already went by. Close it now (a LAN dial that failed and
            // fell back to the relay is a fair fallback and stays)
            if !isLan && !targets.contains(where: \.lan) && self.lan != nil {
                up.cancel()
                return c.cancel()
            }
            self.tunnel(c, up, lan: isLan, rest: rest)
        }
    }

    private func dial(_ ts: [Target], _ i: Int, done: @escaping (NWConnection?, Bool) -> Void) {
        guard i < ts.count else { return done(nil, false) }
        let t = ts[i]
        let tcp = NWProtocolTCP.Options()
        tcp.connectionTimeout = t.lan ? 1 : 10
        let p = NWParameters(tls: nil, tcp: tcp)
        if t.lan { p.requiredInterfaceType = .wifi } // same network as the probe
        guard let port = NWEndpoint.Port(rawValue: t.port) else { return dial(ts, i + 1, done: done) }
        let up = NWConnection(host: NWEndpoint.Host(t.host), port: port, using: p)
        var finished = false
        up.stateUpdateHandler = { [weak self] st in
            guard !finished else { return }
            switch st {
            case .ready:
                finished = true
                up.stateUpdateHandler = nil
                done(up, t.lan)
            case .failed, .waiting, .cancelled:
                finished = true
                up.stateUpdateHandler = nil
                up.cancel()
                self?.dial(ts, i + 1, done: done)
            default:
                break
            }
        }
        up.start(queue: q)
    }

    private func tunnel(_ c: NWConnection, _ up: NWConnection, lan isLan: Bool, rest: Data) {
        let id = ObjectIdentifier(up)
        if isLan { openLan += 1 } else { openRelay += 1; relayUps[id] = up }
        changed()
        var closed = false
        let close = { [weak self] in
            guard let self, !closed else { return }
            closed = true
            c.cancel()
            up.cancel()
            if isLan { self.openLan -= 1 } else { self.openRelay -= 1; self.relayUps[id] = nil }
            self.changed()
        }
        // closeRelay cancels `up` from outside: that has to tear the tunnel down too
        up.stateUpdateHandler = { st in
            switch st {
            case .failed, .cancelled: close()
            default: break
            }
        }
        c.send(content: Data("HTTP/1.1 200 Connection Established\r\n\r\n".utf8), completion: .contentProcessed { _ in })
        if !rest.isEmpty { up.send(content: rest, completion: .contentProcessed { _ in }) }
        pump(c, up, close)
        pump(up, c, close)
    }

    /// One direction. Sends before reading more, so a slow side holds the other back instead of piling up memory.
    /// Either side ending closes both: TLS inside ends with its own close_notify anyway.
    private func pump(_ from: NWConnection, _ to: NWConnection, _ close: @escaping () -> Void) {
        from.receive(minimumIncompleteLength: 1, maximumLength: 64 * 1024) { [weak self] data, _, complete, err in
            if let data, !data.isEmpty {
                to.send(content: data, completion: .contentProcessed { e in
                    if e != nil || complete { return close() }
                    self?.pump(from, to, close)
                })
            } else if complete || err != nil {
                close()
            } else {
                self?.pump(from, to, close)
            }
        }
    }

    private func closeRelay() {
        let n = relayUps.count
        relayUps.values.forEach { $0.cancel() }
        if n > 0 { NSLog("LanRoute: on the LAN now, closed %d relay connections so they reconnect over it", n) }
    }

    // MARK: - Helpers

    private func loadCached() -> [Cand] {
        (UserDefaults.standard.string(forKey: Self.cacheKey + host) ?? "").split(separator: ",").compactMap { Self.parse(String($0)) }
    }

    /// Only `https://<LAN address>:<port>`: the page's list is a hint, never a reason to dial a public address.
    private static func parse(_ o: String) -> Cand? {
        guard let u = URL(string: o), u.scheme == "https", let h = u.host, let p = u.port, p > 0, p <= 65535,
              Server.isLocalHost(h) else { return nil }
        return Cand(host: h, port: UInt16(p))
    }

    private static func isIP(_ h: String) -> Bool {
        h.contains(":") || h.allSatisfy { $0.isNumber || $0 == "." }
    }
}
