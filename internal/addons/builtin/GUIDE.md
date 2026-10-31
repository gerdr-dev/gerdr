# Writing gerdr add-ons

An add-on changes gerdr itself: what the sidebar shows, the names of tabs and panes, a button in the composer, the
line under each turn, commands and keys, a process that keeps running. It is a folder of TypeScript; gerdr compiles it
on save (no Node, no build step) and runs it in the page.

Add-ons are not Claude Code or Codex plugins. Those extend the agent (skills, MCP, hooks on its tool calls) and run
inside one agent process; an add-on extends the window around all of them, every session and every agent at once.

This guide is installed as `~/.gerdr/addons/GUIDE.md` next to `gerdr.d.ts`, the exact API with a comment on every
call. Both are rewritten on every gerdr start: read them, don't edit them. Working examples:
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
  (`import { x } from "./lib"`); everything is bundled into one module. npm packages can't be imported: React, gerdr's
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
g.on("stats", () => …);          // today's numbers changed (see g.stats)
```

`g.state()` is a read-only snapshot of workspaces, tabs, panes and sessions. `g.actions` does things: `newTab`,
`duplicateTab`, `split`, `focusPane`, `renameTab`, `renameWorkspace`, `send(sessionId, text)` (sends a message),
`insertText(sessionId, text)` (types into its composer without sending), `runCommand`. `g.notify(text, { system })`
shows a toast or a system notification.

### Draw

Name the file `.tsx` and write JSX. Hooks come from `g.React` (the React that mounts your components: never import
your own). `g.ui` has gerdr's building blocks (`Button`, `Popover`, `Tooltip`, `Switch`, `cn`), `g.icons` every
lucide icon (`<g.icons.Timer className="size-3.5" />`).

| where | call | gets |
|---|---|---|
| Sidebar, above its bottom row | `g.slot("sidebar", Component, { order })` | no props |
| Every pane's title bar | `g.slot("paneTitle", Component)` | `{ pane, tab }` |
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
    return g.slot("sidebar", Clock);
  },
});
```

Rules that keep it looking like gerdr:

- Style with Tailwind classes; they work as written, nothing to configure.
- No literal colors. Use the theme's tokens so light and dark both work: `text-muted-foreground`, `bg-muted`,
  `bg-accent`, `border`, `text-brand` / `bg-brand`, `text-ok`, `text-warn`, `text-bad` (and `/10`-style opacity:
  `bg-warn/10`).
- Sizes follow the surroundings: 11–12px text in the sidebar and under turns, 13px in panels.
- Sidebar blocks: give yours horizontal padding only (`px-1.5`); the slot pads above and below once for all of them.
- A phone has no hover: don't hide what matters in a `title` tooltip.
- `g.turnStat`'s `fn` runs on every render of the line and every 30s: keep it cheap. `info.latest` marks the newest
  turn, `info.now` is the time to compare against.

### Data

| call | what |
|---|---|
| `g.state()` | Workspaces, tabs, panes, sessions (status, agent, title, cwd). |
| `g.usage(force?)` | The Claude Code / Codex quotas, as `/usage` shows them. |
| `g.stats(days?)` | Per day: turns finished, and money spent (USD by agent; only Claude Code reports money). |
| `g.settings.get(key)` | This add-on's [settings](#settings). |
| `g.storage.get/set` | Small private storage, kept in this browser / App (not shared between devices). |
| `g.asset(path)` | A URL for a file of the add-on's folder: `<img src={g.asset("logo.png")} />`, `new Audio(g.asset("done.wav"))`. |

### Settings

Declare them and gerdr draws the form (Settings → Add-ons → your add-on → 设置) and stores the values, the same on
every device:

```json
"settings": [
  { "key": "ttlMinutes", "title": "缓存时长", "type": "number", "default": 60, "min": 1, "max": 1440, "unit": "分钟",
    "description": "Shown under the field." },
  { "key": "sound", "title": "提示音", "type": "boolean", "default": true },
  { "key": "style", "title": "样式", "type": "select", "default": "dot",
    "options": [{ "value": "dot", "label": "圆点" }, { "value": "text", "label": "文字" }] },
  { "key": "prefix", "title": "前缀", "type": "string", "default": "" }
]
```

Read with `g.settings.get<number>("ttlMinutes")` (the default until changed), at the moment you need it rather than
once in `setup`: a change applies at once, without reloading the add-on.

### Services

UI code runs in every open window (the App, a browser, the phone) and in none while no window is open. Work that has
to exist exactly once and keep running (holding a Bluetooth device, polling something) goes in a service: a process
gerdr's backend starts, restarts when it dies (backing off to a minute) and stops when the add-on is switched off or
removed.

```json
"service": { "command": "./bin/bridge", "args": ["--quiet"], "env": { "K": "V" } }
```

`command` is a name on the login shell's PATH, an absolute path, or `./…` relative to the folder. It talks JSON lines:

- stdin: `{"type":"hello",…}` first, then `{"type":"sessions","sessions":[…]}` whenever an open session changes, and
  `{"type":"message","data":…}` from `g.service.send(data)`.
- stdout: `{"type":"status","text":"…"}` (shown in Settings), `{"type":"message","data":…}` (to
  `g.service.on("message")` in every window). Other lines go to gerdr's log; stderr to `~/.gerdr/logs/addons/<id>.log`.
- stdin closing means stop: let go of what you hold and exit within 3 seconds.
- Keep files in `$GERDR_ADDON_DATA`, never in the add-on's folder (writing there reloads every window, and an update
  replaces the folder).

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
the installed copy, gerdr asks before overwriting it.

While writing a store, add its local folder as a store (an absolute path works) and install from it; or put the
add-on's folder straight into `~/.gerdr/addons`, where saving reloads it.
