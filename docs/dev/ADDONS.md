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

- `g.slot("panel" | "sprite" | "paneTitle", Component)`, `g.composerAction`, `g.contextMenu` (ui/src/components/AddonSlot.tsx,
  AppContextMenu.tsx). Add-on JSX compiles to the globals `__gerdrH` / `__gerdrFragment` (React.createElement /
  Fragment, set in lib/addons.ts) — not `gerdr.h`: the desktop shell owns `window.gerdr`. Hooks must come from
  `g.React` (the same React that mounts them); `g.ui` hands out Gerdr's shadcn pieces and `g.icons` all of lucide, so
  an add-on looks native without bundling its own copies. Each slot entry sits in its own error boundary.
- `g.slot("loader", C)` replaces the working indicator (PixelLoader: the dots before "Pondering…", a pane starting an
  agent). The package gets it through `ChatHost.renderLoader`, which hands the host the whole indicator with the
  default as `fallback` rather than a colour or a frame, so the next host's idea (a mascot, a spinner of its own) needs
  no new package release. Which one draws is config `loader`, picked in Settings → General → Loading 动画
  (LoaderSelect.tsx, AddonSlot.tsx `loaderPick`): gerdr's dots, another built-in (components/Loaders.tsx: seven,
  so there's a choice without any add-on; they live in the app, not the chat package, so adding one costs no npm
  release), a "loader" entry, or a sprite. A Select row among the looks: it first was a gallery page of its own
  (扩展 → 素材), which read as one more concept to learn. Unset = the first "loader" entry, else the dots: an add-on whose whole point is the
  indicator works once installed. A pick whose add-on is gone, or one that throws, falls back to the dots: a running
  turn must still look like it runs. The Select previews each option through the real PixelLoader with a
  `LoaderPreview` context overriding the choice, so the dots come from the package without it exporting them.
