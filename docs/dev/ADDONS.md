# Add-ons

What used to be "plugins": code that changes Gerdr itself (tab / pane / workspace names, commands and keys, session
events, a process that keeps running). Code: `internal/addons` (backend), `internal/server/addonapi.go` (HTTP),
`ui/src/lib/addons.ts` (runtime), `ui/src/components/AddonSettings.tsx` (Settings → Add-ons). The API an add-on
sees: `internal/addons/builtin/gerdr.d.ts`, copied into `~/.gerdr/addons` on every start.

## Why not "plugin", and why not Claude Code's mods

- Claude Code and Codex both have **plugins** (skills, MCP, hooks of the agent), and Settings already lists them
  (`ext.ClaudePlugins`). A third thing called plugin was confusing, so: add-on.
- Claude Code's **mods** (function-hook plugins, `.claude-plugin/plugin.json` + `hooks/register.tsx`) were looked at
  (2026-10, Claude Code 2.1.314) and are a different layer. A mod runs inside **one** claude process: it can rewrite
  tool calls, prompts, the system prompt and transcript rows, and draw panes beside that one chat. It can't see
  Gerdr's tabs / workspaces, other sessions, or Codex; and its processes (`$.process.spawn`) live as long as the
  session — N sessions, N copies — so the one-connection wristband service can't be a mod. Drawing a mod's UI in
  Gerdr would mean speaking the `ui_attach` / `ui_render` / `ui_press` control requests on stream-json, all marked
  `@internal` and changing per release. Decided: borrow their shape (a folder with a manifest, owner/repo stores),
  don't be compatible. Mods a user installs with `claude plugin install` still run their non-UI hooks in Gerdr's
  claude sessions, since Gerdr drives the real CLI.

## Drawing and data

- `g.slot("sidebar" | "rail" | "paneTitle", Component)`, `g.composerAction`, `g.contextMenu` (ui/src/components/AddonSlot.tsx,
  AppContextMenu.tsx). Add-on JSX compiles to the globals `__gerdrH` / `__gerdrFragment` (React.createElement /
  Fragment, set in lib/addons.ts) — not `gerdr.h`: the desktop shell owns `window.gerdr`. Hooks must come from
  `g.React` (the same React that mounts them); `g.ui` hands out Gerdr's shadcn pieces and `g.icons` all of lucide, so
  an add-on looks native without bundling its own copies. Each slot entry sits in its own error boundary.
- `g.slot("loader", C)` replaces the working indicator (PixelLoader: the dots before "Pondering…", a pane starting an
  agent). The package gets it through `ChatHost.renderLoader`, which hands the host the whole indicator with the
  default as `fallback` rather than a colour or a frame, so the next host's idea (a mascot, a spinner of its own) needs
  no new package release. Gerdr's AddonLoader draws only the first registration and falls back to the dots when it
  throws: a running turn must still look like it runs.
