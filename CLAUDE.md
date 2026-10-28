# gerdr

Claude Code / Codex 的图形界面：Go 后端（ACP 适配器 + hub + HTTP/WS）+ 界面源码（ui/，**没有打包环节**）+ mac App 外壳。
使用说明见 README.md，设计理由和协议的坑见 docs/dev/ARCHITECTURE.md —— **改适配器 / hub / uisite 之前先读它**，
里面每一条都是实测踩出来、会静默出错的。

- 远程访问（中继 / 配对码 / passkey，`internal/auth` `relay` `acme` `remote`）的设计和坑在 docs/dev/REMOTE.md，动之前先读。
- 改完跑 `make test`（Go 测试）；动了界面跑 `make typecheck`（要先 `cd ui && pnpm install`）。
  动了适配器要跑 `make test-e2e`（真 claude / codex，花钱）。
- 注释和文档写中文，说清**为什么**。
- 起调试实例用独立的目录和端口，别碰默认的 `~/.gerdr` 和 7870：`make dev`（界面直接用仓库里的 ui/，改了立刻热更新）。
- 界面怎么写（能 import 什么、样式、目录）在 ui/AGENTS.md；那份也是给用户的 agent 看的（内置的 gerdr-ui skill 让它先读）。
- 内置 skill 在 internal/ext/builtin/，会随二进制发给每个 agent：改 `gerdr session` / `gerdr ui` 的命令行时同步改它们。
- 内置插件（internal/plugins/builtin）和界面的插件 API（ui/src/lib/plugins.ts）要和 `gerdr.d.ts` 一致。
- 界面组件里不写具体颜色，用 index.css 里的 token（bg / muted / brand / ok / warn / bad…）。