- `"sprite"` entries are pictures gerdr draws only where the user put them: the working indicator and agents' icons
  (AgentDef.icon "sprite:<key>", AgentIcon.tsx; the default icon while its add-on isn't loaded). Props
  `{ state, width, height }`: "working" / "waiting" in the indicator (24×16, unclipped), "idle" as an icon (FitSprite:
  the box comes from size classes, so it's measured first). Sizes are the sprite's business, not gerdr's: it gets the
  box and decides how to fill it. Not animated as an agent's icon for now — a list of tabs all moving is loud;
  "working" there per session state is the obvious next step. Why a slot of its own: clawd-loader first was both a
  loader and a rail face, so wanting Clawd only on a button took a "don't replace the dots" switch, and both places
  had to show the same costume. With sprites an add-on brings pictures and the user places them, one per place; the
  old `"rail"` slot (Nov 2026, a few days) went into it — `g.slot("rail")` now throws "没有这个插槽". Sprites were rail
  button faces too while the rail had user-made groups; a button's icon is its add-on's own now (`"panel"` below).
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
- A select's options can be worked out at run time: `optionsFrom: {events, reply}` lists the event sources whose
  switched-on add-ons declare a matching kind in `emits` (internal/addons/state.go `resolve`, applied in both the
  listing and `SettingsOf`, so the form, `g.settings.get` and a service's settings line agree). `required` = no empty
  choice: no pick, or a pick whose add-on is gone, reads as the first option, while the stored pick is kept (it comes
  back with its add-on). For remote-answer's "which chat" (2026-11): first it went to wherever the person last sent a
  dm, which nobody could see or set; the user wanted a dropdown of the chat add-ons, never empty. Filled by Gerdr from
  the manifests rather than reported by the service like `links`: the form must work while the service is stopped,
  and the service has no view of what else is installed. If an add-on ever needs options only it can know, that is
  the next source to add (the status line, as `links` does).
- A `link` setting has no value: it is a button opening a page (an add-on's control panel), a row of the settings
  form like the others (it was first drawn outside the folded form, always in sight; the user wanted every setting
  in one place). Its address is addon.json's `url`, or what the service reports in its status line's
  `links` by key (ServiceStatus.Links), for a page on a port only the process knows (wristkit's settings page). A
  setting rather than one URL on the service, so an add-on can have several, and a setting, rather than a field of
  its own, because the user looks for them where the add-on's other options are. First tried (Nov 2026) as a single
  `url` on the status line drawn as an "Open" link after the status text: one link per add-on, and lost in that line.
  Only http(s) addresses are kept: the UI puts them in an `<a href>`. The desktop App opens them in the browser.
- Drafts are keyed by **pane**, not session: `composerBar(session, { paneId })` passes the key, and
  `g.actions.insertText(sessionId)` writes into the focused pane showing that session.
- The context menu only exists in the desktop and Android Apps (a browser keeps its own), so `g.contextMenu` items
  show there only.
- `"overlay"` is mounted once in App.tsx next to the app's own dialogs: an add-on's command needs somewhere to draw
  a dialog that exists whatever the layout is (a "panel" block is hidden while its panel is closed, on a phone only there
  while the drawer is open).
- `"panel"` entries are blocks (`"sidebar"`, the slot's first name from when they sat in the left column, still maps
  to it: add-ons in the wild use it). Each add-on with blocks gets one rail button, its blocks stacked in the panel;
  the button's title, icon (a lucide name, or a component drawn in a 32×20 box, 28×16 on a phone; `tallIcon` makes the button two cells
  tall, a 32×72 box, 24×24 on a phone — three quota rings were cramped in one cell) and hover / click
  opening come from the add-on's slot options (lib/rail.ts `railButtons`, RailAddons.tsx). Buttons are in slot order.
  Each rail panel is kept mounted while closed (portal + `hidden`), as the blocks were in the old sidebar, so timers
  and fetched state survive closing it; the phone's sheets mount on open. The icon box is clipped, so an add-on can
  draw anything without pushing the rail around.
  History: the rail replaced the left column's bottom strip (docs/dev/ARCHITECTURE.md, "The rail"), first with one
  shared button; then (Nov 2026) user-made groups — Settings → 扩展 → 分组 dragged blocks of several add-ons behind one
  button, with a face picked from built-in glyphs, typed characters or a sprite — because the user wanted three
  add-ons (today + two quota meters) in one button with a "¥" on it. Dropped a few days later for one button per
  add-on: add-ons are small source code an agent writes, so "these three together" is one add-on with their code
  copied in. That also does what groups couldn't (rows lined up across sources, an icon drawn from all of the data:
  three quota rings) and leaves no settings page to keep. `config.rail` from then is ignored.
- `g.fetch` is POST /api/addons/{id}/fetch (internal/server/addonfetch.go): the backend makes the request. A plain
  fetch() to a third-party API can't work: the page's CSP is `connect-src 'self'`, which blocks it even when the API
  sends CORS headers (the SubHub add-on shipped a CORS fallback that never ran for that reason), and add-on authors
  usually can't change the API anyway. It grants nothing new — add-ons are trusted code that can already run a service — and it sits behind the
  same auth as every /api call. Bodies are text both ways; an error status is data (like fetch()), only a transport
  failure is a 502. 30s timeout, 10 MB cap, no cookie jar.
- `g.messageAction` + `g.audio` (2026-11, asked for: tap a message, hear it read aloud). First built as `g.tts`, a
  voice registry: the add-on turned text into audio and Gerdr did the button and the playing. Replaced before release:
  the next wishes (translate a message, summarize it) would each have needed a primitive of their own. Split instead by
  **place** and **capability**: the place is "a button on a message" (`g.messageAction`; Gerdr owns where it sits and
  how a run goes: spinner, second tap aborts `ctx.signal`, errors), capabilities are added one by one (`g.audio`
  now; showing a result under the message, for translation, when that is wanted — it needs thought: the list is
  virtual, so a result must outlive its row). Rule used: a registry ("add-on registers, Gerdr calls") only when Gerdr
  itself needs the capability, like `g.sessionTitle`; nothing in Gerdr speaks on its own. Several TTS add-ons are then
  just several actions, behind one "⋯" per message (components/chat/MessageActions.tsx), with a running one pulled
  out so its stop stays one tap away.
- The button reaches the chat through `ChatHost.messageActions`, a general "the host's buttons on a message" hook, so
  the chat package knows nothing about add-ons or sound. Where: beside a user's bubble (with rewind) and at the end of a
  reply's turn line (a reply is several rows of the virtual list; its text is the turn's text parts, joined only when
  read). Long-press was the first idea; on Android a long press is the system's text selection, and taking it over (or
  adding to the selection bar natively) was turned down for a visible button.
- `g.audio` (lib/audio.ts) is one `<audio>` element for everything: one sound at a time, and the autoplay rules handled
  once — a message action's tap plays a silent clip on it, since Android's WebView refuses sound not started by a
  gesture and an add-on's audio arrives long after it. Clips may come as an iterable; the next is pulled when one starts
  playing, so a generator synthesizes ahead. A play() given a run's `ctx.signal` tells that run's button that sound is
  on (stop square instead of spinner): linked by the signal, so `g.audio` needs no notion of messages.
- `g.fetch(…, { binary: true })` answers base64 (`bytes()` / `blob()`): many TTS APIs answer with audio, which a JSON
  string can't carry.
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

## Answering for the person (services with "pending": true)

- Asked for (2026-11): get what agents wait on to a chat (Feishu, Slack), answer it there, and have the agent go on.
  Two halves, kept apart so neither knows the other: a chat add-on (slack) brings messages in as events and posts
  replies (`reply` addresses, docs/dev/EVENTS.md); `remote-answer` (gerdr-addon repo) holds the requests and talks to
  whichever chat the person last wrote from. A Feishu add-on that emits `dm` events with a `reply` works with it unchanged.
- Gerdr's part is `internal/server/addonpending.go`: the pending list (hub.Pending, the ACP request taken apart into
  tool / options / questions, so an add-on needn't know ACP) sent to services whose addon.json says `"pending": true`,
  and `{"type":"answer"}` checked against the request (an option id it offered; every question answered, only its
  labels unless allowOther) before `Session.Answer` — the same response shapes the UI's cards send.
- Why opt-in, and why not elsewhere: answering approves commands. On the event bus, anything that can emit (any panel
  function, `gerdr event emit`) could approve; in `gerdr session`, an agent could approve its own requests (that CLI
  deliberately says "let the user handle it in the UI"). An add-on gets it because the person installed one that
  asks for it, the same trust as running its service at all.
- First thought was a UI hook that vetoes notifications ("quiet") for do-not-disturb; dropped for this, which hands
  the whole "where do requests go and who answers" to add-ons. Do-not-disturb stays built in (Workspace.muted) and
  shows up here as `muted` on each entry.

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
  Gerdr itself ships none. Old versions seeded them into `~/.gerdr/plugins` on first start; the migration drops any
  file still byte-identical to a seeded version (`seeded` in migrate.go), so they don't reappear as installed add-ons
  after an upgrade. Edited copies are migrated; anyone who wants the originals installs them from the store.
