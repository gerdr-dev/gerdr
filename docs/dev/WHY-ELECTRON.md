# 为什么桌面 App 用 Electron

**结论：桌面外壳是 Electron（`desktop/`），自带 Chromium。** 不用系统 WebView（原来的 mac 外壳就是
WKWebView）、不用 Tauri / Wails、不用 mygo，也不用 nsh。

> 2026-10-28 定的。下面关于 mygo / nsh 的说法都是那时的文档写的，它们变了结论可能跟着变 —— 文末列了什么时候该重看。

## 1. 原来的问题：同一份界面，要在两个内核上对齐

gerdr 的界面是网页（`ui/`），浏览器、手机、桌面 App 都用这一份。原来的 mac 外壳是 WKWebView，也就是 Safari 的内核；
开发时验的是 Chrome。于是「在 Chrome 里好好的，到 App 里坏了」成了常态，而且每一个都是用户先撞上的：

- 弹窗（网格 + 限高）里的滚动区被 WebKit 压成一条缝：导入弹窗、`/resume` 弹窗，前后报了三次；
- 藏起来的 tab 里滚动位置被清零（`visibility:hidden` 的滚动容器，Chrome 不会），只能自己记、自己恢复；
- 弹窗居中用 `translate(-50%)` 在 WebKit 里发糊；
- 量界面进程内存要靠 WKWebView 的私有属性拿 pid。

Windows 上要出 App 的话，系统 WebView 是 WebView2（Chromium），**mac 和 Windows 会是两个内核**，这类问题只会翻倍。

## 2. 几条路

| | 内核 | 界面要不要重写 | 结论 |
|---|---|---|---|
| WKWebView（原来的） | mac 上 WebKit | 不用 | 就是问题本身 |
| Tauri / Wails | 系统 WebView：mac 上还是 WebKit | 不用 | 换汤不换药 |
| mygo 网页模式 | 同上（WKWebView / WebView2 / WebKitGTK） | 不用 | 同上 |
| mygo 原生 UI | 自己用 GPU 画，没有网页 | **整个重写**（Go） | 见 §3 |
| nsh | 各平台原生控件 | **整个重写**（TSX，原生控件） | 见 §4 |
| **Electron** | **自带 Chromium，各平台同一个** | 不用 | **选它** |

## 3. 为什么不是 mygo

