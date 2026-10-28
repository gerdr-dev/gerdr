# gerdr

**Claude Code 和 Codex 的图形界面。** 不再隔着一层 TUI 模拟按键：底下走 ACP（Agent Client Protocol），
所以审批、提问、中途插话、回退都是真接口，而不是往终端里敲字再读屏。

- **中途插话**：agent 在干活的时候直接说话，消息插进正在跑的那一轮（不用等、不用先打断）。
- **审批 / 提问**：要你批准的命令、改文件，agent 问你的选择题，都是界面上的卡片，点一下就回。
- **回退**：回到任何一条消息之前 —— 对话截断，**文件也一起恢复**（含 agent 用 shell 改的文件）。
- **接着以前的聊**：在终端里开过的 Claude Code / Codex 会话，选目录就能列出来接着聊。
- **从 herdr 导入**：herdr 的工作空间、tab、分屏原样搬过来，每个 agent 接上它的会话（设置 → 从 herdr 导入）。
- **分屏、tab、工作空间**：和 [herdr](https://github.com/herdrdev/herdr) 一样的结构和快捷键（`ctrl+b v` 左右分屏……）。
- **终端**：临时敲几条命令不用切出去：`ctrl+b t` 在旁边开一个（跟着当前目录），和会话一起分屏、开 tab。shell 跑在 gerdr 这边，刷新页面、换到手机上接着用。
- **改坏了有急救**：主界面被改坏打不开时，报错页上点「急救面板」（或右下角冒出来的「急救」、App 菜单 帮助 → 急救）：从右边拉出一个面板，看错误、退回之前的一版、叫 agent 来修，主界面就在旁边看着它修。这个面板编进 gerdr，改不坏。
- **界面能让 agent 改**：界面就是 `~/.gerdr/ui` 里的 React 源码，没有打包环节，改完保存立刻生效。
  对 agent 说「侧栏底下加一个会话统计」就行（内置的 gerdr-ui skill 教它怎么改、改完怎么确认没改坏）。
- **多个 agent 互相协作**：会话里的 agent 能开新会话（Claude Code / Codex）、派活、等结果、读回复（内置的 gerdr skill + `gerdr session` 命令）。
- **后台任务和子 agent**：看得到在跑的后台命令、子 agent，能停、能把前台的转到后台；子 agent 的完整对话可以开一个只读面板看。
- **skill / MCP 自己管**：用 agent 自己的、gerdr 的、或两者都用；从 agent 那边一键导入。
- **TS 插件**：改 tab 名、pane 标题，加命令和快捷键。保存即生效。
- **桌面 App**（mac / Windows）：独立窗口、系统通知、Dock 角标；也可以只跑后端，用浏览器打开。

## 装

需要先装好、登录好你要用的 agent：

```bash
curl -fsSL https://claude.ai/install.sh | bash   # Claude Code，然后跑一次 claude 登录
npm i -g @openai/codex                           # Codex，然后 codex login
```

**mac App**：下载 `gerdr-x.y.z.dmg`，拖进「应用程序」。第一次打开如果提示「无法验证开发者」，
在 Finder 里右键 → 打开（或 `xattr -dr com.apple.quarantine /Applications/Gerdr.app`）。

**Windows**：下载 `gerdr Setup x.y.z.exe` 装上（没签名，SmartScreen 拦的话点「更多信息 → 仍要运行」）。

**从源码**（Go 1.24+、Node 20+、pnpm；打桌面 App 还要 `cd desktop && npm install`）：

```bash
make build        # → bin/gerdr（命令行 + 内嵌一份界面源码）
make app          # → build/Gerdr.app（本机架构）
make dmg          # → desktop/dist/gerdr-<版本>-universal.dmg（mac 通用安装包）
make win          # → desktop/dist/gerdr Setup <版本>.exe（Windows 安装包，在 mac 上就能打）
make install      # 把 Gerdr.app 放进 /Applications，命令行链到 ~/.local/bin/gerdr
```

## 用

```bash
gerdr             # 起服务，打印一个带令牌的链接，用浏览器打开
gerdr --open      # 起服务并打开浏览器
gerdr url         # 再拿一次登录链接
gerdr acp claude  # 把内置适配器当 stdio ACP agent 跑（给 Zed 等 ACP 客户端用）
```

桌面 App 和命令行可以同时用：App 发现已经有一个 gerdr 在跑，就直接连它，看到的是同一份会话。

新建会话：选 agent、选目录、开始。下面会列出这个目录下以前的会话，点一下接着聊。

输入框：回车发送，Shift+回车换行（设置里可以改成 ⌘+回车）；`/` 出斜杠命令；Esc 打断；
粘贴 / 拖进来的图片会一起发。agent 在跑的时候发送 = 插话。

鼠标移到自己说过的话上：✏️ 改这句重发、↺ 回到这条消息之前（可选连文件一起恢复）。

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
| `b` | 侧栏 | `?` | 全部快捷键 |

中文输入法开着也能用（按物理键位认键）。全部可以在「设置 → 快捷键」里改。
mac App 里另有 ⌘T 新 tab、⌘D 分屏、⌘1…9 切 tab 这些菜单快捷键。

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
| `GERDR_ESM_CDN` | `https://esm.sh` | 界面第三方包的来源（esm.sh 或它的镜像；本机有缓存） |
| `GERDR_CLAUDE_BIN` / `GERDR_CODEX_BIN` | 自动找 | agent 可执行文件的位置 |

**用程序接**（手环、脚本）：看会话、读回复、发消息、替人点审批，都有 HTTP 接口，见 [docs/API.md](docs/API.md)。

**安全**：gerdr 等于一个能在你机器上跑命令的入口，所以永远要令牌，哪怕只听 127.0.0.1。
别把它的端口转发到公网 —— 要从外面用，走下面的「远程访问」。

## 远程访问（手机 / 别的电脑）

和 herdr-web 同一套：经云端中继（[herdr-relay](https://github.com/zbysir/herdr-web)）对外，TLS 在这台机器上终止，
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

**多个账号 / 换启动参数**：设置 → Agent →「加一个 Claude」，起个名字（比如「Claude 2」），给它一套环境变量：
订阅账号用一个单独的 `CLAUDE_CONFIG_DIR=~/.claude-2`（然后在终端里 `CLAUDE_CONFIG_DIR=~/.claude-2 claude` 进去 `/login` 一次），
API key 就写 `ANTHROPIC_API_KEY=…`。还能换可执行文件、追加命令行参数。新建会话时它和原版并排出现在 agent 里，
每个是单独的进程和会话记录目录。Codex 同理（`CODEX_HOME` / `OPENAI_API_KEY`）。
外部 ACP agent（Gemini CLI 等）也在这一页加（`gemini --experimental-acp`）。改了立即生效，不用重启。
存在 `config.json` 的 `agents` 里：
`{"id": "claude-2", "name": "Claude 2", "base": "claude", "env": {"CLAUDE_CONFIG_DIR": "~/.claude-2"}, "bin": "", "args": []}`。

## 内存

- Codex：所有会话共用一个 `codex app-server` 进程（几十 MB）。
- Claude Code：一个会话一个 `claude` 进程（几百 MB，CLI 的结构）。闲置 15 分钟的会话会被收掉进程，
  下次说话时自动 `--resume`，对话不丢。
- gerdr 本身：一个 Go 进程，没有 node。

## 开发

见 [docs/dev/ARCHITECTURE.md](docs/dev/ARCHITECTURE.md)（为什么这么设计、协议里实测出来的坑）。

```bash
make test         # Go 测试
make typecheck    # 界面的类型检查（先 cd ui && pnpm install）
make test-e2e     # 对着真的 claude / codex 跑（花钱、要登录）
make dev          # 界面用仓库里的 ui/（改了立刻热更新），数据放 ~/.gerdr-dev，端口 7871
```
