# Writing Gerdr add-ons

An add-on changes Gerdr itself: a button on the rail and the panel it opens, the names of tabs and panes, a button in the composer, the
line under each turn, commands and keys, a process that keeps running. It is a folder of TypeScript; Gerdr compiles it
on save (no Node, no build step) and runs it in the page.

Add-ons are not Claude Code or Codex plugins. Those extend the agent (skills, MCP, hooks on its tool calls) and run
inside one agent process; an add-on extends the window around all of them, every session and every agent at once.

This guide is installed as `~/.gerdr/addons/GUIDE.md` next to `gerdr.d.ts`, the exact API with a comment on every
call. Both are rewritten on every Gerdr start: read them, don't edit them. Working examples:
https://github.com/gerdr-dev/gerdr-addon (each folder there is one add-on).

## Quick start

```
~/.gerdr/addons/hello/
  addon.json   {"name": "Hello", "version": "1.0.0", "description": "Says hello"}
  index.tsx
```

```tsx
/// <reference path="../gerdr.d.ts" />
export default defineAddon({
  setup(g) {
    const off = g.command({ id: "hello.say", title: "Say hello", key: "prefix+y", run: () => g.notify("hello") });
    return off; // called when the add-on unloads (edited, switched off, removed)
  },
});
```

Save, and it is live in every open window. Settings → Add-ons → Installed lists it: loaded, or the error that stopped
it (a compile error with file and line, or what `setup` threw). An add-on can't take the UI down: every hook it
registers runs in a try/catch, every component it draws in its own error boundary.

- One folder per add-on; the folder name is its id. A folder whose name starts with `_` isn't loaded.
- The entry is `index.ts` / `index.tsx` / `index.js`, or what `"main"` names. It may import other files of the folder
  (`import { x } from "./lib"`); everything is bundled into one module. npm packages can't be imported: React, Gerdr's
  components and icons come through `g` instead.
- `setup(g)` runs once per load. Return a function (or call the `Dispose` each registration returns) to clean up
  timers and listeners you made yourself; registrations made through `g` are removed for you on unload.

## addon.json

All fields optional; a store only lists folders that have one.

