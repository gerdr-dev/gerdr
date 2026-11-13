import UIKit

/// The first screen: which Gerdr to connect to. Looks like the pairing page (pairpage.html), which comes next.
final class SetupViewController: UIViewController, UITextFieldDelegate {
    private weak var root: RootViewController?
    private let prefill: String?
    private let initialError: String?
    private let field = UITextField()
    private let err = Look.label("", size: 13, color: Look.bad)

    init(root: RootViewController, prefill: String?, error: String?) {
        self.root = root
        self.prefill = prefill
        self.initialError = error
        super.init(nibName: nil, bundle: nil)
    }

    required init?(coder: NSCoder) { fatalError() }

    override func viewDidLoad() {
        super.viewDidLoad()
        view.backgroundColor = Look.bg

        let title = Look.label(L("连接你的 Gerdr", "Connect to your Gerdr"), size: 17, weight: .semibold)
        let lead = Look.label(
            L("填它的远程访问地址：在跑 Gerdr 的电脑上打开「设置 → 远程访问」，上面那个 https:// 开头的地址。配对链接也可以直接粘进来。",
              "Enter its remote-access address: on the computer running Gerdr, open Settings → Remote access and copy the https:// address. A pairing link works too."),
            size: 13, color: Look.muted)

        field.text = prefill
        field.placeholder = "gerdr.example.com"
        field.keyboardType = .URL
        field.textContentType = .URL
        field.autocapitalizationType = .none
        field.autocorrectionType = .no
        field.spellCheckingType = .no
        field.returnKeyType = .go
        field.clearButtonMode = .whileEditing
        field.font = .monospacedSystemFont(ofSize: 16, weight: .regular)
        field.textColor = Look.fg
        field.backgroundColor = Look.bg
        field.layer.cornerRadius = 8
        field.layer.borderWidth = 1
        field.layer.borderColor = Look.line.cgColor
        field.leftView = UIView(frame: CGRect(x: 0, y: 0, width: 10, height: 1))
        field.leftViewMode = .always
        field.heightAnchor.constraint(equalToConstant: 44).isActive = true
        field.delegate = self

        let go = Look.button(L("连接", "Connect"), primary: true) { [weak self] in self?.connect() }
        err.text = initialError

        let card = Look.card([title, lead, field, go, err])
        Look.center(card, in: view)
        card.bottomAnchor.constraint(lessThanOrEqualTo: view.keyboardLayoutGuide.topAnchor, constant: -16).isActive = true
    }

    override func viewDidAppear(_ animated: Bool) {
        super.viewDidAppear(animated)
        field.becomeFirstResponder()
    }

    override func traitCollectionDidChange(_ previous: UITraitCollection?) {
        super.traitCollectionDidChange(previous)
        // CGColor doesn't follow dark mode by itself
        field.layer.borderColor = Look.line.cgColor
    }

    func textFieldShouldReturn(_ textField: UITextField) -> Bool {
        connect()
        return false
    }

    private func connect() {
        guard let u = Server.parse(field.text ?? "") else {
            err.text = L("这不像一个地址", "That doesn't look like an address")
            return
        }
        // Remote access is always https (ACME certificates); plain http is only for a local debug instance
        if u.scheme == "http" && !Server.isLocalHost(u.host) {
            err.text = L("远程访问的地址是 https:// 开头的", "A remote-access address starts with https://")
            return
        }
        let origin = Server.origin(u)
        Server.save(origin)
        root?.showWeb(URL(string: origin)!, start: u)
    }
}
