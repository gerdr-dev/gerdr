# 和 herdr 比

<sub>[English](vs-herdr.md)</sub>

Gerdr 是天天用 [herdr](https://github.com/herdrdev/herdr) 用出来的：分屏、tab、工作空间和快捷键照搬，herdr 里的布局能直接导进来。
两者差在站的那一层：**herdr 管的是 agent 的终端，Gerdr 直接跟 agent 本身说话。**

| | herdr | Gerdr |
|---|---|---|
| 是什么 | 给 agent 用的终端复用器（Rust TUI） | agent 的图形界面（Go 服务 + 网页界面，桌面外壳用你已有的 Chrome） |
| 在哪用 | 你现有的终端里，SSH 上也行 | 桌面 App、任意浏览器、安卓 App |
| 图片 / 文件 | 在终端里看不了，要另开 | 图片、视频、PDF 在对话里直接看，点路径就打开文件 |
| 支持哪些 agent | 能在终端里跑的都行（Claude Code、Codex、Cursor、OpenCode……） | Claude Code、Codex、任意 ACP agent、内置 Gerdr Agent |
| 审批 / 提问 | 在 agent 自己的 TUI 里回 | 对话里的卡片，手机上、[HTTP 接口](../API.md)也能回 |
| 中途说话 | 往 TUI 里打字 | 插进正在跑的那一轮，等这一步做完或立即插 |
| 撤销 | 看 agent 自己有什么 | 回到任何一条消息之前（只退对话，不动文件） |
| 布局 | pane、tab、工作空间、前缀键 | 同一套结构和键位，能从 herdr 导入 |
| 普通 shell | 每个 pane 都是 | 终端 pane，和会话并排 |
| agent 指挥 agent | `herdr agent …` 命令 + socket 接口 | `gerdr session …` 命令 + 内置 skill |
| 远程 | 存好的 SSH 机器放在一个窗口里 | 中继（端到端 TLS）、配对码、passkey；安卓局域网直连 |
| 定制 | 插件 | TS 扩展（带商店），整个界面也是能热更新的源码 |
| 体量 | 一个二进制，没有 Electron | 一个 Go 二进制；桌面 App 多一层约 18MB 的外壳（用你已有的 Chrome） |

**用 herdr**：常待在终端里、要走 SSH，或者要用的 agent Gerdr 没有适配。
**用 Gerdr**：想在界面上点审批、回退，想在手机上看 agent 跑得怎么样，或者想改界面本身。
两个也能一起用：把 herdr 的布局导进 Gerdr，其余的照样在 herdr 里跑。同一个会话别两边同时开着，两边写同一份记录会分叉，先在一边退出。

## 为什么不是 herdr-web

[herdr-web](https://github.com/zbysir/herdr-web) 把 herdr 搬进浏览器和手机，Gerdr 就是从它长出来的。卡住它的是终端本身：
在手机上操作终端，提交一句话、敲一条命令都不自然。要在手机上好用，只能往终端里发按键、再读屏把话抽出来，这是很脆的补丁 ——
agent 的 TUI 加个新选项、改个交互，原来发的按键可能就完全不管用了。

Gerdr 换了条路：不经过终端，直接用 agent 的结构化接口说话，界面自己画。审批、提问、回退都是接口调用，
agent 升级了也不会因为屏幕长得不一样就坏。
