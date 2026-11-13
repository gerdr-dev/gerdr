import Foundation

/// Which Gerdr the App connects to: its remote-access address (Settings → Remote access, `https://…`). The App
/// carries nobody's domain (everyone connects to their own computer), so the first launch asks for it.
/// Same rules as android/…/Server.kt.
enum Server {
    private static let key = "origin"

    /// Parse what the person typed: a bare address (`gerdr.example.com`, with or without https) or a pairing link
    /// (`https://host/?pair=CODE`, from the settings QR / `gerdr remote pair`). The latter is loaded once as is: the
    /// server (remoteGuard's enterPair) swaps `?pair=` for the device cookie and cleans the address.
    static func parse(_ input: String) -> URL? {
        var s = input.trimmingCharacters(in: .whitespacesAndNewlines)
        if s.isEmpty { return nil }
        if !s.contains("://") { s = "https://" + s }
        guard let u = URL(string: s), let sc = u.scheme?.lowercased(), sc == "https" || sc == "http",
              let h = u.host, !h.isEmpty else { return nil }
        return u
    }

    /// `scheme://host[:port]`, default ports left out: the bridge compares origins in exactly this form, and so does
    /// the page's `location.origin`.
    static func origin(_ u: URL) -> String {
        let sc = (u.scheme ?? "https").lowercased()
        return format(scheme: sc, host: u.host ?? "", port: u.port ?? 0)
    }

    static func format(scheme: String, host: String, port: Int) -> String {
        let sc = scheme.lowercased()
        let def = (sc == "https" && port == 443) || (sc == "http" && port == 80)
        return "\(sc)://\(host.lowercased())" + (port == 0 || def ? "" : ":\(port)")
    }

    static func load() -> URL? { UserDefaults.standard.string(forKey: key).flatMap(URL.init(string:)) }
    static func save(_ origin: String) { UserDefaults.standard.set(origin, forKey: key) }

    /// Local / LAN addresses: only while debugging (a build pointed at `make dev`). The navigation lock lets these
    /// through and sends everything else to Safari, or one tap on a link in an agent's reply would replace the App
    /// with someone else's page.
    static func isLocalHost(_ host: String?) -> Bool {
        guard let host, !host.isEmpty else { return false }
        if host == "localhost" || host.hasSuffix(".local") { return true }
        let p = host.split(separator: ".").compactMap { Int($0) }
        guard p.count == 4 else { return false }
        return p[0] == 10 || (p[0] == 172 && (16...31).contains(p[1])) || (p[0] == 192 && p[1] == 168)
            || (p[0] == 100 && (64...127).contains(p[1])) // CGNAT (Tailscale and the like)
    }
}
