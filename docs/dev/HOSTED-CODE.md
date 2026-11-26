# A panel's own Code (CodeView list="replace")

A panel can show Code in its place (`@gerdr/core` `CodeView`): Gerdr's own panes, tabs, splits, terminals and keys,
beside a list the panel draws. With `list="replace"` (the Superagent panel: the secretary and the tasks by status) the
panel also has **panes of its own**, completely apart from Code mode's: its own data, as its tasks and its agents are.

## Why apart

At first there was one layout and each side only filtered its list. Whatever Code had in front stayed in front in
the panel (titled 不是任务), workers' tabs were put into Code's workspaces and hidden there, a split could mix both.

The second try (adc4ecb, 2026-11-26) kept the panel's workspaces inside Code's layout.json, marked with the panel, and
had every device move panes to the side it thought they belonged to. Whether a panel had panes of its own was only
known on a device where the panel had been opened, and the layout is shared by every device: a phone or another
window moved the panel's panes into Code, the device showing the panel moved them back, and in the tug a
conversation just started from the panel lost its pane (开始 seemed to do nothing). **Never let a device's own idea of
whose a pane is decide what is written to shared data.**

So now the two are separate all the way. A conversation belongs to exactly one side, by itself: a panel's carry
`tags.panel`; importing a Code conversation into the panel starts a new one that carries it on (`continueFrom`), it
doesn't move the original. Nothing moves panes between the sides.

## How

- **Stored apart.** Code's panes are layout.json; a panel's are `<panel dir>/user/layout.json` (same shape), so
  renaming, removing and restoring the panel take them along (`internal/work/panellayout.go`). API: `GET
  /api/panel-layouts`, `PUT /api/panel-layout {key, data, client}`, the `panel_layout` push; hello carries
  `panelLayouts`. The file existing is what tells every device the panel has panes of its own.
- **Held apart in the UI** (`ui/src/lib/store/layoutSync.ts`). `layout` in the store is the layout on screen,
  `layoutOwner` whose it is ("" = Code's, else the panel key), `stashed` the others. Nothing merges them. What is
  about all projects (the project list, switching projects, every project's dot on the rail) reads Code's:
  `codeLayout()`; `spaces.ts` acts on it with `setLayoutOf("", …)` even while a panel's panes are on screen.
  `setLayoutOf(owner, …)` changes a layout that isn't on screen (an agent opening a conversation beside itself).
- **The screen** (`ui/src/lib/hostView.ts`). Switching to a panel with its own list puts its layout on screen (made
  and saved the first time: one workspace in its project); Code mode, another project or a panel that adds to Code's
  list brings Code's back. Each layout keeps where you were in it (per device, `gerdr.view:<owner>`).
- **Which side a conversation opens in**: `ownerHost` — a panel's (`tags.panel`) when that panel has panes of its own,
  else Code's. `focusSession`, `addTabForSession`, `openSessionNear` and the panel's `showInCodeView` go to that side
  first, then act on its layout.
- **The server reads both** (`internal/server/panellayout.go`): `LayoutAll` is Code's layout plus every panel's
  workspaces, marked, for what asks "which panes are there" (a session's project, do-not-disturb, Browser panes still
  shown, `gerdr pane …`, whose result `setLayoutAll` writes back to the file each workspace came from). A conversation a
  panel starts with `workspace` set goes into the panel's own layout (`placeSessionWith`).
- **Old data**: `MigrateHostWorkspaces`, at start-up and on every save of layout.json while a device still runs an old
  UI: workspaces marked for a panel move to its file, and a panel's conversations sitting as tabs in Code's workspaces
  (the first design) move to the panel's layout or go if it already shows them.
- `useCodeRows()` gives the panel its own panes (on screen or not); `useCodeRows("code")` Code mode's (what 从 Code
  导入 offers).

Only `list="replace"` panels have panes of their own. A `list="add"` panel draws its part above Code's own list and
shows Code's layout; a panel that doesn't show Code opens its conversations in Code ("open in Code"), as before.

## Upgrading

The UI is synced before the backend restarts onto a new binary. Against a backend without `panelLayouts` it keeps the
one-layout behaviour and saves nothing to the new endpoint; when the backend comes back with them the page reloads once.
