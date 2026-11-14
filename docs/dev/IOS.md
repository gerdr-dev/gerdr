# iOS App: a thin native shell around WKWebView

**In one line**: `ios/` is the iOS counterpart of `android/` (docs/dev/ANDROID.md): a WKWebView loading your own
Gerdr's remote address, the UI reused whole, the shell only doing what the page can't. This first version is the
thin shell, passkey sign-in and LAN routing (iOS 17+); background notifications, widgets and self-update are not in
it yet (see "Not done yet" for what each would take on iOS).

```
ios/
  project.yml           XcodeGen spec; Gerdr.xcodeproj and Gerdr/Info.plist are generated, not in git
  Config.xcconfig       bundle id + team, overridden by Local.xcconfig (not in git; copy Local.xcconfig.example)
  Gerdr/
    App.swift           AppDelegate / SceneDelegate / RootViewController (address form ↔ web page)
    SetupViewController.swift   the address form
    WebViewController.swift     WKWebView, navigation lock, bridge, loading / error covers, downloads, login overlay
    Login.swift         the device flow's HTTP (passkey through Safari)
    LanRoute.swift      LAN routing: the in-App CONNECT proxy, probing, network watching (iOS 17+)
    Server.swift        parsing / saving the address, origin format, local hosts
    Css.swift           the page's CSS colours (rgb / oklch / #hex) → UIColor
    Look.swift          native screens' colours (same values as index.css's tokens) and controls; L(zh, en)
UI side: ui/src/lib/android.ts (isIOSApp / isApp), components/AndroidSettings.tsx (IOSSettings),
         internal/server/pairpage.html (`droid` also takes gerdrIOS)
```

## Building and installing

