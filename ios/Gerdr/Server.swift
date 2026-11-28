import Foundation
import Network

/// Which Gerdr the App connects to: its remote-access address (Settings → Remote access, `https://…`). The App
/// carries nobody's domain (everyone connects to their own computer), so the first launch asks for it.
/// Same rules as android/…/Server.kt.
enum Server {
    private static let key = "origin"
    private static let recentKey = "recentOrigins"

    /// Parse what the person typed: a bare address (`gerdr.example.com`, with or without https) or a pairing link
    /// (`https://host/?pair=CODE`, from the settings QR / `gerdr remote pair`). The latter is loaded once as is: the
    /// server (remoteGuard's enterPair) swaps `?pair=` for the device cookie and cleans the address.
    static func parse(_ input: String) -> URL? {
        var s = input.trimmingCharacters(in: .whitespacesAndNewlines)
        if s.isEmpty { return nil }
        if !s.contains("://") { s = "https://" + s }
        guard let u = URL(string: s), let sc = u.scheme?.lowercased(), sc == "https" || sc == "http",
              let h = u.host, !h.isEmpty, u.user == nil, u.password == nil else { return nil }
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

    /// Include the active address saved by earlier versions. Pairing links are used once and only the origin is saved.
    static func recent() -> [String] {
        let defaults = UserDefaults.standard
        let active = defaults.string(forKey: key)
        let saved = defaults.stringArray(forKey: recentKey) ?? []
        guard let active else { return saved }
        return [active] + saved.filter { $0 != active }
    }

    static func save(_ origin: String) {
        let defaults = UserDefaults.standard
        defaults.set([origin] + recent().filter { $0 != origin }, forKey: recentKey)
        defaults.set(origin, forKey: key)
    }

    /// Local / LAN addresses: only while debugging (a build pointed at `make dev`). The navigation lock lets these
    /// through and sends everything else to Safari, or one tap on a link in an agent's reply would replace the App
    /// with someone else's page.
    static func isLocalHost(_ host: String?) -> Bool {
        guard let host, !host.isEmpty else { return false }
        if host == "localhost" || host.hasSuffix(".local") { return true }
        let p = host.split(separator: ".").compactMap { Int($0) }
        guard p.count == 4 else { return false }
        return p[0] == 10 || (p[0] == 172 && (16...31).contains(p[1])) || (p[0] == 192 && p[1] == 168)
            || isTailnet(host)
    }

    /// A tailnet address (Tailscale's 100.64/10): the Mac's LAN port also answers there, through the phone's VPN
    /// from anywhere. LanRoute must not pin these to Wi-Fi: the VPN is its own interface, and Wi-Fi goes around it.
    static func isTailnet(_ host: String) -> Bool {
        let p = host.split(separator: ".").compactMap { Int($0) }
        return p.count == 4 && p[0] == 100 && (64...127).contains(p[1])
    }

    /// A global IPv6 address (2000::/3): the Mac's LAN port also answers there from anywhere with IPv6, when its
    /// router lets inbound IPv6 through. Like a tailnet address, LanRoute doesn't pin it to Wi-Fi: out of the house
    /// it is reached over cellular. Not in isLocalHost, which is also the navigation lock.
    static func isGlobalV6(_ host: String) -> Bool {
        let h = host.trimmingCharacters(in: CharacterSet(charactersIn: "[]"))
        guard h.contains(":"), let a = IPv6Address(h) else { return false }
        return a.rawValue[0] & 0xE0 == 0x20
    }
}