| field | |
|---|---|
| `name` | Shown in Settings and the store. Defaults to the folder name. |
| `version` | Shown next to the name. Updates are detected by content, not by this, but bump it anyway. |
| `description` | One or two sentences under the name. |
| `icon` | A file of the folder (svg / png), shown square at 36px. |
| `screenshots` | Files of the folder, shown as thumbnails in the store (click opens a lightbox). Ship at least one. |
| `main` | The UI half's entry, when it isn't `index.*`. |
| `settings` | A form in Settings → Add-ons; see [Settings](#settings). |
| `service` | A process the backend keeps running; see [Services](#services). |
| `i18n` | The texts above in other languages; see [Languages](#languages). |

## Languages

Gerdr's interface is in Chinese or English (Settings → General → Language; it follows the system by default). Write the
fields of `addon.json` in one language, the base, and give the others under `i18n`, by locale. Anything left out keeps
the base text:

```json
{
  "name": "Pixel Clawd",
  "description": "Replaces the working dots with a pixel crab.",
  "settings": [
    { "key": "size", "title": "Size", "type": "select", "default": "small",
      "options": [{ "value": "small", "label": "Small" }, { "value": "medium", "label": "Medium" }] }
  ],
  "i18n": {
    "zh": {
      "name": "像素 Clawd",
      "description": "把运行中的点点换成像素小螃蟹。",
      "settings": { "size": { "title": "尺寸", "options": { "small": "小", "medium": "中" } } }
    }
  }
}
```

A setting's entry can have `title`, `description`, `unit` and `options` (label by the option's value). The store shows
`README.<locale>.md` (`README.zh.md`) in that language instead of `README.md`. In code, `g.locale()` is `"zh"` or
`"en"`: pick the texts your components draw from it.

## What `g` can do

### Rename things

```ts
g.tabLabel((tab, { index, label }) => `${index + 1}. ${label}`);
g.paneTitle((pane, { label }) => (pane.session?.status === "running" ? `⏳ ${label}` : label));
g.workspaceLabel((ws, { label }) => label.toUpperCase());
g.sessionTitle(async (session, ctx) => (ctx.turn % 5 === 0 ? await ctx.summarize("…") : undefined));
```

Return a string to change it, `undefined` to leave it. Several add-ons chain in folder-name order (`label` is what the
previous one gave).

### Commands and keys

`g.command({ id, title, key?, run })` adds a command to the palette (prefix+:) and, with `key`, a shortcut
(`"prefix+a"`, `"ctrl+alt+p"`). A built-in key wins over an add-on's on a clash.

### Events and actions

```ts
g.on("turnEnd", (e) => { if (!e.visible) g.notify(`${e.session.title} finished`, { system: true }); });
g.on("needsInput", (e) => …);    // a permission or a question is waiting
g.on("sessionCreated", (e) => …);
g.on("focusChanged", (e) => …);
g.on("turn", (t) => …);          // a turn ended, with its facts (see g.turns)
```

`g.state()` is a read-only snapshot of workspaces, tabs, panes and sessions. `g.actions` does things: `newTab`,
`duplicateTab`, `split`, `focusPane`, `renameTab`, `renameWorkspace`, `send(sessionId, text)` (sends a message),
`insertText(sessionId, text)` (types into its composer without sending), `runCommand`. `g.notify(text, { system })`
shows a toast or a system notification.

### Draw

Name the file `.tsx` and write JSX. Hooks come from `g.React` (the React that mounts your components: never import
your own). `g.ui` has Gerdr's building blocks (`Button`, `Popover`, `Tooltip`, `Switch`, `cn`), `g.icons` every
lucide icon (`<g.icons.Timer className="size-3.5" />`).

| where | call | gets |
|---|---|---|
| A block of your panel, opened from your add-on's own button on the rail | `g.slot("panel", Component, { order, id, title, icon, tallIcon, open })` | no props |
| A sprite: a small picture the user can use as the working indicator (Settings → General) or as an agent's icon | `g.slot("sprite", Component, { id, title })` | `{ state, width, height }` |
| The app's root, always mounted, no space taken | `g.slot("overlay", Component)` | no props |
| Every pane's title bar | `g.slot("paneTitle", Component)` | `{ pane, tab }` |
| The working indicator (the dots before "Pondering…", a 20px row), drawn your own way; the user picks which one draws. Only a picture? Make it a sprite | `g.slot("loader", Component)` | `{ paused, className, fallback }` (`fallback`: gerdr's own dots) |
| The composer's bottom bar | `g.composerAction({ id, title, icon, run })` | `run(ctx)`: `ctx.text`, `ctx.insert()`, `ctx.setText()` |
| Right-click menu (desktop and Android App) | `g.contextMenu({ id, title, when, run })` | `{ selection, sessionId }` |
| The line under each finished turn | `g.turnStat(fn)` | `fn(info)` returns `{ className, title, after }` |

```tsx
export default defineAddon({
  setup(g) {
    const { useState, useEffect } = g.React;
    function Clock() {
      const [now, setNow] = useState(Date.now());
      useEffect(() => { const t = setInterval(() => setNow(Date.now()), 1000); return () => clearInterval(t); }, []);
      return <div className="px-3.5 py-1 text-[11px] text-muted-foreground">{new Date(now).toLocaleTimeString()}</div>;
    }
    return g.slot("panel", Clock);
  },
});
```

`g.ui` also has `Dialog` / `DialogContent` / `DialogHeader` / `DialogTitle` / `DialogDescription` / `DialogFooter` /
`DialogClose`. To open one from a command, draw it in the `"overlay"` slot (the panel is hidden while closed, and on a phone
only there while the drawer is open) and keep the open flag outside the component, so `run()` can set it:

```tsx
let open = false;
const subs = new Set<() => void>();
const setOpen = (v: boolean) => { open = v; subs.forEach((f) => f()); };
function Details() {
  const [, rerender] = g.React.useState(0);
  g.React.useEffect(() => { const f = () => rerender((n) => n + 1); subs.add(f); return () => { subs.delete(f); }; }, []);
  return (
    <g.ui.Dialog open={open} onOpenChange={setOpen}>
      <g.ui.DialogContent>…</g.ui.DialogContent>
    </g.ui.Dialog>
  );
}
g.slot("overlay", Details);
g.command({ id: "x.details", title: "Show details", run: () => setOpen(true) });
```

For a drawer, `g.ui.Sheet` / `SheetContent` (`side="right"` by default, also `left` / `top` / `bottom`) /
`SheetHeader` / `SheetTitle` / `SheetDescription` / `SheetFooter` / `SheetClose` work the same way and slide in from
the edge; widen it with `<SheetContent className="sm:max-w-md">`. For a one-line message, `g.notify` is enough.

Rules that keep it looking like Gerdr:

- Style with Tailwind classes; they work as written, nothing to configure.
- No literal colors. Use the theme's tokens so light and dark both work: `text-muted-foreground`, `bg-muted`,
  `bg-accent`, `border`, `text-brand` / `bg-brand`, `text-ok`, `text-warn`, `text-bad` (and `/10`-style opacity:
  `bg-warn/10`).
- Sizes follow the surroundings: 11–12px text in panel blocks and under turns, 13px in dialogs.
- Panel blocks: give yours horizontal padding only (`px-1.5`); the panel pads above and below once for all of them.
- Your add-on gets one rail button; its blocks stack in its panel. `icon` is a lucide name or a component drawn in a
  `{ width, height }` box (32×20 on the rail, 28×16 on a phone, clipped): live data belongs there (quota rings, a
  count), still, in theme tokens. Need more room? `tallIcon: true` makes your button two cells tall, its box 32×72
  (24×24 on a phone). Without an icon the button shows your addon.json icon.
- Want other add-ons' blocks behind the same button as yours (today's numbers next to two quota meters)? Copy their
  code into your add-on (add-ons are small) and tell the user to turn the originals off. Gerdr has no grouping on
  purpose: one add-on can line the rows up and draw one icon from all of the data, a grouping setting couldn't.
- A sprite (`g.slot("sprite")`) gets its box as `width` × `height`: 24×16 in the working indicator (`state`
  "working" / "waiting") and as small as 14×14 as an agent's icon (`state` "idle": keep it
  still, those are always in sight). Draw it to fit — at 14px a simpler version may read better — text 10px or more, red (`bg-bad` / `text-bad`) only for a warning. One picture per registration (`id`,
  `title`): the user may want a different one in each place.
- A phone has no hover: don't hide what matters in a `title` tooltip.
- `g.turnStat`'s `fn` runs on every render of the line and every 30s: keep it cheap. `info.latest` marks the newest
  turn, `info.now` is the time to compare against.

### Recolor

`g.theme({ light, dark })` overrides the theme's tokens themselves (the ones above, plus `background`, `foreground`,
`border`, `ring`…; the full list is `ThemeToken` in gerdr.d.ts), so everything drawn with them follows: user bubbles,
buttons, links. Give each mode its own value, or derive the dark one with a relative color. Pass a function to follow
a setting; it is called again when settings change:

