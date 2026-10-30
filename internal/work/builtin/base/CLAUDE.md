# gerdr Work view

You are the agent on the right of gerdr's **Work view** for one gerdr **project** (a project has its
Code workspaces and its own Work). The left side is this project's **Work site** — the folder `ui/`
here — drawn in a frame next to you. Buttons on its pages hand tasks to
you as messages (e.g. "write this week's progress reports").

## Layout of this directory (the project's Work directory)

- `ui/` — the Work project: a React site with **no build step**, same rules as gerdr's own UI
  (gerdr reads the source, compiles each file with esbuild, hot-reloads on save):
  - `ui/src/layout.tsx` — the menu on the left of the page + `<Outlet />`. The menu is plain code:
    change it however the user wants (groups, icons, badges, styling).
  - `ui/src/pages/*.tsx` — one file per page, routed by file name (`index.tsx` → `/`,
    `weekly.tsx` → `/weekly`, `okr/[id].tsx` → `/okr/:id`); default-export a component.
  - `ui/src/lib/gerdr.ts` — the bridge: `run(fn, input)` calls a local function, `ask(text)` sends
    a task to you, `openSettings(tab)` opens gerdr's settings. Light / dark follows gerdr.
  - `ui/src/index.css` — theme tokens (same as gerdr's). Style with Tailwind classes using them:
    `bg-background`, `text-muted-foreground`, `border`, `bg-accent`, `text-ok` / `text-warn` /
    `text-bad`, `bg-brand`. No hard-coded colors; both themes must look right.
  - `ui/package.json` — third-party packages, **exact versions**; only listed packages can be
    imported (loaded from esm.sh through gerdr, no install). Don't import CSS files.
  - Check for build / runtime errors after editing: `gerdr work status`.
- `local/*.ts` — **local functions**: data access and deterministic logic the pages need, run by
  gerdr (esbuild + goja: no node, no files, no processes) and called from pages with `run()`:
  `export async function name(input, ctx)`; `await ctx.mcp(server, tool, args)` calls an MCP tool
  and returns `structuredContent`, or the text parsed as JSON, or the text. MCP servers: gerdr's own
  (Settings → MCP), then Claude Code's (`~/.claude.json`), then Codex's. Keep pages to drawing.
  Try a function exactly as a page would: `gerdr work run okr.summary` (2nd arg: input JSON).
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
