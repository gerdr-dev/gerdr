# Building and developing

<sub>[简体中文](development.zh-CN.md)</sub>

From source (Go 1.24+, Node 20+, pnpm):

```bash
make build        # → bin/gerdr (CLI + an embedded copy of the UI source)
make app          # → desktop/dist/<arch>/Gerdr.app + dmg (this machine's arch)
make dmg          # → desktop/dist/Gerdr-<version>-mac-{arm64,amd64}.dmg
make win          # → desktop/dist/win/Gerdr-<version>-windows-x64-setup.exe (builds on a Mac too)
make apk          # → Android app (needs the Android SDK and JDK 17–21)
make install      # puts Gerdr.app in /Applications, links the CLI to ~/.local/bin/gerdr
```

Read [docs/dev/ARCHITECTURE.md](../dev/ARCHITECTURE.md) first: why it's built this way, and the protocol pitfalls
found by testing. AI agents working on this repo: start with [CLAUDE.md](../../CLAUDE.md).

```bash
make test         # Go tests
make typecheck    # UI type check (cd ui && pnpm install first)
make test-e2e     # against real claude / codex (costs money, needs login)
make dev          # UI served from the repo's ui/ with hot reload, data in ~/.gerdr-dev, port 7871
```