```ts
g.theme(() => {
  const c = g.settings.get<string>("color"); // a "color" setting: "#e5569b"
  return { light: { brand: c }, dark: { brand: `oklch(from ${c} calc(l + 0.1) c h)` } };
});
```

Unloading the add-on puts Gerdr's colors back. Don't reach for `document.documentElement.style` instead: it misses
the light / dark switch and outlives the add-on.

### Data

| call | what |
|---|---|
| `g.state()` | Workspaces, tabs, panes, sessions (status, agent, title, cwd). |
| `g.usage(force?)` | The Claude Code / Codex quotas, as `/usage` shows them. |
| `g.turns({ since, until? })` | Every turn that ended in the range (unix ms), oldest first: session, agent, stop reason, whether the agent started it itself, duration, tokens, cost (USD; only Claude Code reports money). Gerdr keeps no daily totals: add them up yourself. |
| `g.settings.get(key)` | This add-on's [settings](#settings). |
| `g.storage.get/set` | Small private storage, kept in this browser / App (not shared between devices). A cache: store `{ at: Date.now(), data }` and refetch when it's older than you like. |
| `g.fetch(url, init)` | An HTTP request made by Gerdr's backend, so the API needs no CORS. The only way out: the page's own `fetch` is limited to Gerdr itself (CSP `connect-src 'self'`), CORS or not. Resolves `{ ok, status, headers, text, json() }`; a non-string `body` goes as JSON. |
| `g.asset(path)` | A URL for a file of the add-on's folder: `<img src={g.asset("logo.png")} />`, `new Audio(g.asset("done.wav"))`. |

