# Configuration

<sub>[简体中文](config.zh-CN.md)</sub>

UI preferences (shortcuts, theme, agents) live in Settings and are saved to `~/.gerdr/config.json`. The server
itself reads environment variables:

| variable | default | |
|---|---|---|
| `GERDR_PORT` | `7870` | listen port (picks another if taken) |
| `GERDR_HOST` | `127.0.0.1` | listen address |
| `GERDR_DIR` | `~/.gerdr` | data directory (sessions, layout, add-ons) |
| `GERDR_LANG` | system language | `zh` / `en`: language of the CLI and of the texts the backend writes into sessions (the UI picks its own in Settings) |
| `GERDR_TOKEN` | generated | login token, stored in `~/.gerdr/token` |
| `GERDR_CLAUDE_IDLE_MINUTES` | `15` | stop an idle Claude session's process after this long (resumed on the next message); `0` = never |
| `GERDR_UI` | `~/.gerdr/ui` | UI source directory (e.g. a separate clone) |
| `GERDR_ESM_CDN` | `https://esm.talizen.com` | where the UI's third-party packages come from (any esm.sh instance, e.g. `https://esm.sh`; cached locally) |
| `GERDR_CLAUDE_BIN` / `GERDR_CODEX_BIN` | auto | agent executables |

**Scripting** — listing sessions, reading replies, sending messages and answering approvals are all HTTP endpoints:
[docs/API.md](../API.md).

**Security** — Gerdr is a way to run commands on your machine, so it always requires a token, even on 127.0.0.1.
Don't port-forward it to the internet; use [remote access](remote.md) instead.
