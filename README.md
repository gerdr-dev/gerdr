# gerdr

<p align="center">
  <img src="ui/public/icon.svg" alt="gerdr" width="100" />
</p>

<p align="center">
  <a href="#install">install</a> · <a href="#usage">usage</a> · <a href="#gerdr-vs-herdr">gerdr vs herdr</a> · <a href="docs/API.md">http api</a> · <a href="docs/dev/ARCHITECTURE.md">architecture</a>
</p>

<p align="center">
  English · <a href="README.zh-CN.md">简体中文</a>
</p>

<p align="center">
  <a href="https://github.com/gerdr-dev/gerdr/releases/latest"><img src="https://img.shields.io/github/v/release/gerdr-dev/gerdr?label=release&labelColor=333333&color=666666" alt="latest release" /></a>
  <a href="https://github.com/gerdr-dev/gerdr/releases"><img src="https://img.shields.io/github/downloads/gerdr-dev/gerdr/total?labelColor=333333&color=666666" alt="total downloads" /></a>
  <a href="https://github.com/gerdr-dev/gerdr/stargazers"><img src="https://img.shields.io/github/stars/gerdr-dev/gerdr?labelColor=333333&color=666666&logo=github" alt="GitHub stars" /></a>
</p>

---

**A real GUI for Claude Code and Codex.**

gerdr talks to your agents over a structured protocol (ACP, the Agent Client Protocol), not by typing into a TUI
and scraping the screen. So approvals, questions, interrupting mid-turn and rewinding are real API calls with real
answers, rendered as real UI.

- **talk while it works** — send a message while the agent is busy and it joins the running turn. By default it waits
  for the current step to finish (like Claude Code does); ⚡ or `ctrl+enter` interjects right away.
- **approvals and questions are cards** — commands and file edits that need your OK, and multiple-choice questions
  the agent asks you, show up as cards. One click answers.
- **rewind** — go back to before any message: it and everything after are dropped and the agent forgets them; your
  words go back into the input box to edit and resend. Only the conversation rewinds, files are left alone.
- **pick up old sessions** — Claude Code / Codex sessions you started in a terminal are listed per directory; click
  one to continue it.