Needs Xcode (the Command Line Tools alone can't build for iOS) and `brew install xcodegen`.

1. `cp ios/Local.xcconfig.example ios/Local.xcconfig`, fill in your team ID and a bundle id of your own.
   A free Apple ID works: its personal team can install on your own iPhone, but the signature lasts **7 days**,
   after which the App won't open until you Run it from Xcode again. A personal team also can't take a bundle id
   anyone else registered, hence the setting.
2. `make ios` (generates the project, opens Xcode), pick your iPhone, Run. The first time, the iPhone asks you to
   trust the developer: Settings → General → VPN & Device Management. iOS 16+ also wants Developer Mode on
   (Settings → Privacy & Security).

`make ios-typecheck` type-checks the Swift without Xcode, against the Mac Catalyst UIKit / WebKit / SafariServices
that ship inside the Command Line Tools' macOS SDK. It catches API and type mistakes, not iOS-only availability or
runtime behaviour; build in Xcode before trusting a change.

### Simulator, from the command line

What was used to test it (an Intel Mac on macOS 15; any Mac with Xcode works the same):

- Xcode: the newest one the macOS allows (xcodereleases.com lists `requires`; macOS 15 stops at 26.3). Xcode 26
  comes as an "Apple silicon" and a "Universal" download with the same version and build: an Intel Mac needs
  Universal (the other gives "Bad CPU type in executable"); `xcodes install` picks whichever comes first in its list.
- Xcode 26 builds nothing for iOS, not even with `-sdk iphonesimulator`, until the platform is installed:
  `xcodebuild -downloadPlatform iOS` (~10 GB, no Apple ID).
- `cd ios && xcodegen generate`, then `xcrun simctl create … ; xcodebuild -scheme Gerdr -destination id=<udid>
  CODE_SIGNING_ALLOWED=NO build`, `simctl install / launch`, `simctl io <udid> screenshot`. The address can be set
  without typing: `simctl spawn <udid> defaults write io.github.zbysir.gerdr origin https://…` (a `/?pair=CODE` link
  works there too). Logs: `simctl spawn <udid> log show --predicate 'eventMessage CONTAINS "LanRoute"'`.
- The downloaded iOS 26.3 Simulator runtime has no PingFang (only Hiragino): native screens still draw Chinese, but
  WebKit draws it as boxes. A Simulator quirk; a phone has the font.
## The bridge

Same message format as Android (JSON strings dispatched on `t`, table in docs/dev/ANDROID.md "桥"), so the page has
one code path: `ui/src/lib/android.ts` takes `window.gerdrIOS` when there is no `gerdrAndroid`. `isAndroid()` now
means Android only (keys, LAN, notifications, update); `isApp()` is either App, `isIOSApp()` iOS.

- A `WKUserScript` at document start defines `gerdrIOS` (postMessage + onmessage) **only when `location.origin` is
  the server's** and only in the main frame. `window.webkit.messageHandlers.gerdr` itself is visible to every frame
  of any page, so the handler checks again: main frame, and `frameInfo.securityOrigin` equals the server's origin.
  Agents' output is rendered in this page; nothing else may reach the shell.
- Shell → page is `evaluateJavaScript("gerdrIOS.onmessage({data: <JSON string>})")`, only after the page's hello
  and only while the web view is on the server's origin (reset on every commit).
- Messages iOS answers: `hello` (reply: `platform: "ios"`, `webauthn: false`, `scan: false`, `copyImage: true`,
  `lan` = it routes over the LAN), `theme`, `open`, `setup`, `login`, `copyImage`, `lan`; it sends `route`. Everything else is ignored, as Android ignores what it doesn't know.

## Passkey: through Safari, not in the WKWebView

WKWebView can only run WebAuthn for a domain listed in the App's **Associated Domains** entitlement
(`webcredentials:<domain>`, confirmed by the domain's `apple-app-site-association`). Two problems:

- A free Apple ID's personal team can't have that entitlement at all.
- It is baked into the signed App: a general App can't list every user's own domain. (Android has no such
  limit: assetlinks.json is checked against the signing certificate, and Gerdr serves it for its own domain.)

So the shell always says `webauthn: false`, and the pairing page's passkey button hands the job to it, through the
device flow Android already uses as its fallback (`internal/auth/cliapprove.go`, `server/appapi.go`, no server
change): the App starts a request, shows the code (already copied), and opens `/?cli` in an
**SFSafariViewController** as a half-height sheet so the code stays readable above it. That is real Safari, so the
domain's passkeys (iCloud Keychain etc.) work there: sign in there if needed, paste the code, approve with the
passkey. A sign-in with a passkey a moment before counts as the approval's step-up, so it's one Face ID if Safari
wasn't signed in yet. The App polls, gets the device token and writes it as the `gerdr_dev` cookie into the
WKWebView's cookie store (host-only, HttpOnly, SameSite=Strict, Secure, as the server's IssueCookie), then tells the
page `login{state:"done"}` and the page goes in.

- SFSafariViewController has its own cookie jar (not Safari's since iOS 11), kept across launches: the first time
  you'll sign in there with the passkey; it then stays signed in like any browser device.
- Re-verification (`reauth`) starts with the existing cookie, the server renews that device; if the cookie is no
  longer accepted (and the server was reachable), it falls back to a new device, like Android.
- The rules in Login.swift's comment (code typed by hand, never in the URL; Cookie header, not Bearer; `X-Gerdr`)
  are the same as Android's.

Settings → Remote access → passkeys (adding one, step-up) runs in the page and will fail inside the iOS App for the
same reason; do it in Safari. With a paid developer account the in-App path could be added later: an
`apple-app-site-association` route next to `assetLinks` listing `<TeamID>.<bundle id>` from remote.json, the
entitlement for one's own domain in Local.xcconfig, and hello saying `webauthn: true` for that build.

## LAN routing: same origin, another route (iOS 17+)

Android's design unchanged (docs/dev/ANDROID.md "局域网直连", the three rules in LanRoute.swift's comment); only the
plumbing differs:

- iOS 17 added `WKWebsiteDataStore.proxyConfigurations`: the data store sends **only the server's domain**
  (`matchDomains`) to an HTTP CONNECT proxy on 127.0.0.1 in the App (an `NWListener`). The proxy accepts CONNECTs to
  exactly that host:port (403 otherwise) and dials the LAN port once a probe passed, the domain otherwise. WebKit
  does TLS end to end and checks the certificate against the domain, so cookies, passkeys and the Host whitelist
  are untouched. `allowFailover`: if the proxy is gone, WebKit connects directly, i.e. the relay.
- Probe: `NWConnection` with TLS, SNI = the domain, and a verify block evaluating the chain with
  `SecPolicyCreateSSL(true, domain)`, so a valid certificate for another name fails. LAN probes and dials are pinned
  to Wi-Fi (`requiredInterfaceType`), Android's dual-network lesson; tailnet addresses (100.64/10) are not, the VPN
  is its own interface (ANDROID.md "Over a tailnet").
- Before the first load the proxy probes last time's candidates (cached per host in UserDefaults, ≤1.5 s), so the
  page and the terminal's WebSocket start on the LAN at home. The page reports fresh candidates after hello and when
  it becomes visible; `NWPathMonitor` changes and coming to the foreground re-probe (drop first); relaying on Wi-Fi
  retries every 20 s.
- The first probe makes iOS ask for **Local Network** access (NSLocalNetworkUsageDescription). Denied, or the macOS
  firewall on the Mac blocking port 7878: no error anywhere, the App simply stays on the relay.
- A suspended App can lose its listening socket: the listener restarts and the WebView gets the new port (`onProxy`).
- Before iOS 17, or on an IP / http address, there's no proxy: everything goes the usual way and hello says `lan:
  false`, so the page hides the LAN / relay label.

Tested without an iPhone: LanRoute.swift only needs Network / Security / Foundation, so a small macOS program built
around the same file, with `curl --proxy` / `openssl s_client -proxy` in WebKit's place, checked against a real Gerdr:
relay before candidates; LAN after the probe (curl verified the domain's certificate on the LAN port); other hosts
refused; candidates with the wrong certificate (the router, port 7870) rejected; an idle relay TLS session cut 0.1 s
after the probe passed; a second launch probing the cached candidate before the first request. What that can't cover
is WebKit itself honouring the proxy configuration (WebSocket included); check the label on a device.

**The iOS Simulator can't show it.** There, any `proxyConfigurations` fails before connecting with NSURLErrorBadURL
(-1000, stream error 22 = EINVAL), in WebKit and in a plain URLSession alike, with every variant tried (`127.0.0.1` as a
name or `.ipv4(.loopback)`, with or without `matchDomains`); the proxy never sees a CONNECT. The same code in a macOS
process works on both the Apple Silicon and the Intel Mac (macOS 26 and 15). With `allowFailover` the App just goes
through the relay there; the probe itself does run in the Simulator (the log shows `LanRoute: LAN … → …`).

## Other shell duties

- **Safe area**: the UI never pads for notches / the home indicator (no `env(safe-area-inset-*)`), so the web view
  is pinned inside the safe area and the strips around it take the page's background from `theme`. `theme` also
  sets the window's light / dark style, so the status bar and native sheets match the page, not the phone.
- **Navigation lock**: main-frame navigations stay on the server's origin (and local hosts, for debugging); anything
  else, `target=_blank` and `window.open` go to Safari / the system. iframes load freely (the bridge ignores them).
- **TLS**: default handling only, no challenge override. Never accept a certificate unconditionally: this page drives
  agents with full access to the computer.
- **Downloads**: files sit behind the device cookie only this WebView has, so they download here (`WKDownload`, on
  `?download=1` / `Content-Disposition: attachment` or what WebKit can't show) and the file goes to the share sheet
  (Save to Files, AirDrop…). Opening in Safari would be a 401.
- **File picking**: WKWebView's own picker for `<input type=file>`; Info.plist has the camera / microphone usage
  strings, without which iOS kills the App when "Take Photo" is tapped.
- **alert / confirm / prompt**: implemented with UIAlertController; WKWebView's default is to show nothing and
  answer "no".
- **Web process killed** in the background (memory pressure): reload.
- The UA gets `GerdrApp/<version>`, so the device list says "iPhone · App" (`auth.LabelFromUA`).
- Debug builds set `isInspectable`: Safari → Develop → your iPhone shows the page's console.

## Not done yet

- **Background notifications**: iOS doesn't let an App keep long-polling in the background; it needs APNs pushes,
  i.e. a paid account and a push relay between each Gerdr and Apple. Local notifications while the App is open work
  without that.
- **Widgets, self-update**: a widget needs an App Group and a background refresh; updating is reinstalling from Xcode
  (or TestFlight with a paid account).
- **Scanning the pairing QR in the App**: hello says `scan: false`; the Camera app's scanner opens Safari instead.