- `g.turnStat` decorates the line under each finished turn through `ChatHost.useTurnStat`, a **hook** rather than a
  plain callback: answers depend on the time (cache-clock colors by prompt-cache age), so Gerdr's implementation
  (`useAddonTurnStat`) re-renders every 30s while any add-on uses it. `latest` (the newest turn's line) is worked out in
  `buildRows`, since only the chat's transcript knows which turn is last.
- `g.theme` writes one `<style id="gerdr-addon-theme">` from all registrations (lib/addons.ts `applyThemes`). The
  selectors are `html:root:not(.dark)` / `html:root.dark`: higher specificity than index.css's `:root` / `.dark`, so
  load order doesn't matter, and a light-only override can't win in dark mode the way a plain `html:root` would.
  Only token names (`[a-z0-9-]`) and values without `;{}` get in. A function spec is re-run at the end of every
  `doReload`: a settings change broadcasts "addons" but doesn't change the hash, so no add-on re-runs `setup`.
- Settings: addon.json `settings` (number / string / text / boolean / select / color / secret) become a form under the add-on in Settings →
  Add-ons; values live in addons-state.json (`settings[id][key]`, only keys changed from the default), not in the
  browser's localStorage like `g.storage`, so every window and the phone agree. `g.settings.get` reads the latest
  `/api/addons` build each call, so a change applies without reloading the add-on (its hash doesn't include values).
- Drafts are keyed by **pane**, not session: `composerBar(session, { paneId })` passes the key, and
  `g.actions.insertText(sessionId)` writes into the focused pane showing that session.
- The context menu only exists in the desktop and Android Apps (a browser keeps its own), so `g.contextMenu` items
  show there only.
- `"overlay"` is mounted once in App.tsx next to the app's own dialogs: an add-on's command needs somewhere to draw
  a dialog that exists whatever the layout is (the "sidebar" panel is hidden while closed, on a phone only there
  while the drawer is open).
- `"sidebar"` entries are blocks and `"rail"` entries are button faces; which blocks share a rail button, in what
  order, and the button's face (a built-in icon, typed characters, or any add-on's face) is the user's, in config
  `rail.groups` (lib/rail.ts, Settings → 扩展 → 分组 = RailSettings.tsx, buttons in RailGroups.tsx). Not the add-ons':
  blocks that belong together often come from different authors (today + two quota meters). Groups refer to blocks
  by a key stable across reloads ("addon:id", the add-on's own id or its n-th entry in the slot). No config = one
  button with every block; a block no group lists joins the first group; keys of unloaded blocks keep their place.
  Each rail panel is kept mounted while closed (portal + `hidden`), as the blocks were in the old sidebar, so timers
  and fetched state survive closing it; the phone's sheets mount on open. A face gets a 36×28px box, clipped, so an
  add-on can draw anything without pushing the rail around. History: the rail replaced the left column's bottom
  strip (docs/dev/ARCHITECTURE.md, "The rail"), first with one shared button, then these groups when the user wanted
  three add-ons in one button and a "¥" on it.
- `g.fetch` is POST /api/addons/{id}/fetch (internal/server/addonfetch.go): the backend makes the request. A plain
  fetch() to a third-party API can't work: the page's CSP is `connect-src 'self'`, which blocks it even when the API
  sends CORS headers (the SubHub add-on shipped a CORS fallback that never ran for that reason), and add-on authors
  usually can't change the API anyway. It grants nothing new — add-ons are trusted code that can already run a service — and it sits behind the
  same auth as every /api call. Bodies are text both ways; an error status is data (like fetch()), only a transport
  failure is a 502. 30s timeout, 10 MB cap, no cookie jar.
- `g.usage()` is /api/usage (the quotas). `g.turns()` is /api/turns (internal/hub/turns.go): one record per finished
  turn — session, agent, stop reason, auto, duration, tokens, cost — appended to `~/.gerdr/turns/<yyyy-mm>.jsonl`
  and broadcast as "turn" (`g.on("turn")`). Gerdr keeps **facts, not tallies**: what counts as a task done, which day
  a turn belongs to, whether to price Codex's tokens, is the add-on's call (the `today` add-on sums a day's). It used
  to keep daily totals itself (stats.go, `g.stats`, until 2026-11): that put a definition of "done" in the core
  (every end_turn, a quarter of which were the agent waking itself after a background task) and the per-process cost
  arithmetic in the hub, guessing process restarts from the running total going down. The log is kept in the core,
  not an add-on, because only the hub sees each turn exactly once: a UI add-on runs in every open window and in none
  while no window is open, and a service would need a runtime to be shipped with it.
- Cost: Claude Code's `result.total_cost_usd` is a running total per process. The adapter keeps the last one on its
  `proc` (agents/claude/proc.go `costDelta`) and reports the growth as `_meta.gerdr.cost = {usd}`; the hub sums the
  reports that arrive during a turn onto its `turn_end` (`costUsd`; acp-go-sdk processes notifications before the
  response that follows them, so they're in before the turn ends). Codex reports tokens, not money.
- Tokens: Gerdr Agent reports every model call as `_meta.gerdr.tokens = {model, input, output, cacheRead, cacheWrite}`
  (acpx.TokensKey; sub-agents and compaction on the small model included, each under its own model key), and the hub
  sums them per model onto the turn record (`tokens`, not the session log). No prices in the core: they change, and a
  relay charges its own, so the `today` add-on keeps a price table in its settings and converts. Before
  this, every Gerdr Agent turn showed $0 — it had the usage but nothing turned it into money.
- The sidebar's quota meter used to be built in (UsageMeter); it is the `usage-meter` add-on now, and "today" (tasks
  done, cost) is the `today` add-on. Without them the sidebar has neither — install from the store.

## Languages

- addon.json's `i18n` ({locale: {name, description, settings: {key: {title, description, unit, options}}}}) is passed
  through untouched (`Manifest.I18n` → `Addon.I18n` / `StoreAddon.I18n`); the UI lays the entry for its language over the
  base texts once, in api.ts (`localizeAddon`), so Settings, the store and the rail's block names all read translated
  names without each knowing about it. The backend doesn't pick: it doesn't know the language (each device has its
  own, ui/src/lib/i18n.ts), and the same /api/addons answers a phone in English and a computer in Chinese.
- A separate `i18n` object, not `"name": {"en": …}`: every existing addon.json and every reader of `name` as a string
  keeps working, and an add-on is translated by adding one block instead of reshaping every field.
- README.<locale>.md files are read into `StoreAddon.Readmes`; the store shows the one for the interface's language.
- `g.locale()` is the interface's language. It never changes under a running add-on: a language change reloads the
  page (lib/i18n.ts says why), so an add-on reads it once in setup.

## Layout on disk

```
~/.gerdr/addons/<id>/addon.json   optional manifest: name, version, description, icon, main, service
~/.gerdr/addons/<id>/index.ts     UI half (or "main"); bundled by esbuild with everything it imports
~/.gerdr/addons/gerdr.d.ts        API declarations + tsconfig.json + README.md (refreshed on start)
~/.gerdr/addons-state.json        switched-off ids, install records (store, version, hash), the store list
~/.gerdr/addon-data/<id>/         a service's own files (GERDR_ADDON_DATA)
~/.gerdr/logs/addons/<id>.log     a service's stderr
~/.gerdr/cache/addon-stores/<key>/repo   a store's last download
```

- The id is the folder name. `_x` folders are skipped (switching off by hand); the UI's switch writes the state file
  instead, so a store update doesn't have to know about renamed folders.
- The state file is **outside** the add-on directory: the watcher reloads every window on any change in there, and
  a service writing its data there would do that once a second. The watcher does include the state file's mtime, so
  switching an add-on off unloads it everywhere.
- Top-level `.ts` files aren't add-ons any more. `addons.Migrate` turns the old `~/.gerdr/plugins` into folders once
  (`foo.ts` → `foo/index.ts` with its `/// <reference>` pointed one level up, `_foo` → switched off, `plugin.json` →
  `addon.json`) and leaves the old directory as `plugins.migrated`. `definePlugin` stays as an alias of `defineAddon`,
  and `g.storage` keeps its old `gerdr.plugin.<id>.` localStorage prefix, so migrated add-ons keep working and keep
  their data.
- Files an add-on ships are served at `/api/addons/<id>/files/<path>` (`g.asset(path)`), through the same
  `serveRaw` as the file viewer (sandboxed CSP, no HTML as a page), refusing paths that leave the folder.

## Stores

A store is a GitHub repository (`owner/repo`, optionally `#ref`) or, for writing one, an absolute folder path. Its
folders (one or two levels down) holding an `addon.json` are its add-ons; no index file to keep in sync.

- Refresh = download `https://codeload.github.com/<owner>/<repo>/tar.gz/<ref|HEAD>` and unpack into the cache
  (regular files only, ≤100MB, nothing outside the folder; executable bits kept so a service script still runs).
  codeload has no API rate limit, and one request gets every icon and README, so browsing is offline. Only a store
  never downloaded is fetched by itself (an empty default store would look broken); after that only the refresh
  button. A failed refresh keeps the last download and shows why.
- Private repositories don't work yet (no credentials are sent).
- Install copies the folder from the cache to `<addons>/.install-*` (dot names are skipped by the scanner), then
  swaps it in, so no window ever loads half an add-on. The install record keeps `hashDir` of what was copied:
  - an update is offered when the store's hash differs from that (not the version string: a store may forget to bump it);
  - installing over a folder that has no record (someone made it) or whose hash changed (edited after installing)
    is a 409 `conflict`, and the UI asks before sending `force`.
- Uninstall deletes the folder, keeps `addon-data/<id>`.
- The default store is `gerdr-dev/gerdr-addon`; removing it sticks (the state file stores `stores: []`).
- An add-on in a store should have `screenshots` in addon.json (shown as thumbnails in the store list) and a
  README.md; relative image paths in the README are served from the store's copy.
- `gerdr addon` (cmd/gerdr/addoncmd.go) does all of this from the command line for agents (the gerdr skill teaches
  it). It works on the files through the same `Manager` (with gerdr stopped too); the running gerdr's watcher picks
  the add-on directory and the state file up, but store list changes reach an open store tab only on reopening it
  (`addon_stores` is broadcast from the HTTP API only). `Manager.Describe` lists without building; build errors and
  service status come from the running gerdr's /api/addons when there is one.
- The add-ons that used to ship inside the binary (duplicate-tab, notify-when-done, session-title) moved there.
  Users who had them keep them through the migration; new users install them from the store.