- **splits, tabs, workspaces** — the same layout model and prefix keys as [herdr](https://github.com/herdrdev/herdr)
  (`ctrl+b v` to split…). Already on herdr? Settings → Import from herdr brings your workspaces, tabs and splits over
  and reattaches each agent to its session.
- **terminals alongside** — `ctrl+b t` opens a shell in the current directory, splittable like any pane. The shell
  runs inside gerdr, so it survives a page reload and follows you to your phone.
- **agents that coordinate** — an agent can start new Claude Code / Codex sessions, hand them work, wait, and read
  the replies (built-in `gerdr` skill + `gerdr session`).
- **background work in view** — see running background commands and subagents, stop them, move foreground work to
  the background, open a subagent's full transcript read-only.
- **a UI your agent can change** — the interface is plain React source in `~/.gerdr/ui`, with no build step: saved
  edits show up instantly. Ask "add a session counter under the sidebar" and the built-in `gerdr-ui` skill teaches the
  agent how. If an edit breaks the UI, the **rescue panel** (compiled into gerdr, can't be broken) lets you see the
  error, roll back, or have an agent fix it while you watch.
- **TypeScript plugins** — rename tabs, title panes, add commands and keys. Save and it's live.
- **skills and MCP, your way** — use the agent's own, gerdr's, or both; import from the agent in one click.
- **more than two agents** — multiple Claude / Codex accounts side by side, any external ACP agent (Gemini CLI…),
  and a built-in **Gerdr Agent** that calls OpenAI-compatible APIs (GPT, DeepSeek…) directly, with long-term memory.
- **from anywhere** — desktop app (macOS / Windows) or any browser; remote access through an end-to-end TLS relay with
  pairing codes and passkeys; an Android app with LAN direct-connect and background notifications.

## install

Install and log in to the agents you want to use first:

```bash
curl -fsSL https://claude.ai/install.sh | bash   # Claude Code, then run `claude` once to log in
npm i -g @openai/codex                           # Codex, then `codex login`
```

Then grab gerdr from [releases](https://github.com/gerdr-dev/gerdr/releases/latest):

- **macOS** — `Gerdr-x.y.z-mac-arm64.dmg` (Apple silicon) or `Gerdr-x.y.z-mac-x64.dmg` (Intel); drag it into
  Applications. If macOS says the developer can't be verified, right-click → Open in Finder
  (or `xattr -dr com.apple.quarantine /Applications/Gerdr.app`).
- **Android** — `Gerdr-x.y.z-android.apk`. It connects to your own gerdr through [remote access](#remote-access).
- **Windows** — not in releases yet; build the installer with `make win` (unsigned: on SmartScreen click
  More info → Run anyway).

From source (Go 1.24+, Node 20+, pnpm; the desktop app also needs `cd desktop && npm install`):

```bash
make build        # → bin/gerdr (CLI + an embedded copy of the UI source)
make app          # → build/Gerdr.app (this machine's arch)
make dmg          # → desktop/dist/Gerdr-<version>-mac-{arm64,x64}.dmg
make win          # → desktop/dist/gerdr Setup <version>.exe (builds on a Mac too)
make apk          # → Android app (needs the Android SDK and JDK 17–21)
make install      # puts Gerdr.app in /Applications, links the CLI to ~/.local/bin/gerdr
```

## usage

```bash
gerdr             # start the server and print a login link with a token
gerdr --open      # same, and open the browser
gerdr url         # print the login link again
gerdr acp claude  # run the built-in adapter as a stdio ACP agent (for Zed and other ACP clients)
```

> The UI is Chinese-only for now; menu and setting names in this README are translations.

The desktop app and the CLI can run together: if a gerdr is already running, the app connects to it and you see the
same sessions. On macOS, Settings → General → *Run in background* installs a LaunchAgent so sessions keep running
after you quit the app ([details](docs/dev/SERVICE.md)).

New session: pick an agent, pick a directory, go. Earlier sessions in that directory are listed below; click one to
continue it.

In the input box: `enter` sends, `shift+enter` is a newline (switchable to `⌘+enter` in Settings), `/` opens slash
commands, `esc` interrupts, pasted or dropped images are sent along. Sending while the agent runs is an interjection
(`ctrl+enter` = interject now). A line like `` `! ls` `` from the agent gets a *Run* button.

Hover one of your own messages and click ↺, then click again (or press enter) to confirm: the conversation goes back
to before it, and the message (text and images) returns to the input box — send as is, or edit first. Files are not
restored.

### keys

Like herdr / tmux: press the prefix `ctrl+b`, release, then:

| key | action | key | action |
|---|---|---|---|
| `v` | split left/right | `-` | split top/bottom |
| `h j k l` / arrows | move between panes | `shift+h/j/k/l` | swap with neighbor |
| `x` | close pane | `z` | zoom / restore |
| `c` | new tab | `n` / `p` | next / previous tab |
| `1`…`9` | go to tab N | `;` | last tab |
| `shift+x` | close tab | `shift+t` | rename tab |
| `shift+n` | new workspace | `w` | switch workspace |
| `g` | session list | `o` | jump to a session waiting on you |
| `u` / `d` | previous / next session needing you | `:` | command palette (also `⌘K`) |
| `t` | terminal | `b` | sidebar |
| `?` | all shortcuts | | |

Keys are matched by physical position, so they work with a CJK input method on. Rebind everything in
Settings → Shortcuts. The macOS app adds menu shortcuts: `⌘T` new tab, `⌘1…9` switch tab, `⌘D` / `⌘⇧D` duplicate
the focused pane to the right / below (a new session with the same agent, directory and mode, or a terminal in the
same directory).

## gerdr vs herdr

gerdr grew out of using [herdr](https://github.com/herdrdev/herdr) daily: it keeps herdr's layout model and keys, and
can import a herdr setup. The difference is the layer they work at. **herdr owns the agents' terminals; gerdr talks to
the agents themselves.**

| | herdr | gerdr |
|---|---|---|
| what it is | terminal multiplexer for agents (Rust TUI) | graphical client for agents (Go server + web UI, Electron shell) |
| runs in | the terminal you already use, over SSH | desktop app, any browser, Android app |
| how it sees an agent | the agent's terminal: screen state, hooks | a structured protocol (ACP): messages, tool calls, permissions as data |
| supported agents | anything that runs in a terminal (Claude Code, Codex, Cursor, OpenCode…) | Claude Code, Codex, any ACP agent, the built-in Gerdr Agent |
| approvals / questions | answered in the agent's own TUI | cards in the chat, also answerable from your phone or the [HTTP API](docs/API.md) |
| talking mid-turn | type into the TUI | interject into the running turn, queued or immediate |
| undo | whatever the agent offers | rewind to any message (conversation only, files untouched) |
| layout | panes, tabs, workspaces, prefix keys | same model, same keys, importable from herdr |
| plain shells | every pane is one | terminal panes next to sessions |
| agents driving each other | `herdr agent …` CLI + socket API | `gerdr session …` CLI + built-in skill |
| remote | saved SSH machines in one window | relay with end-to-end TLS, pairing codes, passkeys; LAN direct on Android |
| customizing | plugins | TS plugins, plus the whole UI is editable source with hot reload |
| footprint | one binary, no Electron | one Go binary; the desktop app adds Electron |

**Pick herdr** if you live in the terminal, work over SSH, or use agents gerdr has no adapter for.
**Pick gerdr** if you want approvals and rewinds as UI, want to check on agents from your phone, or want to
reshape the interface itself. They also mix: import a herdr setup into gerdr, keep using herdr for the rest. A session
open in both at once will fork its transcript, so quit it in one first.

## plugins

Plugins live in `~/.gerdr/plugins/`, one `.ts` file each, live on save (no Node needed, gerdr compiles them). Bundled:

- `tab-numbering.ts` — prefixes tab names with their position (`1.web`, `2.api`), numbers duplicates `Ⅱ Ⅲ`;
- `duplicate-tab.ts` — `prefix+a` / `prefix+i` duplicates the current tab after / before itself;
- `_notify-when-done.ts` — an example, off by default (files starting with `_` aren't loaded).

```ts
/// <reference path="./gerdr.d.ts" />
export default definePlugin({
  setup(g) {
    g.paneTitle((pane, { label }) => `🤖 ${label}`);
    g.command({ id: "hello", title: "Say hello", key: "prefix+y", run: () => g.notify("hello") });
  },
});
```

The full API is in `gerdr.d.ts` in the plugin directory.

## configuration

UI preferences (shortcuts, theme, agents) live in Settings and are saved to `~/.gerdr/config.json`. The server
itself reads environment variables:

| variable | default | |
|---|---|---|
| `GERDR_PORT` | `7870` | listen port (picks another if taken) |
| `GERDR_HOST` | `127.0.0.1` | listen address |
| `GERDR_DIR` | `~/.gerdr` | data directory (sessions, layout, plugins) |
| `GERDR_TOKEN` | generated | login token, stored in `~/.gerdr/token` |
| `GERDR_CLAUDE_IDLE_MINUTES` | `15` | stop an idle Claude session's process after this long (resumed on the next message); `0` = never |
| `GERDR_UI` | `~/.gerdr/ui` | UI source directory (e.g. a separate clone) |
| `GERDR_ESM_CDN` | `https://esm.sh` | where the UI's third-party packages come from (esm.sh or a mirror; cached locally) |
| `GERDR_CLAUDE_BIN` / `GERDR_CODEX_BIN` | auto | agent executables |

**Scripting** — listing sessions, reading replies, sending messages and answering approvals are all HTTP endpoints:
[docs/API.md](docs/API.md).

**Security** — gerdr is a way to run commands on your machine, so it always requires a token, even on 127.0.0.1.
Don't port-forward it to the internet; use [remote access](#remote-access) instead.

### more agents and accounts

Settings → Agents → *Add a Claude*: give it a name (say "Claude 2") and an environment. For a second subscription
use its own `CLAUDE_CONFIG_DIR=~/.claude-2` (then `CLAUDE_CONFIG_DIR=~/.claude-2 claude` in a terminal and `/login`
once); for an API key set `ANTHROPIC_API_KEY=…`. You can also swap the executable or append arguments. It shows up
next to the original when starting a session, with its own process and transcript directory. Codex works the same
way (`CODEX_HOME` / `OPENAI_API_KEY`). External ACP agents (e.g. `gemini --experimental-acp`) are added on the same
page. Changes apply immediately. Stored under `agents` in `config.json`:
`{"id": "claude-2", "name": "Claude 2", "base": "claude", "env": {"CLAUDE_CONFIG_DIR": "~/.claude-2"}, "bin": "", "args": []}`.

### Gerdr Agent (GPT, DeepSeek… straight from the API)

gerdr ships its own agent that skips Claude Code / Codex and calls OpenAI-compatible APIs (Responses for GPT, Chat
Completions for DeepSeek and others). Configure models in `config.json` and *Gerdr Agent* appears when starting a
session:

```json
"llm": {
  "providers": [{
    "id": "my", "name": "My gateway", "baseUrl": "https://example.com/v1", "apiKey": "sk-…",
    "models": [{"id": "gpt-6.1-sol"}, {"id": "deepseek-v4-pro"}]
  }],
  "defaultModel": "my/gpt-6.1-sol"
}
```

Use `"apiKeyEnv": "VAR_NAME"` to keep the key out of the file. A model id is usually enough; protocol, reasoning
levels and context window are guessed from it, and you can override with `api` (chat / responses), `efforts`,
`window`, `images`. The bottom of Settings → Agents can do this too (fetch the model list, test a model).

Transcripts go to `~/.gerdr/native/`. Interjecting, rewind, approvals, subagents (backgroundable), background
commands, web fetch, `/compact`, `/clear`, skills and MCP (Claude Code's servers plus gerdr's; no OAuth servers) all
work. **Long-term memory**: corrections, your preferences and project conventions are remembered across sessions, one
Markdown file each under `~/.gerdr/native/memory/` (`global/`, and `projects/` per project). `/memory` lists them,
`/memory forget xxx` deletes one; Settings → Memory shows them too, alongside Claude Code's and Codex's.

## remote access

From your phone or another computer, through a cloud relay ([herdr-relay](https://github.com/zbysir/herdr-web)).
TLS terminates on this machine; the relay only forwards ciphertext. A new device gets in with a **pairing code** (only
someone at the machine can issue one), then adds a **passkey** so the next device doesn't need you at the machine.
The local token does **not** work remotely. Design and reasoning: [docs/dev/REMOTE.md](docs/dev/REMOTE.md).

One-time setup:

```bash
# on the VPS (with herdr-relay already running): issue an invite for this machine
herdr-relay invite gerdr.example.com

# on this machine: register and set up certificates (DNS-01, Cloudflare as the example)
gerdr remote join https://relay.example.com gerdr.example.com <invite> \
  --dns cloudflare --env CLOUDFLARE_DNS_API_TOKEN=…
```

It prints the DNS records to add (an A record to the relay, a CAA record), writes `~/.gerdr/remote/remote.json`, and
takes effect after restarting gerdr (the macOS app reads it too). Then:

- **pair** — Settings → Remote access → issue a pairing code (or `gerdr remote pair`), scan it with your phone. Codes
  last 5 minutes and work once.
- **passkey** — in gerdr on the phone, Settings → Remote access → add a passkey. Paired devices re-verify after 24
  hours without one.
- **revoke** — *Kick* in Settings, or `gerdr remote devices` / `gerdr remote revoke <id|all>` (`all` clears passkeys
  too).
- `gerdr remote status` shows the relay connection and certificate expiry.

Everything can also come from the environment (overrides `remote.json`): `GERDR_RELAY_URL`, `GERDR_RELAY_HOST`,
`GERDR_ACME_DNS`, `GERDR_<DNS credential>` (e.g. `GERDR_CLOUDFLARE_DNS_API_TOKEN`), `GERDR_ACME_EMAIL`, or your own
certificate via `GERDR_TLS_CERT` / `GERDR_TLS_KEY`; `GERDR_DEVICE_TTL_DAYS` (default 90, 0 = never expire),
`GERDR_REAUTH_HOURS` (default 24, 0 = never).

The [Android app](docs/dev/ANDROID.md) wraps the same UI and adds what a browser can't: direct LAN connection when
you're home (same origin, same TLS), passkeys inside the app, and background notifications.

## memory use

- Codex: one shared `codex app-server` process for all sessions (tens of MB).
- Claude Code: one `claude` process per session (hundreds of MB, that's how the CLI works). Sessions idle for 15
  minutes have their process stopped and are `--resume`d on the next message; nothing is lost.
- gerdr itself: one Go process, no Node.

## development

Read [docs/dev/ARCHITECTURE.md](docs/dev/ARCHITECTURE.md) first: why it's built this way, and the protocol pitfalls
found by testing. AI agents working on this repo: start with [CLAUDE.md](CLAUDE.md).

```bash
make test         # Go tests
make typecheck    # UI type check (cd ui && pnpm install first)
make test-e2e     # against real claude / codex (costs money, needs login)
make dev          # UI served from the repo's ui/ with hot reload, data in ~/.gerdr-dev, port 7871
```
