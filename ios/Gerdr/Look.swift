import UIKit

/// Text the shell itself shows, zh or en by the phone's first language (the page has its own i18n).
func L(_ zh: String, _ en: String) -> String {
    (Locale.preferredLanguages.first ?? "en").hasPrefix("zh") ? zh : en
}

/// The App's version (CFBundleShortVersionString), sent in the hello reply and the user agent.
let appVersion = Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "0"

/// The domain this build carries an Associated Domains webcredentials entitlement for (Local.xcconfig
/// GERDR_PASSKEY_DOMAIN → Info.plist), lowercased; "" = none. Only for that host can the WKWebView use passkeys.
let passkeyDomain = (Bundle.main.object(forInfoDictionaryKey: "GerdrPasskeyDomain") as? String ?? "").lowercased()

/// Colors of the native screens (setup, loading, login overlay): the same values as ui/src/index.css's tokens and
/// pairpage.html, light and dark, like android/app/src/main/res/values(-night)/colors.xml. Change them together.
enum Look {
    private static func pair(_ light: UInt32, _ dark: UInt32) -> UIColor {
        UIColor { $0.userInterfaceStyle == .dark ? hex(dark) : hex(light) }
    }

    static func hex(_ v: UInt32) -> UIColor {
        UIColor(red: CGFloat((v >> 16) & 0xFF) / 255, green: CGFloat((v >> 8) & 0xFF) / 255, blue: CGFloat(v & 0xFF) / 255, alpha: 1)
    }

    static let bg = pair(0xFCFCFC, 0x0E0E0E)
    static let card = pair(0xFFFFFF, 0x161616)
    static let fg = pair(0x161616, 0xE8E8E8)
    static let muted = pair(0x696969, 0x929292)
    static let line = pair(0xE1E1E1, 0x2B2B2B)
    static let ctl = pair(0xF0F0F0, 0x212121)
    static let brand = pair(0x337BD0, 0x5AA3EC)
    static let brandFg = pair(0xF8F8F8, 0x161616)
    static let bad = pair(0xE23439, 0xF75D59)

    static func label(_ text: String, size: CGFloat, color: UIColor = fg, weight: UIFont.Weight = .regular) -> UILabel {
        let l = UILabel()
        l.text = text
        l.font = .systemFont(ofSize: size, weight: weight)
        l.textColor = color
        l.numberOfLines = 0
        return l
    }

    static func button(_ title: String, primary: Bool = false, action: @escaping () -> Void) -> UIButton {
        var c = primary ? UIButton.Configuration.filled() : UIButton.Configuration.bordered()
        c.title = title
        c.baseBackgroundColor = primary ? brand : ctl
        c.baseForegroundColor = primary ? brandFg : fg
        c.cornerStyle = .medium
        c.contentInsets = NSDirectionalEdgeInsets(top: 10, leading: 14, bottom: 10, trailing: 14)
        let b = UIButton(configuration: c, primaryAction: UIAction { _ in action() })
        return b
    }

    /// A rounded card like the pairing page's.
    static func card(_ views: [UIView], spacing: CGFloat = 12) -> UIView {
        let stack = UIStackView(arrangedSubviews: views)
        stack.axis = .vertical
        stack.spacing = spacing
        stack.translatesAutoresizingMaskIntoConstraints = false
        let box = UIView()
        box.backgroundColor = card
        box.layer.cornerRadius = 14
        box.layer.borderWidth = 1
        box.layer.borderColor = line.cgColor
        box.addSubview(stack)
        NSLayoutConstraint.activate([
            stack.topAnchor.constraint(equalTo: box.topAnchor, constant: 22),
            stack.bottomAnchor.constraint(equalTo: box.bottomAnchor, constant: -22),
            stack.leadingAnchor.constraint(equalTo: box.leadingAnchor, constant: 22),
            stack.trailingAnchor.constraint(equalTo: box.trailingAnchor, constant: -22),
        ])
        return box
    }

    /// Put a view centered in [parent], at most 400pt wide (the pairing page's column).
    static func center(_ v: UIView, in parent: UIView) {
        v.translatesAutoresizingMaskIntoConstraints = false
        parent.addSubview(v)
        let g = parent.safeAreaLayoutGuide
        let w = v.widthAnchor.constraint(equalTo: g.widthAnchor, constant: -32)
        w.priority = .defaultHigh
        // Not required: a screen with a text field pushes it up above the keyboard
        let y = v.centerYAnchor.constraint(equalTo: g.centerYAnchor)
        y.priority = .defaultHigh
        NSLayoutConstraint.activate([
            v.centerXAnchor.constraint(equalTo: g.centerXAnchor),
            y,
            v.topAnchor.constraint(greaterThanOrEqualTo: g.topAnchor, constant: 16),
            v.widthAnchor.constraint(lessThanOrEqualToConstant: 400),
            w,
        ])
    }
}
