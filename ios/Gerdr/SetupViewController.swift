import UIKit

/// The first screen: which Gerdr to connect to. Looks like the pairing page (pairpage.html), which comes next.
final class SetupViewController: UIViewController, UITextFieldDelegate {
    private let onConnect: (URL) -> Void
    private let field = UITextField()
    private let err = Look.label("", size: 13, color: Look.bad)

    init(connect: @escaping (URL) -> Void) {
        self.onConnect = connect
        super.init(nibName: nil, bundle: nil)
    }

    required init?(coder: NSCoder) { fatalError() }

    override func viewDidLoad() {
        super.viewDidLoad()
        view.backgroundColor = Look.bg

        let heading = L("添加服务器", "Add server")
        title = heading
        let lead = Look.label(
            L("填它的远程访问地址：在跑 Gerdr 的电脑上打开「设置 → 远程访问」，上面那个 https:// 开头的地址。配对链接也可以直接粘进来。",
              "Enter its remote-access address: on the computer running Gerdr, open Settings → Remote access and copy the https:// address. A pairing link works too."),
            size: 13, color: Look.muted)

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
        let header = Look.label(heading, size: 17, weight: .semibold)
        let card = Look.card(navigationController == nil ? [header, lead, field, go, err] : [lead, field, go, err])
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
        onConnect(u)
    }
}

final class ServerPickerViewController: UITableViewController {
    private let servers = Server.recent()
    private let active = Server.load().map(Server.origin)
    private let onConnect: (URL) -> Void

    init(connect: @escaping (URL) -> Void) {
        self.onConnect = connect
        super.init(style: .insetGrouped)
    }

    required init?(coder: NSCoder) { fatalError() }

    override func viewDidLoad() {
        super.viewDidLoad()
        title = L("服务器", "Servers")
        navigationItem.leftBarButtonItem = UIBarButtonItem(systemItem: .cancel, primaryAction: UIAction { [weak self] _ in
            self?.dismiss(animated: true)
        })
        tableView.backgroundColor = Look.bg
        tableView.register(UITableViewCell.self, forCellReuseIdentifier: "server")
    }

    override func tableView(_ tableView: UITableView, numberOfRowsInSection section: Int) -> Int {
        servers.count + 1
    }

    override func tableView(_ tableView: UITableView, cellForRowAt indexPath: IndexPath) -> UITableViewCell {
        let cell = tableView.dequeueReusableCell(withIdentifier: "server", for: indexPath)
        var content = cell.defaultContentConfiguration()
        if indexPath.row == servers.count {
            content.text = L("添加服务器", "Add server")
            content.textProperties.color = Look.brand
            content.image = UIImage(systemName: "plus")
            content.imageProperties.tintColor = Look.brand
            cell.accessoryType = .disclosureIndicator
        } else {
            let server = servers[indexPath.row]
            content.text = server
            content.secondaryText = server == active ? L("当前服务器", "Current server") : nil
            cell.accessoryType = server == active ? .checkmark : .none
        }
        cell.backgroundColor = Look.card
        cell.contentConfiguration = content
        return cell
    }

    override func tableView(_ tableView: UITableView, didSelectRowAt indexPath: IndexPath) {
        tableView.deselectRow(at: indexPath, animated: true)
        if indexPath.row == servers.count {
            let form = SetupViewController { [weak self] url in self?.choose(url) }
            navigationController?.pushViewController(form, animated: true)
        } else if servers[indexPath.row] == active {
            dismiss(animated: true)
        } else if let url = URL(string: servers[indexPath.row]) {
            choose(url)
        }
    }

    private func choose(_ url: URL) {
        let connect = onConnect
        dismiss(animated: true) { connect(url) }
    }
}
