# A panel's own Code (CodeView list="replace")

A panel can show Code in its place (`@gerdr/core` `CodeView`): Gerdr's own panes, tabs, splits, terminals and keys,
beside a list the panel draws. With `list="replace"` (the Superagent panel: the secretary and the tasks by status) the
panel also has **a layout of its own**, kept apart from Code mode's.

## Why apart (2026-11-26)

Until then there was one layout and each side only filtered its list: Code's list left out panel conversations
(`tags.panel`), the panel's left out Code's. Everything else was shared, and it showed:

- the pane in front was whatever the other side last had in front: open a conversation in Code, switch to Superagent,
  and it was still on screen there, titled 不是任务;
- a worker's tab was put into Code's workspaces (a new workspace per repository it touched), which Code then hid;
- one split could hold a task and a Code conversation; ⌘1–9, closing tabs and the tab history crossed over.

Sharing was chosen at first so that a conversation never has two panes (two composers, two drafts) and a terminal
never two views of one PTY. That holds without sharing: **a conversation belongs to exactly one side.** A panel's
conversations carry `tags.panel`; importing a Code conversation into the panel starts a new one that carries it on
(`continueFrom`), it doesn't move the original. So each side can have its own panes without any pane in both.

## How

- `Workspace.host` (`ui/src/lib/layout.ts`): unset = Code mode's, else the panel's key (`<project>/<panel>`). All of it
  stays in the one stored layout, so whatever walks the whole layout (where a conversation is, which terminals still
  have a pane, the server's `gerdr workspace / tab / pane`, muted groups) works unchanged.
- `viewWorkspaces(l, at)` lists one view's workspaces (project + host). What lists, numbers, cycles or picks
  workspaces for the screen goes by it (the sidebar, ⌃⌘1–9, the workspace picker, the new-session page's folders,
  closing a workspace's fallback). `spaceWorkspaces` stays for the project as a whole (closing it, its dot on the rail).
- `ui/src/lib/hostView.ts`, a store subscription started after boot, keeps two things true whatever changed:
  1. **the view on screen and the active workspace agree.** Switching to the panel (or back to Code, or to another
     project) brings back the workspace that view was left on (per device, `gerdr.hostActive`), else its first, else a
     new empty one. When instead an action brought a pane of the other view to the front (a notification, ⌘K, the
     inbox, `focusSession`), the screen follows it: Code mode or that panel.
  2. **a conversation's pane is in its owner's view** (`placeOwned`): `tags.panel` of a panel with its own layout →
     that panel's, everything else → Code's. A pane in the wrong one moves, as its own tab, into the right view's
     workspace for its directory. This is also the one-time migration of layouts from before, and it catches any code
     path that opens a tab without knowing about views. Conversations not loaded yet are left alone, and so are a
     panel's while this device doesn't know whether that panel has its own layout (`codeHosts` is filled only where its
     CodeView has mounted). The layout is shared by every device: a phone or window that hadn't opened the panel once
     took its conversations for Code's and moved them, the device showing the panel moved them back, and in the tug a
     conversation just started there lost its pane (开始 seemed to do nothing).
- `addTabForSession` and `openSessionNear` place a conversation in its owner's view directly, so it doesn't land in the
  wrong one first.
- `useCodeRows()` gives the panel its own panes; `useCodeRows("code")` gives Code mode's (what 从 Code 导入 offers).

Only `list="replace"` panels get their own layout. A `list="add"` panel draws its part above Code's own list, so it
shows Code's workspaces and Code's layout, as before. A panel that doesn't host Code at all opens its conversations in
Code ("open in Code"), as before.

## Known edges

- An older UI on another device (before this change) ignores `host` and lists the panel's workspaces in Code. The UI
  updates with the backend, so this only lasts until that device reloads.
- A panel that stops hosting Code (uninstalled, broken) leaves its workspaces behind, invisible in Code. Nothing in
  them is Code's, and the server still sees them.
