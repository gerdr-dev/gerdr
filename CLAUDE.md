# gerdr

Claude Code / Codex 的图形界面：Go 后端（ACP 适配器 + hub + HTTP/WS）+ 界面源码（ui/，**没有打包环节**）+ 桌面 App 外壳（desktop/，Electron —— 为什么是它：docs/dev/WHY-ELECTRON.md）。
使用说明见 README.md，设计理由和协议的坑见 docs/dev/ARCHITECTURE.md —— **改适配器 / hub / uisite 之前先读它**，
里面每一条都是实测踩出来、会静默出错的。

- 远程访问（中继 / 配对码 / passkey，`internal/auth` `relay` `acme` `remote`）的设计和坑在 docs/dev/REMOTE.md，动之前先读。
- 安卓 App（android/：局域网直连、App 里的 passkey、后台通知，`internal/lan`、`remote/lan.go`、`server/appapi.go` `appwatch.go`）：
  docs/dev/ANDROID.md。桥的两半（`Bridge.kt` ↔ `ui/src/lib/android.ts`）要一起改；换正式签名钥匙要改 `config.OfficialAndroidCert`。
- Measured-and-postponed work (why, numbers, how to do it): docs/dev/TODO.md. Add to it instead of losing the investigation.
- Desktop App self-update (backend-only ~10MB vs whole App, applied only when idle; `desktop/updater.js`,
  `desktop/shell-id.sh`): docs/dev/UPDATE.md. Changing shell files forces a full download for everyone.
- 后台常驻（launchd，`internal/service`、`cmd/gerdr/attach.go`）和 App 怎么分工、怎么跟着 App 升级：docs/dev/SERVICE.md。
- 对话列表闪、跳、切回来不在底（虚拟列表 `chat/ChatView.tsx`、会变高矮的 Markdown / 代码块 / 图片）：别猜，
  按 docs/dev/SCROLL.md 用 `tools/scrolltest/` 的脚本逐帧复现，改完再跑一遍。
- 改完跑 `make test`（Go 测试）；动了界面先 `make fmt` 再跑 `make typecheck`（要先 `cd ui && pnpm install`；它也查格式）。
  动了适配器要跑 `make test-e2e`（真 claude / codex，花钱）。
- English-first：提交信息、代码注释、文档一律用英文写，说清**为什么**。已有的中文注释 / 文档不用专门去翻，改到哪段时顺手换成英文。
- 起调试实例用独立的目录和端口，别碰默认的 `~/.gerdr` 和 7870：`make dev`（界面直接用仓库里的 ui/，改了立刻热更新）。
- Putting a new backend on this machine (`make install`): **never restart gerdr yourself** (no `pkill gerdr`, no
  `launchctl kickstart`, no reopening the App). You are one of the agents running on it, and so are the user's others:
  a restart cuts every running turn off. The background service (on by default) picks the new binary up by itself
  once nothing is running — no turn, no pending permission, no background task, no open terminal (`GET /api/busy`,
  `appAttach` in cmd/gerdr/attach.go) — so it happens after your turn ends. The App has to be open for that (it is
  what watches); if the user turned the background service off, tell them to reopen the App when they're idle.
  Exception: an App opened before edbb169 runs an old watcher that never notices the new file; same for an App
  opened before the shell read the bundle's version from disk, while it runs a downloaded backend. Tell the user to quit
  and reopen the App once; with the background service on that is safe even mid-turn (it only replaces the watcher,
  the service and its agents keep running), and the new watcher then restarts the service once idle.
  Same rule as App updates: docs/dev/UPDATE.md.
- 改完界面、看过没问题后跑 `make sync-ui`，同步到正在用的 `~/.gerdr/ui`（开着的 gerdr 直接热更新）：
  正在用的 gerdr 读的是那份，不是仓库里的 ui/，不同步用户就看不到改动。别手动 cp 或在那边 git 提交 ——
  `gerdr ui sync` 会一起更新清单（不然下次升级会把它当成被人改过），那边被人改过时也会拒绝覆盖。
- 界面怎么写（能 import 什么、样式、目录）在 ui/AGENTS.md；那份也是给用户的 agent 看的（内置的 gerdr-ui skill 让它先读）。
- 内置 skill 在 internal/ext/builtin/，会随二进制发给每个 agent：改 `gerdr session` / `gerdr ui` 的命令行时同步改它们。
- Add-ons (`internal/addons`, `ui/src/lib/addons.ts`, Settings → Add-ons): docs/dev/ADDONS.md. The UI's add-on API
  must match `internal/addons/builtin/gerdr.d.ts`; the add-ons themselves live in the gerdr-dev/gerdr-addon repo.
- 界面组件里不写具体颜色，用 index.css 里的 token（bg / muted / brand / ok / warn / bad…）。
