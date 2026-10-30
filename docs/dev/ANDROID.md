# 安卓 App：原生壳 + WebView

**一句话**：`android/` 是一个很薄的原生壳，里面一个 WebView 加载你自己那台 gerdr 的远程地址（设置 → 远程访问里那个
`https://…`）。界面整份复用网页；壳只做网页做不到的事：**局域网直连**、App 里刷 passkey、后台通知、返回键 / 音量键 /
文件选择这些系统集成。

照搬 herdr-web 的 `app/android`（它的 docs/dev/NATIVE-APP.md 和 RELAY.md §4 是完整的设计理由），这里只写 gerdr 的部分
和搬过来时要对齐的地方。为什么不是浏览器 / PWA：浏览器不让网页决定「这个域名连到哪个 IP」，局域网直连只能换 origin，
那一串坑（自签证书、交接凭据、IP 上用不了 passkey）herdr-web 走过、最后下掉了。

```
android/app/src/main/java/io/github/zbysir/gerdr/
  MainActivity.kt   填地址一屏、WebView、加载层、导航锁、返回键、音量键、passkey 退路的浮层
  Look.kt           原生那几屏的控件样式（照配对页 pairpage.html 的卡片画）；颜色在 res/values(-night)/colors.xml，
                    和 index.css 的 token 对齐、跟系统明暗走 —— 改 token 时一起改
  Bridge.kt         页面 ↔ 壳 的那条线（另一半 ui/src/lib/android.ts）
  LanRoute.kt       局域网换路：本机 CONNECT 代理 + 探测
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
  CGNAT（Tailscale 那段）不算本地。
- 默认端口 7878（`GERDR_LAN_PORT` 或 remote.json 的 `lanPort`，0 = 不开）。起不来只记在 `Status.LanErr`，App 探不通就留在中继上。
  第一次起来时 macOS 防火墙可能问「是否允许传入连接」—— 要允许，不然手机探不通，而且没有任何报错。
- 候选地址每次现报（`GET /api/remote/lan`，页面拿去发给壳）：局域网 IP 会变，配死一个地址的表现是「某天开始不直连了」。
  只报私网地址上的真网卡（虚拟网卡手机碰不到，白占一个探测超时）。
- 壳那边的三条（`LanRoute.kt` 的注释里有细节）：只在**完整 TLS 握手 + 验域名**通过之后才往局域网拨；网络一变先作废再重探；
  探通的那一刻把走中继的连接全部掐掉（不然 /ws 那条长连接一直挂在中继上）。小米这类「双网」机型要把 socket 绑到 Wi-Fi 上。
- 浏览器不走这条。

## passkey：在 App 里自己刷

- 壳给 WebView 开 `WebSettingsCompat.setWebAuthenticationSupport(WEB_AUTHENTICATION_SUPPORT_FOR_APP)`，页面上原有的
  `navigator.credentials`（配对页的「用 passkey 登录」、设置里的加 passkey / step-up）就走系统的 Credential Manager，**网页代码不用改**。
- 前提是域名认这个 App：系统会去取 `https://<域名>/.well-known/assetlinks.json`，看里面有没有这个包名 + 签名证书。
  一个通用 App 本来绑不了每个用户自己的域名（herdr-web 因此只能开浏览器）；**gerdr 自己就是这个域名的服务器**，自己发这份文件就行
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

## 后台通知

App 关着 / 锁屏时，一个前台服务（`NoticeService`）挂在两个长轮询上，**不经过任何第三方**（国内机型多半没有 Google 服务，FCM 收不到还不报错）：

- `GET /api/app/notices?since=&wait=50`：「等你」「跑完了」「出错了」，口径和界面上的系统通知（App.tsx 的 useAttention）一样，
  但在服务端算（`appwatch.go` 订阅 hub）。不带 since 的第一拍只对底不回提示 —— 不然重启手机、App 被杀后拉起都会把旧提示全弹一遍。
- `GET /api/app/agents?ver=&wait=50`：所有会话的状态一览，常驻通知和桌面小部件照它画。
- 为什么不用 /ws：/ws 推的东西多（一帧几 KB），后台走中继一直收白费流量和电；而且 /ws 算「远程界面开着」，会让 Mac 不休眠。
- 凭据就是 WebView 里那份 `gerdr_dev` cookie，后台请求自己带 `Cookie:` 头（远程那条路不认 Bearer）。
- 点通知带着会话 id，页面 hello 之后壳发 `focus`，页面切到那个会话。App 在前台时不弹（网页自己有提示）。
- 小米 HyperOS 那几个坑（频道级别、RemoteViews 按 view id 认控件）见 NoticeService.kt / widget 布局里的注释。

## 桥

页面 ↔ 壳 是 JSON 字符串、按 `t` 分派；JS 对象 `gerdrAndroid`，壳用 `addWebMessageListener` **只对服务器那个 origin 注入**
（不用 `addJavascriptInterface`：页面里渲染着 agent 的输出）。两边对不上是完全静默的。

| `t` | 方向 | 干什么 |
|---|---|---|
| `hello` | 页 → 壳 → 页 | 页面起来发一次；回 `platform` / `version` / `webauthn`。壳收到它才有回信的通道 |
| `back` | 壳 → 页 → 壳 | 返回键：页面收最上面一层（弹窗 → 面板一览 → 抽屉 → 键盘），回 `handled`；没东西收壳把 App 退到后台 |
| `theme` | 页 → 壳 | 系统栏底色（`rgb()` / `#hex` / `oklch()` 都认）和图标深浅 |
| `lan` | 页 → 壳 | 局域网直连的候选 `origins` |
| `route` | 壳 → 页 | 开着的连接各走哪条路（顶栏的点：绿 = 直连、蓝 = 中继） |
| `login` | 页 → 壳 → 页 | passkey 退路：`reauth`；回 `state`（done / error / cancel）+ `msg` |
| `notify` | 页 ↔ 壳 | 后台通知的开关 / 权限 / 电池优化 / 试一下 |
| `volkeys` / `key` | 页 ↔ 壳 | 音量键当 ↑ / ↓（等于在焦点会话的输入框里按方向键：翻历史、提问时选选项） |
| `focus` | 壳 → 页 | 点通知进来：切到那个会话 |
| `open` / `setup` / `fullscreen` | 页 → 壳 | 外部链接交给系统浏览器 / 回到填地址那屏 / 藏系统栏 |
