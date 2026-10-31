# Add-ons

What used to be "plugins": code that changes gerdr itself (tab / pane / workspace names, commands and keys, session
events, a process that keeps running). Code: `internal/addons` (backend), `internal/server/addonapi.go` (HTTP),
`ui/src/lib/addons.ts` (runtime), `ui/src/components/AddonSettings.tsx` (Settings → Add-ons). The API an add-on
sees: `internal/addons/builtin/gerdr.d.ts`, copied into `~/.gerdr/addons` on every start.

## Why not "plugin", and why not Claude Code's mods

- Claude Code and Codex both have **plugins** (skills, MCP, hooks of the agent), and Settings already lists them
  (`ext.ClaudePlugins`). A third thing called plugin was confusing, so: add-on.
- Claude Code's **mods** (function-hook plugins, `.claude-plugin/plugin.json` + `hooks/register.tsx`) were looked at
  (2026-10, Claude Code 2.1.314) and are a different layer. A mod runs inside **one** claude process: it can rewrite
  tool calls, prompts, the system prompt and transcript rows, and draw panes beside that one chat. It can't see
  gerdr's tabs / workspaces, other sessions, or Codex; and its processes (`$.process.spawn`) live as long as the
  session — N sessions, N copies — so the one-connection wristband service can't be a mod. Drawing a mod's UI in
  gerdr would mean speaking the `ui_attach` / `ui_render` / `ui_press` control requests on stream-json, all marked
  `@internal` and changing per release. Decided: borrow their shape (a folder with a manifest, owner/repo stores),
  don't be compatible. Mods a user installs with `claude plugin install` still run their non-UI hooks in gerdr's
  claude sessions, since gerdr drives the real CLI.

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
- The add-ons that used to ship inside the binary (duplicate-tab, notify-when-done, session-title) moved there.
  Users who had them keep them through the migration; new users install them from the store.
