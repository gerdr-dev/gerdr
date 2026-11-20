# 安卓 App：原生壳 + WebView

**一句话**：`android/` 是一个很薄的原生壳，里面一个 WebView 加载你自己那台 Gerdr 的远程地址（设置 → 远程访问里那个
`https://…`）。界面整份复用网页；壳只做网页做不到的事：**局域网直连**、App 里刷 passkey、后台通知、返回键 / 音量键 /
文件选择这些系统集成。

照搬 herdr-web 的 `app/android`（它的 docs/dev/NATIVE-APP.md 和 RELAY.md §4 是完整的设计理由），这里只写 Gerdr 的部分
和搬过来时要对齐的地方。为什么不是浏览器 / PWA：浏览器不让网页决定「这个域名连到哪个 IP」，局域网直连只能换 origin，
那一串坑（自签证书、交接凭据、IP 上用不了 passkey）herdr-web 走过、最后下掉了。

```
android/app/src/main/java/io/github/zbysir/gerdr/
  MainActivity.kt   填地址一屏、WebView、加载层、导航锁、返回键、音量键、passkey 退路的浮层
  WindowActivity.kt more windows, one project each (see "Windows")
  Look.kt           原生那几屏的控件样式（照配对页 pairpage.html 的卡片画）；颜色在 res/values(-night)/colors.xml，
                    和 index.css 的 token 对齐、跟系统明暗走 —— 改 token 时一起改
  Bridge.kt         页面 ↔ 壳 的那条线（另一半 ui/src/lib/android.ts）
  LanRoute.kt       局域网换路：本机 CONNECT 代理 + 探测
  NativeNet.kt      add-ons' g.socket / g.fetch from the phone itself (bridge `ws` / `http`), not through the computer
  Login.kt          passkey 的退路：device flow
  NoticeService.kt  后台通知：前台服务长轮询 /api/app/*
  Agents.kt         会话一览挑哪几行、什么颜色（常驻通知和桌面小部件共用）
  AgentsWidget.kt   桌面小部件
服务端：internal/lan、internal/remote/lan.go（直连口）、internal/server/appapi.go（assetlinks、device flow）、
        internal/server/appwatch.go（通知的长轮询）、internal/auth/cliapprove.go
界面：ui/src/lib/android.ts、components/AndroidSettings.tsx、components/CliApprove.tsx、internal/server/pairpage.html 里那段
```

编：`make apk`（正式包，`~/.android-release/gerdr.jks` 签）/ `make apk-debug`。要 Android SDK（`android/local.properties`
的 `sdk.dir`，这个文件不入库）和 JDK 17–21（AGP 不认更新的 JDK）。**正式包和调试包签名不同，不能互相覆盖安装**，换过去要先卸载。

发版在 GitHub 上打（`.github/workflows/release.yml`，推 `v主.次.修` 的 tag）：钥匙放在仓库 Secrets 的
`GERDR_APP_KEYSTORE_B64`（`base64 -i gerdr.jks`）和 `GERDR_APP_KEY_PASS`，打完拿签名指纹和 `OfficialAndroidCert` 对一下，对不上就失败。
The apk is only rebuilt when android/ changed since the version the latest `android.json` names; otherwise that
`android.json` is republished as is, so the App stays on (and downloads from) that version (docs/dev/UPDATE.md,
"Incremental releases"). So a change the apk needs must live under android/.

## 局域网直连：换路，不换 origin

```
WebView ──CONNECT gerdr.example.com:443──▶ App 内的代理（127.0.0.1:随机口）
                                             ├─ 在家：拨 192.168.1.5:7878（gerdr 的局域网直连口）
                                             └─ 不在：照常拨 gerdr.example.com:443（走中继）
          ◀───────────── TLS 从 WebView 一路到 Mac，代理只看到密文 ─────────────▶
```

