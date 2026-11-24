---
name: gerdr-setup
description: "Changing how Gerdr (the GUI you run in) is set up, from the command line: its settings (theme, default agent / mode / model, shortcuts, model providers: `gerdr config`); agents (roles) with their own prompt, skills and MCP servers (`gerdr agent / skill / mcp`); Gerdr projects (`gerdr project list / new`) and a project's own skills and shared memory (`gerdr skill --project`); add-ons (`gerdr addon`: stores, install, settings); a project's panels (`gerdr panel`: install, new, update, icon, order, badge, remove); the layout (workspaces, tabs, splits, a moved project folder: `gerdr workspace / tab / pane`); remote access from a phone (`gerdr remote`). Changing Gerdr's UI code is gerdr-ui. Requires GERDR_ENV=1."
---

# Gerdr: settings, agents, add-ons, panels, layout

Only inside Gerdr (the GUI you run in). Check first; if not, say so and stop:

```bash
test "${GERDR_ENV:-}" = 1
```

Commands print JSON; an error is one `{"error": "..."}` line on stderr and exit code 1. `--help` on any command has the
installed version's exact flags.

## Arranging the layout (workspaces / tabs / panes)

When the user asks you to tidy up Gerdr (sort tabs into workspaces, put two conversations side by side, rename things), use
`gerdr workspace|tab|pane …`. They're shaped like herdr's commands of the same names and output JSON. Only do what was asked:
the layout is the user's desk.

```bash
gerdr workspace list                     # workspace_id, label, number (sidebar order), tab_count
gerdr tab list [--workspace <ws>]        # tab_id, label, pane_count, split: row(a, b) = a left of b, col(a, b) = a above b
gerdr pane list [--workspace <ws>]       # pane_id, kind (agent / terminal / subagent / file / empty), session {id, title, status, cwd}; self: true is you
gerdr pane current                       # your own pane
gerdr pane layout [<pane>] [--tab <tab>] # each pane's rect in its tab
gerdr pane read [<pane>] [--lines 100]   # a terminal pane's recent output as plain text; no pane = the terminal in your tab
gerdr pane send [<pane>] <text>          # type into a terminal pane + Enter (asks for approval: it runs things)
```

A workspace is named by its id, number or label; a tab by its id or label; a pane by its id or by the conversation it shows
(session id, prefix, or title). When a label matches several things you get an error: use the id.

```bash
gerdr workspace create --label lb [--cwd DIR] [--position 1]   # starts with an empty tab, which goes away once you move something in
gerdr pane move <pane> --workspace lb [--tab-label api]        # make it a tab of that workspace
gerdr pane move <pane> --new-workspace --label lb              # make it the first tab of a new workspace
gerdr pane move <pane> --target-pane <pane> --split right      # split it beside another pane (right / down / left / up, --ratio 0.5)
gerdr pane move <pane> --tab <tab> --split down                # beside the whole tab
gerdr pane move <pane> --new-tab                               # pull it out of a split into a tab of its own
gerdr pane swap <pane> --target-pane <pane>
gerdr pane float <pane> [--fit pc|pad|mobile]                  # float a Browser pane over the rest of its tab (picture-in-picture): its page
                                                               # laid out at a fixed desktop / tablet / phone size, shown as its stream; --fit again to change
gerdr pane dock <pane>                                         # put a floating pane back in its place
gerdr pane minimize <pane>                                     # fold a Browser pane to a chip in its tab's corner (floats it first)
gerdr pane restore <pane>                                      # open a minimized one again where it was (docked / floating)
gerdr pane fit <pane> auto|pc|pad|mobile                       # a Browser pane's page size on every device (auto: the pane's own;
                                                               # pc / pad / mobile: laid out at that size, shown scaled to the pane)
gerdr tab move <tab> --workspace lb [--position 1]             # a whole tab, splits and all
gerdr tab move <tab> --tab <other-tab> --split right           # merge it into another tab
gerdr workspace move <ws> --position 1
gerdr workspace rename <ws> <label>;  gerdr tab rename <tab> <label>
gerdr workspace cwd <ws> <folder>                              # folder new tabs start in (project moved)
gerdr workspace close <ws>;  gerdr tab close <tab>             # only empty ones
```

