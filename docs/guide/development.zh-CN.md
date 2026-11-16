# 构建和开发

<sub>[English](development.md)</sub>

从源码构建（Go 1.24+、Node 20+、pnpm）：

```bash
make build        # → bin/gerdr（命令行 + 内嵌一份界面源码）
make app          # → tools/chromeshell/dist/<架构>/Gerdr.app + dmg（本机架构）
make dmg          # → tools/chromeshell/dist/Gerdr-<版本>-mac-{arm64,amd64}.dmg（Apple 芯片 / Intel 各一个）
make win          # → tools/winshell/dist/Gerdr-<版本>-windows-x64-setup.exe（Windows 安装包，在 mac 上就能打）
make apk          # → 安卓 App（要 Android SDK 和 JDK 17–21）
make install      # 把 Gerdr.app 放进 /Applications，命令行链到 ~/.local/bin/gerdr
```

见 [docs/dev/ARCHITECTURE.md](../dev/ARCHITECTURE.md)（为什么这么设计、协议里实测出来的坑）。在这个仓库里干活的 AI agent 先读 [CLAUDE.md](../../CLAUDE.md)。

```bash
make test         # Go 测试
make typecheck    # 界面的类型检查（先 cd ui && pnpm install）
make test-e2e     # 对着真的 claude / codex 跑（花钱、要登录）
make dev          # 界面用仓库里的 ui/（改了立刻热更新），数据放 ~/.gerdr-dev，端口 7871
```
