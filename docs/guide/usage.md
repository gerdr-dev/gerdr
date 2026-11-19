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

To try another direction without losing the conversation, fork it: the session menu (⋯ in the pane header) → *Fork…*,
(or type `/fork`), then pick the agent — the same one or another. A new conversation named "Fork: <title>" opens beside it with the
conversation so far, which the agent receives with your first message there; the original stays as it was. Agents can
do the same with `gerdr session fork`.

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
| `i` | browser on the right | `?` | all shortcuts |

Keys are matched by physical position, so they work with a CJK input method on. Rebind everything in
Settings → Shortcuts. The macOS app adds menu shortcuts: `⌘T` new tab, `⌘1…9` go to the Nth agent under "Active" in the sidebar (hold `⌘` to see the numbers; tabs are `prefix+1…9`), `⌘E` back to the last tab even in another workspace, `⌘D` / `⌘⇧D` duplicate
the focused pane to the right / below (a new session with the same agent, directory and mode, or a terminal in the
same directory).

## the Browser pane

A browser can sit beside the chat: the pane's "⋯" menu → "Browser on the right", or `prefix+i`, then type an address
at the top. Pages an agent opens with `gerdr browser open` show up beside it too: you watch it work and can click and
type there yourself (input methods and paste included), e.g. to sign in or solve a captcha for it.

Each project has its own browser (the profile lives in `~/.gerdr/projects/<project>/browser`): a site signed in to
in one project stays signed in there, and other projects don't see it. It is a Chrome Gerdr runs in the background (the
Chrome / Edge / Brave / Chromium installed on the computer), streamed to the pane tile by tile, so it works on a phone
and over remote access too. For an agent to use your everyday, already signed-in Chrome, see the browser extension
below.

## your own Chrome (the browser extension)

The project browser starts signed out. When an agent needs your own accounts (a company back office, sites you use
every day) and you'd rather not sign in again there, install the **Gerdr browser extension** in your everyday Chrome:
Settings → Browser → "Install from the Chrome Web Store"
([store page](https://chromewebstore.google.com/detail/hkooiloghnflaffpkfapfodomgkdpnff)). With Chrome open and
Gerdr running it connects by itself; Settings → Browser shows each connected Chrome and the sites it allowed.

Agents use it when you ask (`gerdr browser open --chrome`). It stays limited:

- the first time an agent needs a site, the extension asks you to allow that one site; the extension's toolbar icon
  lists the allowed sites and removes them;
- it only works in tabs it opened itself, grouped as "Gerdr" in Chrome's tab strip with a blue frame on the page; a
  page that leaves the allowed sites stops being controlled;
- it never reads or changes cookies or site storage, and it only talks to Gerdr on the same computer.

Several Chrome profiles? Name each one from the extension's toolbar icon ("Work", "Personal") and tell the agent which
one to use. Privacy: https://gerdr.dev/privacy.

## memory use

- Codex: one shared `codex app-server` process for all sessions (tens of MB).
- Claude Code: one `claude` process per session (hundreds of MB, that's how the CLI works). Sessions idle for 15
  minutes have their process stopped and are `--resume`d on the next message; nothing is lost.
- Gerdr itself: one Go process, no Node.
