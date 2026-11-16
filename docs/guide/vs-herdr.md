# Gerdr vs herdr

<sub>[简体中文](vs-herdr.zh-CN.md)</sub>

Gerdr grew out of using [herdr](https://github.com/herdrdev/herdr) daily: it keeps herdr's layout model and keys, and
can import a herdr setup. The difference is the layer they work at. **herdr owns the agents' terminals; Gerdr talks to
the agents themselves.**

| | herdr | Gerdr |
|---|---|---|
| what it is | terminal multiplexer for agents (Rust TUI) | graphical client for agents (Go server + web UI, a desktop shell on the user's own Chrome) |
| runs in | the terminal you already use, over SSH | desktop app, any browser, Android app |
| images and files | not in a terminal; open them elsewhere | images, videos and PDFs inline in the chat; click a path to open the file |
| supported agents | anything that runs in a terminal (Claude Code, Codex, Cursor, OpenCode…) | Claude Code, Codex, any ACP agent, the built-in Gerdr Agent |
| approvals / questions | answered in the agent's own TUI | cards in the chat, also answerable from your phone or the [HTTP API](../API.md) |
| talking mid-turn | type into the TUI | interject into the running turn, queued or immediate |
| undo | whatever the agent offers | rewind to any message (conversation only, files untouched) |
| layout | panes, tabs, workspaces, prefix keys | same model, same keys, importable from herdr |
| plain shells | every pane is one | terminal panes next to sessions |
| agents driving each other | `herdr agent …` CLI + socket API | `gerdr session …` CLI + built-in skill |
| remote | saved SSH machines in one window | relay with end-to-end TLS, pairing codes, passkeys; LAN direct on Android |
| customizing | plugins | TS add-ons with a store, plus the whole UI is editable source with hot reload |
| footprint | one binary, no Electron | one Go binary; the desktop app adds a ~18MB shell (it uses the Chrome you have) |

**Pick herdr** if you live in the terminal, work over SSH, or use agents Gerdr has no adapter for.
**Pick Gerdr** if you want approvals and rewinds as UI, want to check on agents from your phone, or want to
reshape the interface itself. They also mix: import a herdr setup into Gerdr, keep using herdr for the rest. A session
open in both at once will fork its transcript, so quit it in one first.

## why not herdr-web?

[herdr-web](https://github.com/zbysir/herdr-web) brings herdr to the browser and the phone, and Gerdr grew out of it.
What held it back is the terminal itself: on a phone, sending a message or typing a command into a terminal never
feels natural. The only way to make it work is to send keystrokes into the terminal and scrape the screen for the
reply, a fragile patch: when an agent's TUI gains a new option or changes an interaction, the keys you used to send
can stop working altogether.

Gerdr takes another route: it skips the terminal and talks to the agent through its structured interface, drawing
its own UI. Approvals, questions and rewinds are API calls, so an agent update doesn't break things just because
the screen looks different.
