# gerdr

<p align="center">
  <img src="ui/public/icon.svg" alt="gerdr" width="100" />
</p>

<p align="center">
  <a href="#装">安装</a> · <a href="#用">使用</a> · <a href="#和-herdr-比">和 herdr 比</a> · <a href="docs/API.md">HTTP 接口</a> · <a href="docs/dev/ARCHITECTURE.md">架构</a>
</p>

<p align="center">
  <a href="README.md">English</a> · 简体中文
</p>

<p align="center">
  <a href="https://github.com/gerdr-dev/gerdr/releases/latest"><img src="https://img.shields.io/github/v/release/gerdr-dev/gerdr?label=release&labelColor=333333&color=666666" alt="最新版本" /></a>
  <a href="https://github.com/gerdr-dev/gerdr/releases"><img src="https://img.shields.io/github/downloads/gerdr-dev/gerdr/total?labelColor=333333&color=666666" alt="下载量" /></a>
  <a href="https://github.com/gerdr-dev/gerdr/stargazers"><img src="https://img.shields.io/github/stars/gerdr-dev/gerdr?labelColor=333333&color=666666&logo=github" alt="GitHub stars" /></a>
</p>

---

**一个窗口，同时管一群 coding agent。**

和 [herdr](https://github.com/herdrdev/herdr) 一样：分屏、tab、工作空间，一个键把 pane 放大，还能分成几个互相隔离的 session；哪个 agent 卡住在等你，一眼就看到，一个键跳过去；
agent 之间还能互相对话：开新会话、派活、等回复、读回复。

不习惯终端？gerdr 是另一个同样好的选择，而且是图形界面：agent 给的图片、视频、PDF 直接在对话里看，点一下路径就打开文件；
出门了用手机远程接着看、接着管。

**对话**

- **中途插话**：agent 干活时直接说，消息插进正在跑的那一轮。
- **审批 / 提问是卡片**：批命令、批改文件、回答选择题，点一下就行。
- **回退**：回到任何一条消息之前，原话放回输入框，改了再发。
- **接着以前的聊**：终端里开过的 Claude Code / Codex 会话，选目录就能接上。

**agent**

- **不止 Claude 和 Codex**：多账号并排，接任意 ACP agent，还有直接调 GPT / DeepSeek 接口的 Gerdr Agent。
- **看得见后台**：在跑的后台命令和子 agent，能停、能看完整对话。

**工作区**

- **终端**：`ctrl+b t` 在旁边开一个，刷新页面、换到手机上都还在。
- **从 herdr 搬过来**：快捷键一样；工作空间、tab、分屏原样导入，每个 agent 接上它的会话。

**DIY 自己的 Gerdr**

- **界面让 agent 改**：整个界面都可以让 agent 改，添加按钮、快捷键、显示内容，直接跟 AI 说就行。无需发版、无需部署，即刻生效；改坏了有改不坏的急救面板。
- **TS 插件**：改 tab 名、pane 标题，加命令和快捷键。
- **skill / MCP**：用 agent 自己的、gerdr 的，或两者都用。

**在哪用**

- 桌面 App（mac / Windows）、任意浏览器；经中继远程访问（配对码 + passkey）；安卓 App 能局域网直连、收后台通知。

## 装

需要先装好、登录好你要用的 agent：

```bash
curl -fsSL https://claude.ai/install.sh | bash   # Claude Code，然后跑一次 claude 登录
npm i -g @openai/codex                           # Codex，然后 codex login
```

**mac App**：下载 `Gerdr-x.y.z-mac-arm64.dmg`（Apple 芯片：M1 及以后）或 `Gerdr-x.y.z-mac-x64.dmg`（Intel），拖进「应用程序」。第一次打开如果提示「无法验证开发者」，
在 Finder 里右键 → 打开（或 `xattr -dr com.apple.quarantine /Applications/Gerdr.app`）。

**安卓**：下载 `Gerdr-x.y.z-android.apk`，连的是你自己那台 gerdr（要先配好下面的「远程访问」）。

**Windows**：发版里还没有，先自己 `make win` 打安装包（没签名，SmartScreen 拦的话点「更多信息 → 仍要运行」）。

上面这些都在 [releases](https://github.com/gerdr-dev/gerdr/releases/latest)。

**从源码**（Go 1.24+、Node 20+、pnpm；打桌面 App 还要 `cd desktop && npm install`）：

```bash
make build        # → bin/gerdr（命令行 + 内嵌一份界面源码）
make app          # → build/Gerdr.app（本机架构）
make dmg          # → desktop/dist/Gerdr-<版本>-mac-{arm64,x64}.dmg（Apple 芯片 / Intel 各一个）
make win          # → desktop/dist/gerdr Setup <版本>.exe（Windows 安装包，在 mac 上就能打）
make apk          # → 安卓 App（要 Android SDK 和 JDK 17–21）
make install      # 把 Gerdr.app 放进 /Applications，命令行链到 ~/.local/bin/gerdr
```

## 用

```bash
gerdr             # 起服务，打印一个带令牌的链接，用浏览器打开
gerdr --open      # 起服务并打开浏览器
gerdr url         # 再拿一次登录链接
gerdr acp claude  # 把内置适配器当 stdio ACP agent 跑（给 Zed 等 ACP 客户端用）
```

桌面 App 和命令行可以同时用：App 发现已经有一个 gerdr 在跑，就直接连它，看到的是同一份会话。mac 上打开 设置 → 通用 →「后台常驻」，⌘Q 退出 App 会话也照跑（[细节](docs/dev/SERVICE.md)）。

新建会话：选 agent、选目录、开始。下面会列出这个目录下以前的会话，点一下接着聊。

输入框：回车发送，Shift+回车换行（设置里可以改成 ⌘+回车）；`/` 出斜杠命令；Esc 打断；
粘贴 / 拖进来的图片会一起发。agent 在跑的时候发送 = 插话（Ctrl+回车 = 立即插话）。

鼠标移到自己说过的话上点 ↺，再点一次（或回车）确认，就回到这句之前：这句和后面的对话丢掉，原话（文字和图）放回输入框，原样重发直接点发送，想改就改。只退对话，文件不会恢复。

## 快捷键

和 herdr / tmux 一样：先按前缀 `ctrl+b`，松开，再按：

| 键 | 作用 | 键 | 作用 |
|---|---|---|---|
| `v` | 左右分屏 | `-` | 上下分屏 |
| `h j k l` / 方向键 | 切 pane | `shift+h/j/k/l` | 和邻居交换 |
| `x` | 关 pane | `z` | 放大 / 还原 |
| `c` | 新 tab | `n` / `p` | 下一个 / 上一个 tab |
| `1`…`9` | 跳到第几个 tab | `;` | 上次的 tab |
| `shift+x` | 关 tab | `shift+t` | 重命名 tab |
| `shift+n` | 新工作空间 | `w` | 切工作空间 |
| `g` | 会话列表 | `o` | 跳到在等你的会话 |
| `u` / `d` | 上一个 / 下一个要你处理的会话 | `:` | 命令面板（也可以 ⌘K） |
| `t` | 开终端（跟着当前目录） | `b` | 侧栏 |
| `?` | 全部快捷键 | | |

中文输入法开着也能用（按物理键位认键）。全部可以在「设置 → 快捷键」里改。
mac App 里另有 ⌘T 新 tab、⌘1…9 切 tab 这些菜单快捷键；⌘D / ⌘⇧D 把焦点那个复制一份放右边 / 下面（会话就是同 agent、同目录、同模式的新会话，直接开起来；终端就是同目录终端），要空面板用 prefix+v / prefix+-。

## 和 herdr 比

gerdr 是天天用 [herdr](https://github.com/herdrdev/herdr) 用出来的：分屏、tab、工作空间和快捷键照搬，herdr 里的布局能直接导进来。
两者差在站的那一层：**herdr 管的是 agent 的终端，gerdr 直接跟 agent 本身说话。**

| | herdr | gerdr |
|---|---|---|
| 是什么 | 给 agent 用的终端复用器（Rust TUI） | agent 的图形界面（Go 服务 + 网页界面，Electron 外壳） |
| 在哪用 | 你现有的终端里，SSH 上也行 | 桌面 App、任意浏览器、安卓 App |
| 图片 / 文件 | 在终端里看不了，要另开 | 图片、视频、PDF 在对话里直接看，点路径就打开文件 |
| 支持哪些 agent | 能在终端里跑的都行（Claude Code、Codex、Cursor、OpenCode……） | Claude Code、Codex、任意 ACP agent、内置 Gerdr Agent |
| 审批 / 提问 | 在 agent 自己的 TUI 里回 | 对话里的卡片，手机上、[HTTP 接口](docs/API.md)也能回 |
| 中途说话 | 往 TUI 里打字 | 插进正在跑的那一轮，等这一步做完或立即插 |
| 撤销 | 看 agent 自己有什么 | 回到任何一条消息之前（只退对话，不动文件） |
| 布局 | pane、tab、工作空间、前缀键 | 同一套结构和键位，能从 herdr 导入 |
| 普通 shell | 每个 pane 都是 | 终端 pane，和会话并排 |
| agent 指挥 agent | `herdr agent …` 命令 + socket 接口 | `gerdr session …` 命令 + 内置 skill |
| 远程 | 存好的 SSH 机器放在一个窗口里 | 中继（端到端 TLS）、配对码、passkey；安卓局域网直连 |
| 定制 | 插件 | TS 插件，整个界面也是能热更新的源码 |
| 体量 | 一个二进制，没有 Electron | 一个 Go 二进制；桌面 App 多一层 Electron |

**用 herdr**：常待在终端里、要走 SSH，或者要用的 agent gerdr 没有适配。
**用 gerdr**：想在界面上点审批、回退，想在手机上看 agent 跑得怎么样，或者想改界面本身。
两个也能一起用：把 herdr 的布局导进 gerdr，其余的照样在 herdr 里跑。同一个会话别两边同时开着，两边写同一份记录会分叉，先在一边退出。

### 为什么不是 herdr-web

[herdr-web](https://github.com/zbysir/herdr-web) 把 herdr 搬进浏览器和手机，gerdr 就是从它长出来的。卡住它的是终端本身：
在手机上操作终端，提交一句话、敲一条命令都不自然。要在手机上好用，只能往终端里发按键、再读屏把话抽出来，这是很脆的补丁 ——
agent 的 TUI 加个新选项、改个交互，原来发的按键可能就完全不管用了。

gerdr 换了条路：不经过终端，直接用 agent 的结构化接口说话，界面自己画。审批、提问、回退都是接口调用，
agent 升级了也不会因为屏幕长得不一样就坏。

## 插件

插件目录 `~/.gerdr/plugins/`，一个 `.ts` 文件一个插件，保存即生效（不用装 node，gerdr 自己编译）。
自带的：

- `tab-numbering.ts` —— tab 名前加位置编号 `1.web`、`2.api`，同名的加 `Ⅱ Ⅲ`；
- `duplicate-tab.ts` —— `prefix+a` / `prefix+i` 复制当前 tab 到后面 / 前面；
- `_notify-when-done.ts` —— 示例，默认关（文件名以 `_` 开头的不加载）。

```ts
/// <reference path="./gerdr.d.ts" />
export default definePlugin({
  setup(g) {
    g.paneTitle((pane, { label }) => `🤖 ${label}`);
    g.command({ id: "hello", title: "Say hello", key: "prefix+y", run: () => g.notify("hello") });
  },
});
```

完整 API 在插件目录的 `gerdr.d.ts` 里。

## 配置

界面上的偏好（快捷键、主题、外部 agent）在「设置」里改，存在 `~/.gerdr/config.json`。服务本身用环境变量：

| 变量 | 默认 | |
|---|---|---|
| `GERDR_PORT` | `7870` | 监听端口（被占了会自动换一个） |
| `GERDR_HOST` | `127.0.0.1` | 监听地址 |
| `GERDR_DIR` | `~/.gerdr` | 数据目录（会话、布局、插件） |
| `GERDR_TOKEN` | 自动生成 | 登录令牌，存在 `~/.gerdr/token` |
| `GERDR_CLAUDE_IDLE_MINUTES` | `15` | Claude 会话闲多久就收掉它的进程（下次说话自动接回）；`0` = 不收 |
| `GERDR_UI` | `~/.gerdr/ui` | 界面源码目录（比如单独克隆的 gerdr-ui 仓库） |
| `GERDR_ESM_CDN` | `https://esm.talizen.com` | 界面第三方包的来源（任意 esm.sh 实例，比如 `https://esm.sh`；本机有缓存） |
| `GERDR_CLAUDE_BIN` / `GERDR_CODEX_BIN` | 自动找 | agent 可执行文件的位置 |

**用程序接**（手环、脚本）：看会话、读回复、发消息、替人点审批，都有 HTTP 接口，见 [docs/API.md](docs/API.md)。

**安全**：gerdr 等于一个能在你机器上跑命令的入口，所以永远要令牌，哪怕只听 127.0.0.1。
别把它的端口转发到公网 —— 要从外面用，走下面的「远程访问」。

## 远程访问（手机 / 别的电脑）

和 [herdr-web](https://github.com/zbysir/herdr-web) 同一套：经云端中继（[herdr-relay](https://github.com/zbysir/herdr-web)）对外，TLS 在这台机器上终止，
中继只转密文。新设备用**配对码**进来（只有坐在机器前的人能出），之后加一把 **passkey**，换新设备就不用再回机器前。
本机的令牌在远程**不好使**。设计和理由见 [docs/dev/REMOTE.md](docs/dev/REMOTE.md)。

一次性的准备：

```bash
# VPS 上（已经有 herdr-relay 的话）：给这台机器出一个邀请码
herdr-relay invite gerdr.example.com

# 这台机器上：注册，顺带配好证书（DNS-01 自己签，这里以 Cloudflare 为例）
gerdr remote join https://relay.example.com gerdr.example.com <邀请码> \
  --dns cloudflare --env CLOUDFLARE_DNS_API_TOKEN=…
```

它会打出要加的 DNS 记录（A 指到中继、一条 CAA），配置写进 `~/.gerdr/remote/remote.json`，重启 gerdr 生效（mac App 也认）。

然后：

- **配对**：设置 →「远程访问」→ 出一个配对码（或 `gerdr remote pair`），手机扫码就进来了。码 5 分钟、用一次就废。
- **passkey**：在手机上打开的 gerdr 里，设置 →「远程访问」→ 加一把 passkey。配过对的设备 24 小时没验证要再刷一次。
- **踢设备**：设置里点「踢掉」，或 `gerdr remote devices` / `gerdr remote revoke <id|all>`（all 连 passkey 一起清）。
- `gerdr remote status` 看中继连没连上、证书什么时候到期。

也能全用环境变量配（盖过 remote.json）：`GERDR_RELAY_URL`、`GERDR_RELAY_HOST`、`GERDR_ACME_DNS`、`GERDR_<DNS 凭据>`
（比如 `GERDR_CLOUDFLARE_DNS_API_TOKEN`）、`GERDR_ACME_EMAIL`，或者自己给证书 `GERDR_TLS_CERT` / `GERDR_TLS_KEY`；
`GERDR_DEVICE_TTL_DAYS`（默认 90，0 = 永不过期）、`GERDR_REAUTH_HOURS`（默认 24，0 = 不要求）。

安卓 App 用的是同一份界面，补上浏览器做不到的：在家时局域网直连（origin 不变、TLS 不变）、App 里刷 passkey、后台通知。见 [docs/dev/ANDROID.md](docs/dev/ANDROID.md)。

**多个账号 / 换启动参数**：设置 → Agent →「加一个 Claude」，起个名字（比如「Claude 2」），给它一套环境变量：
订阅账号用一个单独的 `CLAUDE_CONFIG_DIR=~/.claude-2`（然后在终端里 `CLAUDE_CONFIG_DIR=~/.claude-2 claude` 进去 `/login` 一次），
API key 就写 `ANTHROPIC_API_KEY=…`。还能换可执行文件、追加命令行参数。新建会话时它和原版并排出现在 agent 里，
每个是单独的进程和会话记录目录。Codex 同理（`CODEX_HOME` / `OPENAI_API_KEY`）。
外部 ACP agent（Gemini CLI 等）也在这一页加（`gemini --experimental-acp`）。改了立即生效，不用重启。
存在 `config.json` 的 `agents` 里：
`{"id": "claude-2", "name": "Claude 2", "base": "claude", "env": {"CLAUDE_CONFIG_DIR": "~/.claude-2"}, "bin": "", "args": []}`。

**Gerdr Agent（直接用 GPT、DeepSeek 等模型的 API）**：gerdr 自带一个 agent，不经过 Claude Code / Codex，直接调 OpenAI 兼容的接口
（GPT 走 Responses，DeepSeek 等走 Chat Completions）。在 `config.json` 里配了模型，新建会话时就多一个「Gerdr Agent」：

```json
"llm": {
  "providers": [{
    "id": "my", "name": "我的网关", "baseUrl": "https://example.com/v1", "apiKey": "sk-…",
    "models": [{"id": "gpt-6.1-sol"}, {"id": "deepseek-v4-pro"}]
  }],
  "defaultModel": "my/gpt-6.1-sol"
}
```

不想把 key 写进文件就用 `"apiKeyEnv": "环境变量名"`。模型只写 id 就行，协议、思考档位、上下文窗口按 id 猜；猜错了在模型上写
`api`（chat / responses）、`efforts`、`window`、`images`。也可以在设置 → Agent 最下面配（能从接口拉模型列表、单个试一下）。
会话记录在 `~/.gerdr/native/`。插话、回退、审批、子 agent（可以放后台）、后台命令、网页抓取、`/compact`、`/clear`、skill、
MCP（用 Claude Code 配的那些 + gerdr 自己的，不支持要 OAuth 登录的）都支持。
**长期记忆**：你纠正过它的、你的偏好、项目的约定它会自己记下来，新会话也记得。记在 `~/.gerdr/native/memory/`（`global/` 是全局的，
`projects/` 下按项目分），一条一个 Markdown 文件，可以直接看、改、删；`/memory` 看它记了什么，`/memory 忘掉 xxx` 让它删。设置 →「记忆」里能看、改、删，Claude Code 和 Codex 的记忆也在那一页。

## 内存

- Codex：所有会话共用一个 `codex app-server` 进程（几十 MB）。
- Claude Code：一个会话一个 `claude` 进程（几百 MB，CLI 的结构）。闲置 15 分钟的会话会被收掉进程，
  下次说话时自动 `--resume`，对话不丢。
- gerdr 本身：一个 Go 进程，没有 node。

## 开发

见 [docs/dev/ARCHITECTURE.md](docs/dev/ARCHITECTURE.md)（为什么这么设计、协议里实测出来的坑）。在这个仓库里干活的 AI agent 先读 [CLAUDE.md](CLAUDE.md)。

```bash
make test         # Go 测试
make typecheck    # 界面的类型检查（先 cd ui && pnpm install）
make test-e2e     # 对着真的 claude / codex 跑（花钱、要登录）
make dev          # 界面用仓库里的 ui/（改了立刻热更新），数据放 ~/.gerdr-dev，端口 7871
```
