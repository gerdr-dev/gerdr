import UIKit

@main
final class AppDelegate: UIResponder, UIApplicationDelegate {
    func application(_ application: UIApplication, configurationForConnecting s: UISceneSession, options: UIScene.ConnectionOptions) -> UISceneConfiguration {
        UISceneConfiguration(name: "Default", sessionRole: s.role)
    }
}

final class SceneDelegate: UIResponder, UIWindowSceneDelegate {
    var window: UIWindow?

    func scene(_ scene: UIScene, willConnectTo session: UISceneSession, options: UIScene.ConnectionOptions) {
        guard let ws = scene as? UIWindowScene else { return }
        let w = UIWindow(windowScene: ws)
        w.backgroundColor = Look.bg
        w.rootViewController = RootViewController()
        w.makeKeyAndVisible()
        window = w
    }
}

/// Holds the one screen on show: the address form or the web page. Swapping children rather than presenting keeps
/// the web view's lifetime obvious (one at a time, dropped when you switch servers).
final class RootViewController: UIViewController {
    private var current: UIViewController?

    override func viewDidLoad() {
        super.viewDidLoad()
        view.backgroundColor = Look.bg
        if let u = Server.load() { showWeb(u) } else { showSetup(nil) }
    }

    override var childForStatusBarStyle: UIViewController? { current }
    override var childForHomeIndicatorAutoHidden: UIViewController? { current }

    func showSetup(_ error: String?) {
        // The page's theme no longer applies once it's gone: back to following the system
        view.window?.overrideUserInterfaceStyle = .unspecified
        swap(SetupViewController(root: self, prefill: Server.load()?.host, error: error))
    }

    /// start: what to load first (a pairing link keeps its `?pair=`); the saved address is only the origin.
    func showWeb(_ server: URL, start: URL? = nil) {
        swap(WebViewController(root: self, server: server, start: start ?? server))
    }

    private func swap(_ vc: UIViewController) {
        if let old = current {
            old.willMove(toParent: nil)
            old.view.removeFromSuperview()
            old.removeFromParent()
        }
        addChild(vc)
        vc.view.frame = view.bounds
        vc.view.autoresizingMask = [.flexibleWidth, .flexibleHeight]
        view.addSubview(vc.view)
        vc.didMove(toParent: self)
        current = vc
        setNeedsStatusBarAppearanceUpdate()
    }
}
