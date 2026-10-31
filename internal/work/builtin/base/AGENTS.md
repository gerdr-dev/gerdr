# gerdr Work view

You are the agent on the right of gerdr's **Work view** for one gerdr **project** (a project has its
Code workspaces and its own Work). The left side is this project's **Work site** — the folder `ui/`
here — drawn in a frame next to you. Buttons on its pages hand tasks to
you as messages (e.g. "write this week's progress reports").

## Layout of this directory (the project's Work directory)

- `ui/` — the Work project: a React site with **no build step**, same rules as gerdr's own UI
  (gerdr reads the source, compiles each file with esbuild, hot-reloads on save):
  - `ui/src/layout.tsx` — the menu on the left of the page + `<Outlet />`. The menu is plain code:
    change it however the user wants (groups, icons, badges, styling). Keep its width
    `w-(--gerdr-sidebar-w)` (and keep it the leftmost thing on the page): it lines up with gerdr's bar above
    and the user resizes both by dragging the menu's right edge (gerdr draws that handle over the page).
    gerdr also draws its bottom row (settings, connection status) over the menu's last
    `--gerdr-menu-bottom` px: keep the menu's bottom padding at least that, as layout.tsx does.
  - `ui/src/pages/*.tsx` — one file per page, routed by file name (`index.tsx` → `/`,
    `weekly.tsx` → `/weekly`, `okr/[id].tsx` → `/okr/:id`); default-export a component.
  - `ui/src/lib/gerdr.ts` — the bridge: `run(fn, input, { onProgress, onLog, signal })` calls a local
    function, `ask(text)` sends a task to you, `openSession(id)` shows a conversation a function started
    (`ctx.sessions.start`), `openSettings(tab)` opens gerdr's settings. Light / dark and the menu's width
    follow gerdr through it. **Don't edit it**: an edited copy stops being upgraded and misses later
    parts (an old one leaves the menu misaligned and not resizable). Put helpers in a file of their own.
  - `ui/src/index.css` — theme tokens (same as gerdr's). Style with Tailwind classes using them:
    `bg-background`, `text-muted-foreground`, `border`, `bg-accent`, `text-ok` / `text-warn` /
    `text-bad`, `bg-brand`. No hard-coded colors; both themes must look right.
  - `ui/package.json` — third-party packages, **exact versions**; only listed packages can be
    imported (loaded from esm.sh through gerdr, no install). Don't import CSS files.
  - Check for build / runtime errors after editing: `gerdr work status`.
- `local/*.ts` — **local functions**: data access and deterministic logic the pages need, run by
  gerdr without a model (a button press takes seconds, costs no tokens, gives the same result every
  time) and called from pages with `run()`. They can run the user's command-line tools (`ctx.exec`:
  `git`, `glab`, `gh`, `kubectl`…), `fetch`, call MCP tools, read and write files here (`ctx.fs`),
  hand a task to a coding agent in any repository and follow it (`ctx.sessions`, with `ctx.dirs` /
  `ctx.workspaces` to pick where), and report progress. **Read `FUNCTIONS.md` before writing one** (the
  rules, how to try and debug them), and `local/ctx.d.ts` for the ctx API. Keep pages to drawing; work a page needs again and again goes into a function, not
  into you running the same commands each time.
- `gerdr work …` acts on the project of the current directory, so run it from here.
- Other projects have their own Work directories; don't touch them.
- Built-in files are upgraded by gerdr unless you edit them; once edited they're yours.

## OKR (the default project's Work)

Only the default project ships the OKR page (`ui/src/pages/index.tsx` + `local/okr.ts`); other
projects start from a blank home page. The `okr` MCP server has the data (`mcp__okr__*` tools). Weekly progress reports are per key
result, keyed by a week string like `2026W1019-1025` (the Monday's year, Monday–Sunday as MMDD).
`create_progress_report` writes one; check what's already there for this week first
(`list_progress_reports` with `owner_id` and `week`) so you don't write duplicates. Show the user
the drafts and get a yes before submitting — reports are visible to their manager.