### Settings

Declare them and Gerdr draws the form (Settings → Add-ons → your add-on → 设置) and stores the values, the same on
every device:

```json
"settings": [
  { "key": "ttlMinutes", "title": "缓存时长", "type": "number", "default": 60, "min": 1, "max": 1440, "unit": "分钟",
    "description": "Shown under the field." },
  { "key": "sound", "title": "提示音", "type": "boolean", "default": true },
  { "key": "style", "title": "样式", "type": "select", "default": "dot",
    "options": [{ "value": "dot", "label": "圆点" }, { "value": "text", "label": "文字" }] },
  { "key": "prefix", "title": "前缀", "type": "string", "default": "" },
  { "key": "phrases", "title": "短语", "type": "text", "default": "谢谢\n继续" },
  { "key": "color", "title": "颜色", "type": "color", "default": "#e5569b" },
  { "key": "token", "title": "令牌", "type": "secret", "default": "" }
]
```

Read with `g.settings.get<number>("ttlMinutes")` (the default until changed), at the moment you need it rather than
once in `setup`: a change applies at once, without reloading the add-on.

A `text` is a string of several lines, edited in a box under its title (a list, a table you parse yourself).

A `secret` is a string the form keeps masked (an eye button shows it) — for API tokens and keys. Only the display
differs: it is stored like any other setting (`~/.gerdr/addons-state.json`, not the add-on folder) and
`g.settings.get<string>("token")` returns it in the clear.

A `link` has no value: it is a button that opens a page, such as your add-on's control panel, shown in the form with
the other settings. An add-on may have several:

```json
{ "key": "panel", "title": "控制面板", "type": "link", "label": "打开", "url": "https://example.com/dashboard" }
```