- **直连口和中继那条路同一张证书、同一个 handler、同一个「远程」章**（`remote.startLan`，h 是 `server.RemoteHandler` 包过的）。
  WebView 按域名验证书，Host 还是那个域名，所以 remoteGuard 的 Host 白名单、设备 cookie、passkey 全部原样适用，
  **不因为在局域网里就放宽任何东西**。
- 对端必须在本地网络里（`lan.PeerIsLocal`），否则 403：绑通配地址只是拓扑假设，端口转发 / UPnP / 全局 IPv6 都能让公网连上来。
  CGNAT（Tailscale 那段）本身不算本地，见下面「Over a tailnet」；global IPv6 gets in only on our own address, see "Over IPv6".
- 默认端口 7878（`GERDR_LAN_PORT` 或 remote.json 的 `lanPort`，0 = 不开）。起不来只记在 `Status.LanErr`，App 探不通就留在中继上。
  第一次起来时 macOS 防火墙可能问「是否允许传入连接」—— 要允许，不然手机探不通，而且没有任何报错。
- 候选地址每次现报（`GET /api/remote/lan`，页面拿去发给壳）：局域网 IP 会变，配死一个地址的表现是「某天开始不直连了」。
  只报私网地址上的真网卡（虚拟网卡手机碰不到，白占一个探测超时）。
- 壳那边的三条（`LanRoute.kt` 的注释里有细节）：只在**完整 TLS 握手 + 验域名**通过之后才往局域网拨；网络一变先作废再重探；
  探通的那一刻把走中继的连接全部掐掉（不然 /ws 那条长连接一直挂在中继上）。小米这类「双网」机型要把 socket 绑到 Wi-Fi 上。
- 浏览器不走这条。

### Over a tailnet (Tailscale)

With Tailscale on both the Mac and the phone, the same LAN port serves the phone from anywhere, without the relay:
one more route, same origin, same certificate and credentials. On by default; `"lanTailnet": false` in remote.json or
`GERDR_LAN_TAILNET=0` turns it off.

- **Candidates**: `lan.Origins` also reports the Mac's tailnet address (100.64/10 on a tunnel interface: utunN,
  tailscale0, "Tailscale"), after the LAN ones. At home the LAN one wins; elsewhere only the tailnet one answers.