- Moving never stops anything: the conversation or terminal just shows up somewhere else. A pane alone in its tab takes the tab
  (and its label) along. A tab you moved everything out of disappears; a workspace you emptied stays (with one empty tab) until
  you `workspace close` it.
- `close` refuses anything that still shows a conversation or a terminal. Ending a conversation is `gerdr session close`, and
  only when the user asked.
- Nothing switches the user's view. Each window keeps looking at what it was looking at.
- Everything stays inside your own session (the space you're in); other sessions' workspaces aren't listed and can't be reached.
- Plan the whole rearrangement from one `workspace list` + `pane list`, then run the moves. Ids don't change when things move,
  so you don't need to list again between moves.

## Changing Gerdr's settings

When the user asks to change a Gerdr setting (theme, send key, default agent / mode / model, shortcuts, agents
in Settings → Agent → Roles, Gerdr Agent's model providers…), use `gerdr config`, not an editor on config.json:
it changes one field, checks the value, and open windows update within seconds.

```bash
gerdr config keys                         # every setting and what it does
gerdr config get defaultModes             # read one (no path = all)
gerdr config set theme dark               # value is JSON if it parses, else a string
gerdr config set agents.reviewer.prompt "Review only, don't edit"   # array elements by id or index
gerdr config unset defaultConfig.codex
```

`theme`, `sendKey`, `notifications`, `keepTabs`, `gridTiles`, `virtualList`, `copyOnSelect` and `keepConnected` are per device: a device that
picked its own in Settings (marked 仅本设备) keeps it, `gerdr config set` only changes the others. If the user says
it didn't take, tell them to change it in Settings on that device.

Gerdr Agent compacts the context automatically at 90% of the model's window (the composer's context % counts
against the whole window). To compact earlier or later, set `compactAt` on that model, in tokens, not percent:
`gerdr config set llm.providers.<provider>.models.<model>.compactAt 500000` (on a 1M window that is 50%). `window`
on the same model fixes a wrong guess of the window size. Claude Code and Codex compact on their own; this doesn't
touch them.

A model's thinking levels (the effort picker) come from its own `efforts` / `defaultEffort` when set, else from
the provider's model list when it gives them, else a guess from the id. To set them:
`gerdr config set llm.providers.<provider>.models.<model>.efforts '["low","medium","high","xhigh"]'` and
`… .defaultEffort medium`; values go to the API as written. `gerdr config unset` on both goes back to automatic.

The image model (for `gerdr image gen` and Gerdr Agent's `generate_image`) is `llm.imageModel`, picked from the
providers' image models (`gerdr image models`); setting one up is in the gerdr-image skill.

API keys read back as `<hidden>`; the real ones are in secrets.json. Don't open that file or print the
environment variables named in `apiKeyEnv`: whatever you read ends up in this transcript. To set a key the
user gives you, `gerdr config set llm.providers.<id>.apiKey <key>`.

## Setting up an agent (role)

An agent in Settings → Agent → Roles is an engine (claude / codex / native) plus its own prompt, skills, MCP servers and
new-session defaults. Skills and MCP servers live in one shared pool; an agent lists the names it gets.

1. Read what exists: `gerdr config keys agents` (every field), `gerdr skill list`, `gerdr mcp list`, `gerdr session agents`.
2. Create it in one step with a spec — the agent's fields, plus `define` for skills / MCP servers that don't exist yet,
   plus `defaults`. `gerdr agent create --help` has the full shape:

   ```bash
   gerdr agent create --file reviewer.json   # or the JSON as an argument, or on stdin
   ```

   It writes everything or nothing; an existing skill / MCP server with different content is refused unless `--replace`.
3. Done when `gerdr agent show <id>` prints the agent with its skills / MCP and stderr had no `note:` lines left
   unexplained (a `note:` names a skill / MCP that doesn't exist or is switched off).

Changing one later: `gerdr config set agents.<id>.<field> …` (`skills` / `mcp` take the whole list), new pool entries with
`gerdr skill add` / `gerdr mcp add`, removal with `gerdr agent rm <id>` (`--prune` also deletes the pool entries only it
used). An agent's `skills` / `mcp` list gets exactly those names whatever the global source; leaving the field out means
"what the global settings give"; `[]` means none. Skills that come with the repository being worked on are given
either way.

The built-in agents (`claude`, `codex`, `native`) are edited the same way: an entry in `agents` with that id changes
its name, icon, prompt, skills / MCP, `showThinking` (`expanded` | `latest` | `collapsed`), launch settings (its
engine is always itself; `gerdr config set agents.codex.showThinking latest` seeds the entry if needed, or
`gerdr agent create` adds one). `gerdr agent rm claude` only takes it off the lists — sessions on it and roles on its
engine keep working — and `gerdr agent restore claude` puts it back. To change the display order in Settings → Agent
and the new-session picker, set `agentOrder` to the ordered list of agent ids (`gerdr config set agentOrder '["reviewer","claude","codex"]'`;
unlisted ones follow in default order).

MCP tokens (headers, env) are secrets like API keys: put in only what the user hands you; `list` / `show` print them as
`<hidden>`, and `create` fills a `<hidden>` back from the pool entry of the same name.

### A project's own skills and memory

A Gerdr project (the user's lb, default…) spans several repositories and has its own skills and memory, kept on this
computer and given only to that project's sessions, whatever agent runs them. Your own project's id and its memory
folder are in your system prompt ("Project memory (Gerdr project …)"). The user edits both in the project's
right-click menu → Skills & memory.

Projects are listed and created from the command line too (the sidebar's New project; the name is also the id that
`--project` takes; a name used before gets its workspaces back; no window switches to it):

```bash
gerdr project list
gerdr project new lb [--cwd ~/dev/lb]
```

- **Skills**: when a skill is meant for one project (its deploy routine, its team's tools), put it there, not in
  gerdr's pool or `~/.claude/skills`: no source to switch, no other project to hide it from. A same-named gerdr skill
  loses to it. Sessions already running get an added / removed one before their next turn.

  ```bash
  gerdr skill list --project lb
  gerdr skill add deploy --project lb --file ./SKILL.md     # or --dir ./deploy (with scripts); --replace to update
  gerdr skill rm deploy --project lb
  ```

- **Memory**: `<GERDR_DIR>/projects/<id>/memory/`, one .md per fact plus the `MEMORY.md` index, the format of Claude
  Code's auto memory. Every session of the project starts with the index. Edit the files directly; keep the index in
  step.

### Per project instead of per agent

When the difference is between projects (one project needs company skills / MCP servers, another doesn't), switch
them off in the project, not with a new agent: a role is offered in every project, so a role per project means picking
the right one each time. `projects.<id>.disabledSkills` / `.disabledMcp` take names out of every session in that
project, whatever agent runs it; everything else is given as usual. The user's way is the project's right-click
menu → Skills & memory → Switched-off. `gerdr config keys projects` has the rest.

```bash
gerdr config set projects.default.disabledSkills '["lb-cli", "deva"]'
gerdr config set projects.default.disabledMcp '["okr"]'
```

## Add-ons (Settings → Add-ons)

Add-ons change Gerdr itself: an add-on panel by the left rail (quota meter, today's stats), a line under each turn (cache clock),
commands and keys (duplicate tab), notifications. They come from stores (GitHub `owner/repo`, default
`gerdr-dev/gerdr-addon`). When the user asks for something like that, check the store before writing UI code.
Use `gerdr addon`, not file edits: it checks values, keeps install records, and open windows follow within a second.

```bash
gerdr addon store                       # what the stores offer, marked installed / update
gerdr addon info cache-clock            # details, settings, README
gerdr addon install cache-clock         # also updates an installed one
gerdr addon list                        # installed: on / off, store, update, service status, errors
gerdr addon update                      # refresh the stores, update everything with a newer copy
gerdr addon config cache-clock          # settings with type / default / range / options
gerdr addon config cache-clock warnMinutes=3   # --unset warnMinutes goes back to the default
gerdr addon enable|disable cache-clock
gerdr addon rm cache-clock              # deletes the folder; its data stays
gerdr addon store add owner/repo        # or an absolute folder; store rm / store refresh [source]
```

- A `conflict` from install / update means the folder didn't come from a store or the user edited it. Ask before
  `--force`: it throws their changes away.
- Secret settings print as `<hidden>`; set one only with a value the user gives you.
- Done when `gerdr addon list` shows it with the expected `enabled`, no `error` and no `problems`. A non-empty `error`
  is an addon.json that doesn't parse or code that doesn't build; a service add-on's stderr is in its `log`.
- Writing or changing an add-on: read `~/.gerdr/addons/GUIDE.md` first, and run `gerdr addon check <id>` (or
  `./folder`) after every change to its addon.json. Gerdr loads a misspelled field or setting type without a word (the
  setting just doesn't show, or shows wrong); check names each mistake and exits 1 until there are none.

## Panels

Besides Code, a project has panels: each a whole app of its own (pages, local functions, skills, schedules, data, its
agent), level with Code on the strip at the left. `gerdr panel` manages them, the same as Settings → Panels; the
project is the current directory's (or `--project`). A panel is named by its name, the project's own by the project id.

```bash
gerdr panel list                                   # key, name, config (title, icon, color), where it came from
gerdr panel install owner/repo [--name board]      # clone a published panel (its code runs here: confirm with the user)
gerdr panel new <name>                             # a blank one, for its agent to build
gerdr panel update [<panel>]                       # git pull; the user's own changes stay on top
gerdr panel rename <panel> "任务看板"                # the name shown for it, for this person
gerdr panel icon <panel> kanban [--color blue]     # glyph / line icon key, or sprite:<key>; no icon = default
gerdr panel move <panel> 1                         # its place on the rail (1 = top, Code counts), as dragging does; `.` = Code
gerdr panel badge <panel> 3 | dot | clear          # a red number / dot on its rail button ("something new"); --add N adds to it
gerdr panel rm <panel>                             # to the trash, data and all
gerdr panel restore                                # the project's own panel back (blank) after removing it
```

A badge is only a mark: no inbox item, no notification, and it goes once the user opens the panel. For something that
needs the user, a panel's function uses `ctx.inbox`; its functions set the badge with `ctx.badge`.

The order (`move`) is this person's, kept in the project's `panels/.order.json`; `list` gives the panels in it.

rename / icon are the user's own choice: kept in the panel's `user/ui.json` over its `ui/ui.json`, never shared, and
an update leaves them alone. A panel's name for everyone who installs it (its author's) is `title` in `ui/ui.json`.

## Remote access: `gerdr remote`

Opening this Gerdr from a phone or another computer (the same is in Settings → Remote access).
`gerdr remote status` shows whether it's on. To turn it on, pick by what the user has:

- **Tailscale, no domain** (simplest): `gerdr remote direct --tailscale`. Uses this machine's tailnet name
  (`<machine>.<tailnet>.ts.net`) and gets its certificate from Tailscale; phones need Tailscale on the same tailnet.
  If it says HTTPS certificates are off, the user turns them on at login.tailscale.com/admin/dns (you can't).
- **A domain of their own**: `gerdr remote direct <host> --dns <provider> --env NAME=…` (Tailscale / frp / port
  forward) or `gerdr remote join <relay-url> <host> <invite>` (a dialhome relay). `gerdr remote direct --help` explains.

These aren't auto-approved, and a pairing code (`gerdr remote pair`) lets a new device in: leave issuing one to the user.
`gerdr remote devices` lists the paired devices; `gerdr remote note <device-id> <note>` gives one the user's own name
("my Pixel", shown in Settings too; no note clears it).

With a domain and a DNS provider, local ports come along: a server you start on `http://localhost:8765` opens on a
paired phone at `https://8765.<host>/` (Gerdr signs the phone in, then proxies; ports 1024–65535). Links like
`http://localhost:8765/x` in your replies switch to it by themselves, so just write the full local URL.
`gerdr remote status` shows whether it's on ("Local ports"); it needs DNS for `*.<host>` pointing where `<host>` does.