When only your [service](#services) knows the address (a port it picked), leave `url` out and report it from the
service: `{"type":"status","text":"…","links":{"panel":"http://127.0.0.1:7858"}}`. The button is disabled until there
is an address, and while the service isn't running.

### Services

UI code runs in every open window (the App, a browser, the phone) and in none while no window is open. Work that has
to exist exactly once and keep running (holding a Bluetooth device, polling something) goes in a service: a process
Gerdr's backend starts, restarts when it dies (backing off to a minute) and stops when the add-on is switched off or
removed.

```json
"service": { "command": "./bin/bridge", "args": ["--quiet"], "env": { "K": "V" } }
```

`command` is a name on the login shell's PATH, an absolute path, or `./…` relative to the folder. It talks JSON lines:

- stdin: `{"type":"hello",…}` first, then `{"type":"settings","settings":{…}}` (your [settings](#settings), secrets in
  the clear — that's how a service gets its token; sent again whenever they change), `{"type":"sessions","sessions":[…]}`
  whenever an open session changes, `{"type":"message","data":…}` from `g.service.send(data)`, and
  `{"type":"event","event":{…}}` for each event matching a pattern in `"service": { …, "events": ["ci/*"] }`.
- stdout: `{"type":"status","text":"…"}` (shown in Settings; may carry `"links":{"<key>":"http://127.0.0.1:…"}`, the
  addresses of your [link settings](#settings) when the process serves those pages itself), `{"type":"message","data":…}` (to
  `g.service.on("message")` in every window), `{"type":"event","event":{"id":"…","kind":"message","key":"…","title":"…"}}`
  (an [event](#events): source defaults to your add-on's id). Other lines go to Gerdr's log; stderr to
  `~/.gerdr/logs/addons/<id>.log`.
- stdin closing means stop: let go of what you hold and exit within 3 seconds.
- Keep files in `$GERDR_ADDON_DATA`, never in the add-on's folder (writing there reloads every window, and an update
  replaces the folder).

### Events

An add-on that watches something outside (a chat, a mailbox, a dashboard) turns what it sees into **events** and
leaves what to do about them to whoever subscribed: a panel's `triggers/*.json` runs its own function, a session
watches with `gerdr event watch`, another add-on listens with `g.on("event")`. The shape is
`{ id, source, kind, key, title, text, url, data }`; give `id` a stable value (`slack:C123:1717000000.000100`) so
seeing the same thing twice emits it once, `kind` a dotted verb (`message`, `mention`), and `key` what subscribers
narrow and continue by: for a chat, the **conversation** (a DM, a thread), the same for a follow-up as for what it
follows, so a subscriber hands it to whatever took the first message. Emit from the service (stdout, above) when it holds a connection, or from the UI with
`g.events.emit`. To act on events yourself (send a reply), list patterns in `service.events` and read them on stdin.

Say what you emit in addon.json, so panels can offer it ("which events make a task") without knowing your add-on:

```json
"emits": [{ "kind": "dm", "title": "Slack 私信", "description": "…", "reply": "slack.send" }]
```

If someone can answer, put the address in each event: `"reply": { "kind": "slack.send", "data": { "channel": "D1",
"threadTs": "…" } }`, and list `"*/slack.send"` in `service.events`. A panel answers with `ctx.events.reply(event, text)`
and your service gets `{ "kind": "slack.send", "data": { "channel": "D1", "threadTs": "…", "text": "…" } }`.

## Debugging

- Settings → Add-ons → Installed: compile errors, what `setup` or a hook threw, a service's status and last error.
- The page's devtools console: errors are logged as `[gerdr addon <id>]`. In a browser, its devtools; in the
  desktop App, 开发者工具 (Cmd+Alt+I).
- `/// <reference path="../gerdr.d.ts" />` at the top of the entry, and `~/.gerdr/addons/tsconfig.json`, give an
  editor (or `tsc -p ~/.gerdr/addons`) the types.

## Publishing

A store is a GitHub repository whose folders (one or two levels down) holding an `addon.json` are add-ons; there is no
index to maintain. People add it in Settings → Add-ons → Store as `owner/repo` and install with a click; the store is
downloaded again only when they press refresh.

Before you push an add-on:

- `addon.json` with `name`, `version`, `description`, `icon`, and at least one entry in `screenshots`.
- A `README.md` in its folder: what it does, a screenshot, its settings. It shows under 说明 in the store; relative
  image paths work, and its leading `# Title` is dropped (the row already shows the name).
- Nothing written into the folder at runtime, no secrets.

Updates: when the store's copy of a folder differs from what was installed, the add-on shows 更新. If the person edited
the installed copy, Gerdr asks before overwriting it.

While writing a store, add its local folder as a store (an absolute path works) and install from it; or put the
add-on's folder straight into `~/.gerdr/addons`, where saving reloads it.
