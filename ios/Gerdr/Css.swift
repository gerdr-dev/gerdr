import UIKit

/// The CSS color the page reports (bridge `theme`) as a UIColor. getComputedStyle gives `rgb(26, 26, 31)`, but
/// gerdr's tokens are written in `oklch(…)` and WebKit may hand those back as written. Not recognising one means the
/// area outside the safe area stays the wrong color with no error. Port of android/…/Css.kt.
enum Css {
    static func color(_ s: String) -> UIColor? {
        let v = s.trimmingCharacters(in: .whitespaces).lowercased()
        if v.hasPrefix("#") {
            var h = String(v.dropFirst())
            if h.count == 3 { h = h.map { "\($0)\($0)" }.joined() }
            guard h.count >= 6, let n = UInt32(h.prefix(6), radix: 16) else { return nil }
            return Look.hex(n)
        }
        guard let open = v.firstIndex(of: "("), v.hasSuffix(")") else { return nil }
        let fn = String(v[..<open])
        let inner = v[v.index(after: open)..<v.index(before: v.endIndex)]
        // Commas, spaces and a slash (alpha) in any mix; alpha is ignored
        let parts = inner.split(whereSeparator: { $0 == "," || $0 == " " || $0 == "/" }).map(String.init)
        guard parts.count >= 3 else { return nil }
        switch fn {
        case "rgb", "rgba":
            guard let r = num(parts[0], 255), let g = num(parts[1], 255), let b = num(parts[2], 255) else { return nil }
            return UIColor(red: clamp(r / 255), green: clamp(g / 255), blue: clamp(b / 255), alpha: 1)
        case "oklch":
            guard let l = num(parts[0], 1), let c = num(parts[1], 0.4) else { return nil }
            let h = Double(parts[2].replacingOccurrences(of: "deg", with: "")) ?? 0 // `none` = 0
            return oklch(l, c, h)
        default:
            return nil
        }
    }

    /// A number or a percentage of [full] (oklch's L 100% = 1, C 100% = 0.4, per CSS Color 4).
    private static func num(_ p: String, _ full: Double) -> Double? {
        if p.hasSuffix("%") { return Double(p.dropLast()).map { $0 / 100 * full } }
        return Double(p)
    }

    private static func clamp(_ x: Double) -> CGFloat { CGFloat(min(max(x, 0), 1)) }

    /// OKLCH → sRGB (Björn Ottosson's OKLab), out-of-gamut clamped.
    private static func oklch(_ l: Double, _ c: Double, _ hDeg: Double) -> UIColor {
        let h = hDeg * .pi / 180
        let a = c * cos(h), b = c * sin(h)
        let l1 = pow(l + 0.3963377774 * a + 0.2158037573 * b, 3)
        let m1 = pow(l - 0.1055613458 * a - 0.0638541728 * b, 3)
        let s1 = pow(l - 0.0894841775 * a - 1.2914855480 * b, 3)
        let r = 4.0767416621 * l1 - 3.3077115913 * m1 + 0.2309699292 * s1
        let g = -1.2684380046 * l1 + 2.6097574011 * m1 - 0.3413193965 * s1
        let bl = -0.0041960863 * l1 - 0.7034186147 * m1 + 1.7076147010 * s1
        return UIColor(red: clamp(gamma(r)), green: clamp(gamma(g)), blue: clamp(gamma(bl)), alpha: 1)
    }

    private static func gamma(_ x: Double) -> Double {
        x <= 0.0031308 ? 12.92 * x : 1.055 * pow(x, 1 / 2.4) - 0.055
    }
}
