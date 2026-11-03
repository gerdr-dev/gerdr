# Gerdr Work view

You are the agent on the right of Gerdr's **Work view** for one Gerdr **project** (a project has its
Code workspaces and its own Work). The left side is this project's **Work site** — the folder `ui/`
here — drawn in a frame next to you. Buttons on its pages hand tasks to
you as messages (e.g. "write this week's progress reports").

**Read `README.md` in this directory first when there is one**: it's this project's own notes and rules (what
the pages do, how the user wants work handed out), written by the user or by you. This file is Gerdr's and gets
upgraded, so the project's own rules go there, not here.

## Layout of this directory (the project's Work directory)

- `ui/` — the Work project: a React site with **no build step**, same rules as Gerdr's own UI
  (Gerdr reads the source, compiles each file with esbuild, hot-reloads on save):
  - `ui/src/layout.tsx` — the menu on the left of the page + `<Outlet />`. The menu is plain code:
    change it however the user wants (groups, icons, badges, a top bar instead, no menu at all). The whole
    frame is the site's: Gerdr's own controls are on its strip left of the frame and nothing of Gerdr's is
    drawn over the page unless the page asks (`showSession`, `secret`), so there is nothing to line up with or
    leave room for. Keep a narrow-screen layout
    (layout.tsx switches the menu to tabs below `md`): on a phone the frame is the whole screen.
  - `ui/src/pages/*.tsx` — one file per page, routed by file name (`index.tsx` → `/`,
    `weekly.tsx` → `/weekly`, `okr/[id].tsx` → `/okr/:id`); default-export a component.
  - `ui/src/lib/gerdr.ts` — the bridge: `run(fn, input, { onProgress, onLog, signal })` calls a local
    function, `ask(text)` sends a task to you, `openSession(id)` shows a conversation a function started
    (`ctx.sessions.start`) in Code, `showSession(id, { label, actions })` shows it in a panel over the page instead (its whole
    chat, an input box that takes images and files, and the page's own buttons above it, heard with
    `onSessionPanel`): open a worker's task there rather than copying its reply into the page, the user decides
    by reading the whole conversation, `openSettings(tab)` opens Gerdr's settings, `secret(name, { label, note })` asks
    the user for a password / token in Gerdr's own dialog and saves it for `ctx.secrets.get` (the page never sees
    it; never ask for one in a page input or in chat). Light / dark follows Gerdr through it.
    **Don't edit it**: an edited copy stops being upgraded and misses later parts. Put helpers in a file
    of their own.
  - `ui/src/index.css` — theme tokens (same as Gerdr's). Style with Tailwind classes using them:
    `bg-background`, `text-muted-foreground`, `border`, `bg-accent`, `text-ok` / `text-warn` /
    `text-bad`, `bg-brand`. No hard-coded colors; both themes must look right.
  - `ui/package.json` — third-party packages, **exact versions**; only listed packages can be
    imported (loaded from esm.sh through Gerdr, no install). Don't import CSS files.
  - Check for build / runtime errors after editing: `gerdr work status`.
- `local/*.ts` — **local functions**: data access and deterministic logic the pages need, run by
  Gerdr without a model (a button press takes seconds, costs no tokens, gives the same result every
  time) and called from pages with `run()`. They can run the user's command-line tools (`ctx.exec`:
  `git`, `glab`, `gh`, `kubectl`…), `fetch`, call MCP tools, read and write files here (`ctx.fs`),
  hand a task to a coding agent in any repository and follow it (`ctx.sessions`, with `ctx.dirs` /
  `ctx.workspaces` to pick where), and report progress. **Read `FUNCTIONS.md` before writing one** (the
  rules, how to try and debug them), and `local/ctx.d.ts` for the ctx API. Keep pages to drawing; work a page needs again and again goes into a function, not
  into you running the same commands each time.
- Handing a task to another agent (the gerdr skill has the commands): start it with `--report-to self` instead
  of `--wait`, and go back to the user: each time it stops (done, waiting for a person, failed) you get a
  Gerdr report message with its reply. Give it `--cwd` (the repository; otherwise it runs in this Work
  directory) and `--tag` labels (e.g. `task=…`) so you can find it again with `gerdr session list --tag`.
- `gerdr work …` acts on the project of the current directory, so run it from here.
- Other projects have their own Work directories; don't touch them.
- Built-in files are upgraded by Gerdr unless you edit them; once edited they're yours.
