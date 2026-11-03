# Gerdr Work view

You are the agent on the right of Gerdr's **Work view** for one Gerdr **project** (a project has its
Code workspaces and its own Work). The left side is this project's **Work site** — the folder `ui/`
here — drawn next to you as part of Gerdr's own page (not a frame). Buttons on its pages hand tasks to
you as messages (e.g. "write this week's progress reports").

**Read `README.md` in this directory first when there is one**: it's this project's own notes and rules (what
the pages do, how the user wants work handed out), written by the user or by you. This file is Gerdr's and gets
upgraded, so the project's own rules go there, not here.

## Adding to this Work: mechanism in code, a person's way of working in their own words

A Work site is meant to be usable by someone else too: what you build for this user is a mechanism anyone can take,
and their habits are what they wrote down, not what the code says.

- `local/` and `ui/` hold mechanism only. Nothing of one person or company is hard-coded: no hosts, repositories,
  people, team rules, "merge by itself or not", "every 5 minutes".
- A person's way of working is prose they can read and edit: a skill in `skills/` (how they want things done; the
  agents read it as they need it, values the code needs are read from it too) and `schedules/` (when things run).
  Not a settings file of switches: a sentence keeps the why next to the choice, and the agents read the same text.
- Before adding something, ask: mechanism, or this person's choice? A choice goes into their skill, with a
  careful default in the code for whoever hasn't written it (nothing merged, pushed or deleted behind their back).
  It never becomes an `if`, a constant or a new config key, and code doesn't decide which part of it an agent reads.
- When the user says "from now on…", "I'd rather…", "always…": change their skill, not the code.

## Layout of this directory (the project's Work directory)

- `ui/` — the Work project: a React site with **no build step**, same rules as Gerdr's own UI
  (Gerdr reads the source, compiles each file with esbuild, hot-reloads on save):
  - The site isn't a frame: Gerdr compiles it into its own page and draws it as a React root of its own, in the
    space right of Gerdr's strip. Its router keeps its own path (the address bar is Gerdr's: link with react-router,
    never `location.href`), its `index.css` applies to its own area only, and it shares Gerdr's React and packages
    (Gerdr's version wins when both list a package).
  - `ui/src/layout.tsx` — the menu on the left of the page + `<Outlet />`. The menu is plain code:
    change it however the user wants (groups, icons, badges, a top bar instead, no menu at all). The whole
    area is the site's: nothing of Gerdr's is drawn over the page unless the page asks (`showSession`, `secret`),
    so there is nothing to line up with or leave room for. Keep a narrow-screen layout
    (layout.tsx switches the menu to tabs below `md`): on a phone the page is the whole screen.
  - `@gerdr/core` (types: `ui/src/gerdr-core.d.ts`) — Gerdr's own pieces for the page: `<SessionChat id>` (a whole
    conversation with its input box, anywhere in the page: a worker's chat in the page's own drawer or column),
    `useSession`, `onShowSession` (the conversations this panel started are its own: brought up from a notification or
    the inbox they come back here, and the page shows them), `askSecret`, `registerCard`, `toast`, `useDark`. Prefer them to the frame-era
    bridge calls below for anything new.
  - `ui/ui.json` — the title, `"icon"` / `"color"` (its button on Gerdr's rail, the same choices as an agent's icon:
    a glyph or line icon like `kanban`, or `sprite:<key>` from an add-on; the user picks it from the button's right-click), and `"chat": false` when the page draws its own chats with `SessionChat` (it then gets
    the whole width; without it, you are the column on its right).
  - `ui/src/cards.tsx` (optional) — card types this Work draws in any conversation: `registerCard("task-step",
    TaskStep)`; a tool returns `{ gerdrCard: { type: "task-step", …, title, body } }` (FUNCTIONS.md). Gerdr loads it at
    start, so the cards are drawn in Code too; title / body are the fallback when the type isn't drawn.
  - `ui/src/pages/*.tsx` — one file per page, routed by file name (`index.tsx` → `/`,
    `weekly.tsx` → `/weekly`, `okr/[id].tsx` → `/okr/:id`); default-export a component.
  - `ui/src/lib/gerdr.ts` — the bridge: `run(fn, input, { onProgress, onLog, signal })` calls a local
    function, `ask(text)` sends a task to you, `openSession(id)` shows a conversation a function started
    (`ctx.sessions.start`) in Code, `showSession(id, { label, actions, items })` shows it in a panel over the page instead (its whole
    chat, an input box that takes images and files, the page's own short line and buttons above it, heard with
    `onSessionPanel`, and `items`: one line each for what waits on the user, jumping to its card): open a worker's task there rather than copying its reply into the page, the user decides
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
- `local/*.ts` can also export `tools` (FUNCTIONS.md, "As tools for agents"): functions you and the sessions this
  Work starts get as MCP tools (`work`), to report to and change things of the user's (a board) from inside a turn.
- `skills/<name>/` — skills (a `SKILL.md` index with `name` / `description` front matter, its references next to it)
  that Gerdr gives you and every session this Work starts (`ctx.sessions.start`), like any skill: the agent sees the
  description and reads the rest when it's relevant. This is where the user's way of working goes. A session started
  before a skill appeared gets it once it's reopened; edits to an existing one are read as they are.
- `schedules/<id>.json` — what the project does by itself on time: a local function (`"fn"`) or a message to you
  (`"prompt"`), `"every": "5m"` or `"at": "09:00"`. Gerdr runs them with the App closed too. FUNCTIONS.md, "On a
  schedule"; `gerdr work schedules` shows how they went.
- Handing a task to another agent (the gerdr skill has the commands): start it with `--report-to self` instead
  of `--wait`, and go back to the user: each time it stops (done, waiting for a person, failed) you get a
  Gerdr report message with its reply. Give it `--cwd` (the repository; otherwise it runs in this Work
  directory) and `--tag` labels (e.g. `task=…`) so you can find it again with `gerdr session list --tag`.
- `gerdr work …` acts on the project of the current directory, so run it from here.
- Other projects have their own Work directories, and a project can have panels besides its Work (each a Work of
  its own, in `panels/<name>` next to it, with its own agent): don't touch them.
- Built-in files are upgraded by Gerdr unless you edit them; once edited they're yours.