[mygo](https://github.com/egoist/mygo)（MIT，纯 Go、不用 cgo）有两种界面：

- **网页模式**用的是系统 WebView —— mac 上还是 WKWebView，§1 那些问题一个都不会少。
- **原生 UI 模式**（`ui` 包，GPU 自己画，没有 HTML / JS / webview，开窗快、省内存）是真能把 WebView 拿掉的那条路，
  但代价是把界面整个用 Go 重写，而 gerdr 界面最值钱的几样都建在「界面是网页」上：
  1. **agent 能改界面**：内置的 `gerdr-ui` skill 靠的是「界面是一目录 React 源码、保存即热替换」（`internal/uisite`），
     用户的改动靠 git 三方合并保住（`internal/webui`）。换成编译进二进制的 Go 界面，这一套没了。
  2. **浏览器 / 手机能打开同一份界面**（本机浏览器、远程访问那条路）。原生 UI 只有桌面，网页那份还得留着 —— 两套界面。
  3. **TS 插件**（`~/.gerdr/plugins`）跑在页面里。
  4. **对话里那些现成的东西**：Markdown（含表格、代码高亮）、diff、跨消息选中复制、长列表虚拟化。原生 UI 里，
     流式长 Markdown + 自由选字恰恰是最难做好的部分。
- 另外它还年轻（要求 Go 1.27.3+），桌面 API 和打包在 Electron 那边是十年的积累。

## 4. 为什么不是 nsh

[nsh](https://nsh.dev/) 用各平台原生控件画 TSX 模块，瞄准「AI 生成 / 下发的界面」。herdr-web 评估过一次
（那边的 `docs/dev/WHY-NOT-NSH.md`），落到 gerdr 上：

- **没有 WebSocket**（只有 `fetch`）：gerdr 的实时同步走 `/ws`；
- 同样是**整个重写**（§3 那四条一样丢）；
- 它适合的是「一小块界面」，不是一整个重度聊天应用。和「agent 生成界面」的思路很契合，适合以后做手机端的局部界面，
  不适合换掉主界面。

## 5. Electron 的代价，和为什么能接受

- **包大**：App 约 330MB、安装包约 120MB（大头是 Chromium）。
- **内存多 100–200MB**。对照一下：gerdr 里每个 Claude Code 会话本身就是一个 200–400MB 的进程，这点开销不是瓶颈。
- 换来的：**在 Chrome 里验过的，到 App 里就是一样的**（mac / Windows 同一个内核）；界面、浏览器访问、插件、agent 改界面一样不丢；
  外壳本身只有一个 JS 文件（`desktop/main.js`），Go 后端和界面一行不用动。

## 6. 外壳做什么、怎么做（会静默出错的几条）

外壳只做网页做不了的事（`desktop/main.js`）：起 / 收后端、通知（点了跳到那个会话）、Dock 角标、菜单、外部链接交给默认浏览器、
页面进程崩了自动重载、窗口位置记忆、告诉网页它自己的渲染进程号（设置 →「资源」量内存用）。网页那边的桥是
`ui/src/lib/native.ts`（认 `window.gerdrNative`，`desktop/preload.js` 挂的）。

- **后端怎么起**：`gerdr --app --parent-pid <壳的 pid>`，读 stdout 里那行 `GERDR_READY <url>`。已经有一个 gerdr 在跑（同一个数据目录有锁）
  就直接连它、后端进程正常退出（code 0）—— 这不算出错，别弹「后端退出了」。
- **退出**：Unix 上发 SIGTERM、最多等 3 秒；后端关机本身 < 0.1 秒（`⌘Q 卡 4 秒`那次修过：界面热更新的 SSE 会让 `http.Server.Shutdown` 干等，
  所以关机先取消所有请求的 context）。**Windows 没有 SIGTERM**（`kill` 就是硬杀，会话日志来不及写），所以不发信号，
  让后端看到父进程没了自己退 —— 而 **Windows 上父进程死了 ppid 不会变**（Unix 会过继给 init），`waitParent` 在 Windows 上
  要拿父进程句柄等（`cmd/gerdr/platform_windows.go`），照 Unix 那样比 ppid 的话后端永远退不掉。
- **菜单**：mac 上必须有「编辑」菜单（⌘C / ⌘V / ⌘A / ⌘Z 走它）。**Windows 上不挂菜单**：菜单的加速键会先于网页吃掉
  Ctrl+C / Ctrl+W，而网页自己有一套快捷键（Ctrl+C 两次关 agent、前缀键……）；复制粘贴 Chromium 自己处理。
- **签名**：没配证书时 electron-builder 不签（`identity: null`），而 Apple 芯片上没签名的程序起不来 —— `desktop/build.sh` 补一个 ad-hoc 签名。
  别人第一次打开要右键「打开」；正式发布配 `CSC_LINK` / `CSC_KEY_PASSWORD` 走正经签名 + 公证。
- **mac 通用包**：后端先 `lipo` 成一个通用二进制，Electron `--universal` 合并两份 App 时，两边一模一样的 Mach-O 要在
  `x64ArchFiles` 里点名（`desktop/package.json`），不然合并报错。
- **Windows 包在 mac 上就能打**（`make win`，nsis，不需要 wine）。

## 7. 什么时候该重看

- 真正的瓶颈变成了启动速度 / 内存（而不是界面行为不一致）；
- 想在手机上要原生体验：先看 nsh 做**局部**界面，而不是替换主界面；
- mygo 的原生 UI 有了能直接渲染 Markdown + 跨段选字的组件，而且 gerdr 愿意放弃「agent 改界面」这条线（不太可能）。