- **The gate** (`lan.ViaTailnet`): a 100.64/10 peer gets in only when it reached one of **our own tailnet addresses**
  (the connection's local address, `http.LocalAddrContextKey`). Trusting 100.64/10 peers as such would let in anyone
  sharing a carrier's CGNAT range; they arrive on the en / cellular address, if at all, and stay out.
- **The shells must not pin tailnet addresses to Wi-Fi** (`Server.isTailnet` in both): a socket bound to the Wi-Fi
  network (Android `bindSocket`, iOS `requiredInterfaceType`) goes around the VPN and never reaches 100.x. Android
  Apps older than this change still use it on cellular (no Wi-Fi to bind to), not on Wi-Fi; older iOS Apps never.
- macOS may ask once whether to allow incoming connections, same as for the LAN.
- In direct access (docs/dev/REMOTE.md "Direct access") the LAN port is the way in and has no gate at all; the
  tailnet route above is what's left of Tailscale support beside the relay.

### Over IPv6

Home broadband in China (and many other places) gives the Mac global IPv6 addresses with no NAT, and phones on
cellular usually have IPv6 too. When the router lets inbound IPv6 through, the same LAN port serves the phone from
anywhere, without the relay and without Tailscale. On by default; `"lanIPv6": false` in remote.json or
`GERDR_LAN_IPV6=0` turns it off. Measured on 2026-11-15: a China Telecom ONT passed inbound IPv6 to :7878 as is,
and a phone on 5G reached it.

- **Candidates**: `lan.Origins` reports every global unicast IPv6 address on a real NIC, last (`https://[240e:…]:7878`).
  Go can't tell macOS's temporary (privacy) addresses from the stable ones, so they come along; they keep accepting
  connections until they expire, and candidates are reported fresh each time. The /64 prefix changes when the line
  redials; same answer.
- **The gate** (`lan.ViaIPv6`): an IPv6 peer gets in when it reached one of **our own global IPv6 addresses** (the
  connection's local address). That address is on the internet, so this is as open as the relay path, and guarded
  the same way: device cookie, passkeys, Host whitelist. The local-address check keeps it to that: IPv4 through a
  port forward and ULA / bridge addresses stay behind the 403 and its alert.
- **Not pinned to Wi-Fi** (`Server.isGlobalV6` in both shells), same as tailnet: out of the house it is reached over
  cellular, and a café's Wi-Fi is often IPv4 only. Not in `isLocalHost` either: that is also the navigation lock.
  Apps from before this drop IPv6 candidates (they only took local addresses) and stay on the relay.
- **The router may drop inbound IPv6** (many ONTs have an "IPv6 firewall" on by default): then the probe fails and
  the App stays on the relay, nothing to report. Test from a phone on cellular: open `https://[<address>]:7878/` in
  a browser; a certificate warning or this port's own 403 page means it got through, a timeout means the router.
- **Probes run in parallel** (`probeAll` / `probeFirst`), first one through wins. One by one, every candidate that
  can't answer (the LAN address out of the house, IPv6 behind a firewall) cost 1.5 s before the next, and the first
  probe runs before the page loads.

## passkey：在 App 里自己刷

- 壳给 WebView 开 `WebSettingsCompat.setWebAuthenticationSupport(WEB_AUTHENTICATION_SUPPORT_FOR_APP)`，页面上原有的
  `navigator.credentials`（配对页的「用 passkey 登录」、设置里的加 passkey / step-up）就走系统的 Credential Manager，**网页代码不用改**。
- 前提是域名认这个 App：系统会去取 `https://<域名>/.well-known/assetlinks.json`，看里面有没有这个包名 + 签名证书。
  一个通用 App 本来绑不了每个用户自己的域名（herdr-web 因此只能开浏览器）；**Gerdr 自己就是这个域名的服务器**，自己发这份文件就行
  （`server.assetLinks`，没认证也给）。
- 这时 clientDataJSON 里的 origin 不是 `https://域名`，而是 `android:apk-key-hash:<签名证书 SHA-256 的 base64url>`，
  所以服务端的 RPOrigins 里要有它（`remote.Open` 按 `AndroidCerts` 加，`auth.AndroidOrigins` 算；规范说 base64url，实际小米上收到的是标准 base64，所以几种编码都认）。
- 认哪些证书：`config.OfficialAndroidCert`（正式包那把，写死在代码里）+ `GERDR_ANDROID_CERTS` / remote.json 的 `androidCerts`
  （自己编的调试包要用 passkey 时加 debug 钥匙的指纹：`keytool -list -v -keystore ~/.android/debug.keystore -storepass android`）。
  **换正式钥匙要同步改 OfficialAndroidCert**，不然新包刷不了 passkey、只能走下面的退路；而且已经装上的人要卸了重装。
  钥匙和密码文件（`~/.android-release/gerdr.{jks,pass}`）一定要备份。
- passkey 是按 RPID（域名）存的：在手机浏览器里给这个域名注册过的 passkey，App 里也能用（同一个凭据提供方）。

**退路**（WebView 太旧没有这个功能、或者手机上没有凭据提供方 —— 没有 Google 服务又低于 Android 14）：壳在 hello 回信里报
`webauthn: false`，配对页的按钮改成交给壳走 device flow（`auth/cliapprove.go`）：App 显示一个 8 位码、打开浏览器里的
`/?cli`，人手输码、看清是哪台设备、批准（远程那条路上要当场刷 passkey；在电脑上「设置 → 远程访问 → 批准 App 登录」输码也行，
不用 passkey —— 本机口凭令牌本来就能出配对码，批准给的能力一样）。App 轮询领到设备凭据、写进 WebView 的 cookie。
两条别破：码**手输**，不从 URL 带进批准页（防钓鱼）；发起要求至少注册过一把 passkey（不然远程没人批得了，直接说用配对码）。

## App key: keep the device and re-verify with a fingerprint

Like the iOS App (docs/dev/IOS.md "App key", same server endpoints and rules, `internal/auth/appkey.go`), the App
keeps a P-256 key in the Android Keystore (StrongBox when there is one), made and registered when the person turns it on
after a pairing code (the page's "keep this device?" prompt or Settings sends `enroll`) or silently after a browser
approval (`MainActivity.enrollAppKey`), each signature behind a
strong biometric through the platform `BiometricPrompt` (not androidx's: MainActivity is a ComponentActivity),
voided when a new fingerprint is enrolled. hello says `appKey` (strong biometric set up, Android 10+); the pairing
page's "verify with Face ID" then sends `appkey`, the shell answers `appkey {state}`.

Since 2026-11-16 this is used whether or not the WebView can do passkeys. Before, phones with passkeys in the
WebView (most of them) skipped it, so a phone paired with a code was temporary until the person remembered to add a
passkey within 30 minutes; now pairing in the App keeps the device by itself on both platforms, and passkeys are
optional (for signing in from browsers / other devices without a code).

## 后台通知

App 关着 / 锁屏时，一个前台服务（`NoticeService`）挂在两个长轮询上，**不经过任何第三方**（国内机型多半没有 Google 服务，FCM 收不到还不报错）：

- `GET /api/app/notices?since=&wait=50`：「等你」「跑完了」「出错了」，口径和界面上的系统通知（App.tsx 的 useAttention）一样，
  但在服务端算（`appwatch.go` 订阅 hub）。不带 since 的第一拍只对底不回提示 —— 不然重启手机、App 被杀后拉起都会把旧提示全弹一遍。
- `GET /api/app/agents?ver=&wait=50`：所有会话的状态一览，常驻通知和桌面小部件照它画。
- 为什么不用 /ws：/ws 推的东西多（一帧几 KB），后台走中继一直收白费流量和电；而且 /ws 算「远程界面开着」，会让 Mac 不休眠。
- 凭据就是 WebView 里那份 `gerdr_dev` cookie，后台请求自己带 `Cookie:` 头（远程那条路不认 Bearer）。
- 点通知带着会话 id，页面 hello 之后壳发 `focus`，页面切到那个会话。App 在前台时不弹（网页自己有提示）。
- 小米 HyperOS 那几个坑（频道级别、RemoteViews 按 view id 认控件）见 NoticeService.kt / widget 布局里的注释。

## Staying connected in the background (Labs)

By default the page stops in the background: `web.onPause()`, and the WebView waives its renderer's priority when it
isn't visible, so Android (HyperOS readily) freezes the renderer even while NoticeService keeps the App's own process
alive. /ws then delivers nothing, and on coming back the page catches up (sync.ts `wake` → `refresh`: a new
connection opened beside the old one, swapped in once open, so nothing reads "reconnecting"; still a moment of stale
chat). With Labs → "stay connected in the background" on, `MainActivity.setKeepConnected` skips `web.onPause()` and
sets `setRendererPriorityPolicy(IMPORTANT, waivedWhenNotVisible = false)`: the renderer shares the App's importance,
the page keeps handling /ws, and it is current when it comes back. Off by default, for the reasons in "后台通知" above
(/ws traffic in the background, and a connected remote page keeps the Mac awake). Without notifications on, nothing
keeps the App's process alive and the setting does little.

## Downloads

Files (`/api/fs/raw`, `/api/blobs`) sit behind the same device cookie, which only the WebView has. So a download
(`setDownloadListener`) must not go to the browser: it gets a 401 ("not authorized"). `MainActivity.download` hands
it to `DownloadManager` with that cookie copied into a `Cookie:` header, for our own origin only, into the shared
Downloads folder (an App-private folder on Android 9, which would need a storage permission). DownloadManager names the
file from `Content-Disposition` and ignores `<a download>`, so the UI always asks with `?download=1` (an attachment
header). It doesn't go through LanRoute, so it always takes the public address.

The WebView has no PDF viewer: an `<iframe>` of a PDF becomes a download the moment it loads. FileViewer checks
`navigator.pdfViewerEnabled` and shows a download button instead.

## Microphone

The page may record (getUserMedia: voice input in an add-on, 2026-11); the camera is still refused. Until then every
WebView permission request was denied, since voice input went through the keyboard; the user's phone keyboards
couldn't make out technical words ("agent"), so an add-on records and sends the audio to a speech-to-text API with the
conversation's words as hints. `onPermissionRequest` grants only `RESOURCE_AUDIO_CAPTURE`, only for the page's own
host, and asks for Android's RECORD_AUDIO the first time (the page's request waits on that dialog). The manifest also needs
MODIFY_AUDIO_SETTINGS (a normal permission, no dialog): without it WebView's capture fails with NotReadableError
"Could not start audio source" even with RECORD_AUDIO granted.
While the page records, `MicFocus` holds audio focus (it watches the App's own recordings, AudioRecordingCallback): the
WebView records in communication mode but holds no focus, so with a Bluetooth headset another app's music came back
~2s into a press and the rest of the recording was silent.
VIBRATE (normal) is there for `navigator.vibrate`, which WebView silently ignores without it; a buzz felt on a long
press without it is the system's own long-press feedback, which goes away once the page cancels the touch's defaults. The WebView pauses
the page in the background, so recording and playback only work with the App in front.

## Self-update

The App updates itself straight from GitHub (`AppUpdate.kt`), not through the Gerdr it's connected to. The release's
android job writes `android.json` (version, file, size, sha256) next to the APK, and the App reads
`releases/latest/download/android.json` when it comes to the front, at most every 6 h (debug builds never). A newer
version is downloaded into the cache by itself and checked against the sha256; Settings and a toast offer "安装".

- Installing always takes a tap: Android only installs a sideloaded app once the person allows "install unknown apps"
  for Gerdr (the App sends them there the first time) and confirms the installer's dialog. On Android 12+ the session
  asks for no confirmation (`setRequireUserAction`), which the system may grant once the App installed itself before;
  MIUI / HyperOS tend to ask anyway.
- Android refuses an APK signed with another key, so a tampered download can't get in; the sha256 catches truncation.
- Versions compare as numbers (`0.2.10` > `0.2.9`); a local build is `0.1.0`, behind every release.

## Windows

Like the desktop App, the App can have one window per project, each its own card in the recents list, so working on
two projects is switching apps instead of switching projects inside one page. "Open in new window" on a project (the
project menu / a chip's long press) starts `WindowActivity`, a subclass of MainActivity with
`documentLaunchMode="always"`: every start is a new task. It loads `/<project id>`, the address the desktop shells give
a new window (ui sync.ts `wantSpace`); in split screen it opens in the other half (`FLAG_ACTIVITY_LAUNCH_ADJACENT`).

- The launcher, notifications and the widget still go to the main window (MainActivity stays singleTask).
- Each window has its own WebView, page, /ws and bridge; the cookie jar, the LAN proxy (`LanRoute`, process-wide
  WebView proxy override) and the notice service are shared. So process-wide callbacks are listener sets
  (`LanRoute.listeners`, `AppUpdate.listeners`), not one `onChange` that the last window would take over, and
  `MainActivity.visible` counts resumed windows (two windows' onPause / onResume interleave).
- `LanRoute.install` for the server already installed returns at once: installing again set `lan = null` and pushed the
  open windows' new connections to the relay until the probe came back.
- Swiping a window's card away destroys it (`autoRemoveFromRecents`). After the process is killed, the system relaunches
  a window's task with its original intent, so it reopens on the project it was opened with.

## Soft keyboard: the composer follows it

Android lets the WebView know the keyboard's final height up front, so resizing the WebView to it made the composer
jump up while the keyboard was still sliding in; native apps move along with it, frame by frame. Shell
(`MainActivity.onCreate`: the insets listener and a `WindowInsetsAnimationCompat.Callback`) and page
(`ui/src/lib/imeLift.ts`) split it:

- **The WebView resizes once per slide**: at the end when the keyboard comes up, at the start when it goes down (the
  room under it is covered by the keyboard either way). In between the page lifts the focused chat by
  a transform, `h - pad`. Resizing the WebView on every frame was tried first and dropped frames: each one relays the
  whole page out.
- **The switch must land in one frame.** The page takes a new `pad` in the visual viewport's resize event, the same
  one the page's height is written in (`useViewportHeight`); the window's resize came a frame apart, and the chat
  jumped by the keyboard's height for that frame.
- **Round the lift like the page's height**, `round(vv) - round(vv - (h - pad))`, not `round(h - pad)`: 316.6 CSS px
  rounded to 317 while the page lost 316, and the composer settled 1px high and dropped when the resize landed.
- **Keep the lifted box's layer** (`will-change` stays when the lift ends): dropping it in the resize frame left the
  composer without its box and buttons for one frame. Same cause as TAILWIND.md "one bad frame when an animation ends".
- **A chat scrolled up lifts only its composer** (with the page's colour behind and below it, or the list shows
  through): a resize keeps such a list's top in place, so a list lifted with it fell back. A chat at its end goes up whole.
- **A short chat goes up only past its empty space**: the list moves by `max(0, lift - slack)`, slack being the room
  under its rows (measured from the rows' box `[data-chat-list]`: `scrollHeight` is never under `clientHeight`, so
  measuring with it read every short chat as full), and the composer goes the rest. Lifted whole, a short chat's
  messages went up under the bar and dropped back when the resize landed, on every keyboard move.
- **Only the chat follows the keyboard.** The terminal did too, and flashed the same way when short; where xterm puts
  its lines after the resize depends on the cursor row and whole cells, so it was taken out (one-step change, like a
  dialog's input) instead of modelled.
- **The resize frame has to be cheap**, or the composer stalls under the keyboard going down (that resize is at the
  start, while everything moves). In a long chat it was 140–220ms; two things restyled every element: the `short`
  variant's `max-height` media query flipping as the keyboard crossed 560px (now landscape only), and the page's height
  written as `--vvh` on html (any custom property change on html restyles the whole page in Chromium, registered
  non-inherited or not: Gerdr now uses `useViewportHeight({ height: true })`). Measure with
  `tools/scrolltest/grow.mjs`; on the phone, the perf probe add-on's `loaf` records.

How these were found: logcat timestamps from the shell's callbacks, and the phone's screen recording pulled frame by
frame against them (`adb shell screenrecord`, then every frame with its timestamp, TAILWIND.md). Guessing from code was
wrong twice.

## 桥

The iOS App (ios/, docs/dev/IOS.md) speaks the same format as `window.gerdrIOS` and answers a subset of the table
below (hello, theme, open, setup, login, copyImage, lan, route, ws, http); keep the two shells in step when a message changes.

页面 ↔ 壳 是 JSON 字符串、按 `t` 分派；JS 对象 `gerdrAndroid`，壳用 `addWebMessageListener` **只对服务器那个 origin 注入**
（不用 `addJavascriptInterface`：页面里渲染着 agent 的输出）。两边对不上是完全静默的。

| `t` | 方向 | 干什么 |
|---|---|---|
| `hello` | 页 → 壳 → 页 | 页面起来发一次；回 `platform` / `version` / `webauthn`。壳收到它才有回信的通道 |
| `back` | 壳 → 页 → 壳 | 返回键：页面收最上面一层（弹窗 → 抽屉 → 键盘），回 `handled`；没东西收壳把 App 退到后台 |
| `theme` | 页 → 壳 | 系统栏底色（`rgb()` / `#hex` / `oklch()` 都认）和图标深浅 |
| `lan` | 页 → 壳 | 局域网直连的候选 `origins` |
| `route` | 壳 → 页 | 开着的连接各走哪条路（顶栏的点：绿 = 直连、蓝 = 中继） |
| `login` | 页 → 壳 → 页 | passkey 退路：`reauth`；回 `state`（done / error / cancel）+ `msg` |
| `enroll` | 页 → 壳 | Make and register the App key now: sent when the person chooses to keep the device (KeepDevice.tsx, Settings → Remote access) while whoami says `appKeyFresh`, so re-verifying is a fingerprint rather than the browser. No reply: the page polls whoami for `appKey`, then sends `appkey` once so the person sees the fingerprint prompt. The server refuses it outside that window |
| `appkey` | 页 → 壳 → 页 | Re-verify with the App key (fingerprint; "App key" above); answers `state` (done / error / cancel) + `msg`. Offered only when hello says `appKey` and whoami says the device has one |
| `notify` | 页 ↔ 壳 | 后台通知的开关 / 权限 / 电池优化 / 试一下 |
| `volkeys` / `key` | 页 ↔ 壳 | 音量键当 ↑ / ↓（等于在焦点会话的输入框里按方向键：翻历史、提问时选选项） |
| `arrows` | 页 → 壳 | Whether ↑ / ↓ have a use right now (soft keyboard up, a dropdown open, a question waiting in the focused session: `syncArrows`). Off = the volume keys stay volume keys. Never sent (an older page) = always mapped, the old behaviour |
| `copyImage` | 页 → 壳 → 页 | Long-press "copy image": base64 `data` + `mime` (WebView's clipboard API can't write images); answers `ok` / `error`. The hello reply carries `copyImage: true` so the menu hides it on older Apps |
| `keyboard` | 壳 → 页 | `hard`: a hardware keyboard is attached (`Configuration.hardKeyboardHidden`), sent after hello and on every plug in / out (`onConfigurationChanged`; the manifest's configChanges keeps the Activity alive). Menus show shortcut keys only with one (`ui/src/lib/hwKeyboard.ts`). The hello reply carries `keyboard: true`; older Apps fall back to the page's own guess |
| `ime` | 页 → 壳 → 页 | The soft keyboard as it slides ("Soft keyboard" above). The page's hello says `ime: true` (it follows the keyboard itself); then on every frame of a slide the shell sends `h` (the keyboard's height now) and `pad` (how much of it the WebView's padding holds), CSS px, nav bar taken out. Without it (an older page, Android < 11) the WebView resizes in one step as before |
| `focus` | 壳 → 页 | 点通知进来：切到那个会话 |
| `keepConnected` | 页 → 壳 | Labs "stay connected in the background" (config `keepConnected`, per device): `on`. Sent after each hello reply and on every change, only when the hello reply says `keepConnected: true`; the shell keeps it for the next start ("Staying connected in the background" above) |
| `newWindow` | 页 → 壳 | "Open in new window" on a project (SpaceSwitcher): `space` = project id. Shown only when the hello reply says `windows: true` (`canNewWindow` in native.ts) |
| `space` | 页 → 壳 | The project this window shows (`id`, `name`): the window's name in the recents list. Sent on every switch, also before hello |
| `open` / `setup` / `fullscreen` | 页 → 壳 | 外部链接交给系统浏览器 / 回到填地址那屏 / 藏系统栏 |
| `ws` | 页 ↔ 壳 | An add-on's `g.socket` made by the phone (`NativeNet.kt`, OkHttp; `ui/src/lib/appnet.ts`). Page → shell: `op` open (`id`, `url`, `headers`, `protocols`) / send (`id`, `text` or base64 `b64`) / close (`id`, `code`, `reason`). Shell → page: `id` + `ev` open (`protocol`) / message (`text` or `b64`) / error (`msg`, `unreachable`) / close (`code`, `reason`). `unreachable`: no answer to the handshake, so the page opens the same socket through the backend instead. Offered when the hello reply says `net: true` |
| `http` | 页 → 壳 → 页 | An add-on's `g.fetch` made by the phone: the backend's `/api/addons/{id}/fetch` request and answer shapes plus `id`, or `error` + `unreachable` (nothing of the request went out: the page retries through the backend). Same limits as the backend: 30s, 10 MB, http(s) only |
