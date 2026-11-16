# Using Gerdr

<sub>[简体中文](usage.zh-CN.md)</sub>

```bash
gerdr             # start the server and print a login link with a token
gerdr --open      # same, and open the browser
gerdr url         # print the login link again
gerdr acp claude  # run the built-in adapter as a stdio ACP agent (for Zed and other ACP clients)
```

The desktop app and the CLI can run together: if a Gerdr is already running, the app connects to it and you see the
same sessions. On macOS, Settings → General → *Run in background* installs a LaunchAgent so sessions keep running
after you quit the app ([details](../dev/SERVICE.md)).

New session: pick an agent, pick a directory, go. Earlier sessions in that directory are listed below; click one to
continue it.

In the input box: `enter` sends, `shift+enter` is a newline (switchable to `⌘+enter` in Settings), `/` opens slash
commands, `esc` interrupts, pasted or dropped images are sent along. Sending while the agent runs is an interjection
(`ctrl+enter` = interject now). A line like `` `! ls` `` from the agent gets a *Run* button.

Hover one of your own messages and click ↺, then click again (or press enter) to confirm: the conversation goes back
to before it, and the message (text and images) returns to the input box — send as is, or edit first. Files are not
restored.

## keys

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
Settings → Shortcuts. The macOS app adds menu shortcuts: `⌘T` new tab, `⌘1…9` go to the Nth agent under "Active" in the sidebar (hold `⌘` to see the numbers; tabs are `prefix+1…9`), `⌘E` back to the last tab even in another workspace, `⌘D` / `⌘⇧D` duplicate
the focused pane to the right / below (a new session with the same agent, directory and mode, or a terminal in the
same directory).

## memory use

- Codex: one shared `codex app-server` process for all sessions (tens of MB).
- Claude Code: one `claude` process per session (hundreds of MB, that's how the CLI works). Sessions idle for 15
  minutes have their process stopped and are `--resume`d on the next message; nothing is lost.
- Gerdr itself: one Go process, no Node.
