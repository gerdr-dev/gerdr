---
name: gerdr
description: "Coordinate other coding-agent sessions inside Gerdr (Claude Code / Codex sessions in the Gerdr GUI): list sessions, start a new agent session next to you, hand it a task, wait for it, read its reply, or message an existing session. Use when the user asks you to delegate to / start / ask / check on another agent or session in Gerdr, or to run several agents in parallel. Also for arranging Gerdr's layout: sorting tabs into workspaces, splitting panes together, renaming or reordering workspaces / tabs. Also for changing Gerdr's own settings (theme, default agent / mode / model, shortcuts, model providers) with `gerdr config`, and for setting up agents (roles) with their own prompt, skills and MCP servers (`gerdr agent`, `gerdr skill`, `gerdr mcp`). Also for add-ons (`gerdr addon`): browsing add-on stores, installing / updating / removing add-ons, switching them on or off, changing their settings. Also for a project's panels (`gerdr panel`): listing, installing one from a git repository, adding a blank one, updating, changing its icon, removing. Also for being told when something happens outside the conversation (a new MR comment, a pipeline finishing, a chat message): `gerdr event watch / list`. Requires GERDR_ENV=1."
---

# Gerdr：和其他会话协作

Gerdr 是一个跑 Claude Code / Codex 的图形界面，里面同时开着好几个会话（每个会话是一个 agent）。
`gerdr session …` 这组命令让你列出它们、开新的、给它们派活、等它们做完、读它们说了什么。

先确认你确实跑在 Gerdr 里，不是就说明一下然后停：

```bash
test "${GERDR_ENV:-}" = 1
```

命令以安装的版本为准，拿不准就看帮助（这些都是只读的）：

```bash
gerdr session --help
gerdr session new --help
```

所有命令输出 JSON；出错时 stderr 一行 `{"error": "..."}`、退出码 1。会话用完整 `id`、`id` 前 4 位以上的前缀、或者标题来指代，
**从命令的输出里取 id，别自己编**。

## 看看都有谁

```bash
gerdr session current   # 你自己
gerdr session list      # 全部；"self": true 是你自己
```

每个会话的 `status`：

- `idle`：闲着，可以派活；
- `running`：正在干活（给它发消息 = 插话进它正在跑的这一轮）；
- `waiting`：在等人审批命令或回答问题 —— 只有人能处理，告诉用户去 Gerdr 界面上看，别替人决定；
- `error`：上一轮出错了（`error` 字段是原因）。

`list` 只列**开着的**会话 —— 就是用户在界面上看得见的（和 herdr 一样：用户关掉 pane 就是关掉那个会话）。
关掉的会话记录还在、能接着聊：`gerdr session list --all` 能看到（`closed: true`），
`gerdr session open <会话>` 把它开回界面上（开在你旁边，不抢焦点）；给它 `send` 也会自动开回来。

The user splits Gerdr into isolated **projects** (like herdr's `--session`): each has its own workspaces (Code) and its own
panels, each with its agent (call them panels; there is no "Work" any more). You only see, and can only reach, the conversations and workspaces of the project you're in:
`list` leaves the others out, and naming one of them fails with "no such session". That is deliberate. Don't try to get
around it, and don't ask the user to move things across for you unless they brought it up.

Which project you're in is worked out from where you are, every time: the pane showing you, or for a panel's agent its panel's
directory, or the Gerdr terminal a command is typed in. `--project <name>` on `gerdr session|workspace|tab|pane` is only for
running them from outside Gerdr; don't use it to reach into another project.

## Your own sub-agent, or a Gerdr session?

Your agent's built-in sub-agents (Claude Code's Task / Agent tool, Codex's) and `gerdr session new` both hand work off.
Pick by what the work needs:

- **Sub-agent**: a short, self-contained piece of the task you're on, where only its answer matters (search the repo,
  read twenty files and summarise, check three things in parallel). It shares your machine, agent and permissions,
  its answer lands in your context, it's gone afterwards, and the user can only watch it (read-only panel). Cheapest.
- **Gerdr session**: anything the user should be able to see in its own tab, talk to, approve, or pick up later; that
  needs another agent or role (`--agent codex`, a reviewer role), another permission mode, or another computer
  (`--host`); or that runs long (a build, a render, an MR) while you keep talking with the user. It is a real
  conversation: it survives your turn and can be resumed.
- With a session, `--wait` for something short you need before you can go on. For anything long or several at once,
  don't block: wait for it in the background, or have it report back ("Hand it off without waiting" below).

When unsure and the user didn't say: a sub-agent for research inside your task, a session for work that deserves its
own tab.

## 开一个新会话派活

默认就在当前目录、开在你旁边一屏、不抢用户的焦点。没被要求就别换目录、别开一堆：

```bash
gerdr session new --agent codex --title reviewer \
  --prompt "Review the uncommitted diff in this repo and list only actionable problems." \
  --wait --timeout 20m
```

- 不给 `--agent` 就用设置里的默认 agent；`--agent` 是 `claude`、`codex` 或 `native`（Gerdr 自带的 Gerdr Agent，直接用配置里的 GPT / DeepSeek 等模型，配了才有）；
  用户在设置里建的**角色**（比如只读代码的 `reviewer`，带自己的提示词、只开某几个 skill / MCP）和另一个账号（`claude-2`）也用它的 id。
  `gerdr session agents` 列出全部能用的、角色带着它的提示词；用户说「让审查那个看看」这种就去那里找对应的角色。用户指定了哪个就用哪个；
- `--title` 起个短名字，之后可以 `gerdr session send reviewer …`；
- 带 `--wait`：等它这一轮做完，输出里 `reply` 就是它的回复；不带就是派出去不等，之后再 `wait`；
- 要它能改文件 / 跑命令而不每次问人，按用户的意思给 `--mode`（claude：`acceptEdits` / `auto` / `bypassPermissions`；codex：`auto` / `full-access`；native：`acceptEdits` / `bypassPermissions`，只读用 `plan`）。
  不确定就别给：用用户给这种 agent 设的默认模式（没设、或者设的是全放开的那种，就是 agent 自己的默认），要做危险操作会在界面上弹审批给人；
- `--cwd` defaults to your current directory. As a panel's agent that is the panel's directory, not a repository: always
  give `--cwd` when the task is about code;
- `--workspace <id|number|label|new>` puts it in that Code workspace as a new tab (`gerdr workspace list`); without it,
  it opens next to you.
- A panel's conversation (one a panel started, e.g. a board's worker) can't use `gerdr session new`: it is refused,
  because the panel looks after the agents it runs (what they're told, the model, how they report, where they show).
  Start another agent the panel's way, as its instructions say. The one exception is a helper on another computer,
  which a panel can't start: `gerdr session new --host <id> --report-to self` (it reports back to you; you follow it).
- Gerdr records you as the new session's `parent` (also when a panel function you ran with `gerdr panel run` starts it),
  so who handed what to whom can be followed any number of levels deep — a panel can show that tree for its work.
  Starting a helper from your own session keeps the chain; don't hand work out through some other session.

### On another computer: `--host`

The user can pair other computers with this one (Settings → Remote access). `gerdr session hosts` lists them; a
session there shows in `list` with `"host": "<id>"`, and its id starts with `<host>:`. Everything above works on it the
same (`send`, `wait`, `read`, `--report-to`, tags). Start one there with `--host`:

```bash
gerdr session hosts
gerdr session new --host mini --cwd '~/work/promo' --title cover \
  --prompt "Render the launch cover image into out/cover.png" --report-to self
```

- Its files are **that computer's**: a path in its reply doesn't exist here. To hand a file to a session on another
  computer, attach it: `gerdr session send <session> "..." --file out/cover.png` copies it there and tells it the path.
- To get a file it made: `gerdr session fetch <session> out/cover.png [dest]` (relative to its directory).
- For a whole folder you keep working on from both sides (renders, a video project), pair it once and sync on demand:

  ```bash
  gerdr folder pull 'mini:~/work/promo/out' ./promo-out   # pairs ./promo-out with that folder, brings it here
  gerdr folder status ./promo-out                          # what changed on each side
  gerdr folder push ./promo-out                            # send your changes back
  ```

  Pull before you work on it and push after: nothing syncs by itself. A file both sides changed is kept twice
  (`cover (mini).png` next to `cover.png`), never merged: tell the user and let them choose. A file deleted on one side
  is reported, not deleted on the other. `.gerdrignore` in the folder (one pattern per line) skips render caches;
  `node_modules`, `.git` are always skipped.
- `--cwd` with `--host` is absolute or `'~/…'` in quotes: unquoted, your shell turns `~` into *this* computer's home.
  Without `--cwd`, that computer's home. Same for `gerdr folder`'s `'host:~/…'`.
- Only start one on another computer when the user asked for it, or the task needs that machine (its GPU, its files,
  its accounts).

### Hand it off without waiting

To run a long task, or several at once, don't block yourself (and the user) on `--wait`. Start it without `--wait`,
go back to the user, and get told when it stops (turn done, waiting for a person to approve / answer, failed). Two
ways, by what you can do:

**Wait in the background** if you can run a command in the background and be woken when it ends:

- Claude Code, Gerdr Agent: your shell's `run_in_background`;
- Codex, when your instructions say sub-agents you don't wait for wake you: a sub-agent whose only task is to run the
  command and reply with its output (waiting inside one exec cell, `yield_time_ms` 300000, not a model call every
  30 seconds). Don't `wait` for that sub-agent.

```bash
gerdr session new --cwd ~/code/api --title "fix login" --tag task=t12 --prompt "Fix the login redirect bug ..."
gerdr session wait "fix login" --timeout 60m     # this one in the background, then end your turn
```

Same after `gerdr session send <id> "…"` to a session that already exists. The wait is yours: it shows among your
background tasks, and when it returns you're woken with its output. Look at `session.status` (`waiting`: stuck on a
person) and `timed_out` (still working: wait again, or `gerdr session read` it). Prefer this when you can: the wait is
something you hold and can see, not a message that may never come. Don't also give it `--report-to`.

**Otherwise, reports** (`--report-to self`): each time the session stops you get a "Gerdr report" message with its
reply and id (not when a turn the user drove by typing in that session's chat simply ends: they've seen it). While
you're busy, reports are held and arrive together when your turn ends; a report not delivered yet is lost if Gerdr
restarts, so check with `gerdr session list` if one seems overdue. With `--report-to`, don't also `--wait` (you'd get
the result twice).

```bash
gerdr session new --cwd ~/code/api --title "fix login" --tag task=t12 --report-to self \
  --prompt "Fix the login redirect bug described in ...; open an MR when done."
gerdr session report-to "fix login" none   # stop its reports
```

A session you started that stops on a **question** is asking you, not the user: decide. Answer what you can with
`gerdr session send <id> "…"` (a message while it's asking is its answer); what's the user's call, put to them with your
recommendation (if the panel you work for gives you a tool for asking the user, use it), then send it their answer. An **approval** (a command / edit to allow) is
the user's alone: tell them, don't work around it.

### Tags: finding the session again

`--tag k=v` (repeatable) labels a session. Gerdr gives tags no meaning; it keeps them and finds sessions by them:

```bash
gerdr session list --all --tag task=t12   # sessions with this tag (--all includes closed ones); --tag mr = has the key
gerdr session tag "fix login" mr=group/api/34 task=   # k=v sets, k= removes
```

Keep tag values to letters, digits and `/ - _ . :`: an unquoted `!` or `#` makes the command count as unsafe, so it
waits for the user's approval.

## 给已有的会话发消息

```bash
gerdr session send reviewer "Also check the tests." --wait
```

它正在跑的话，消息会插进它当前这一轮（不打断）。对方界面上会标出「来自会话 X」，它知道是你发的。
**别给用户自己正在对话的会话发消息**，除非用户明确要你这么做。

## 等、读

```bash
gerdr session wait reviewer --timeout 30m     # 等它停下来，输出最后一轮的回复
gerdr session read reviewer --last 20         # 读最近 20 条（用户说的 / agent 说的 / 调用的工具）
```

`wait` 返回时看 `session.status`：`waiting` 就是卡在等人，照实告诉用户；`timed_out: true` 是还没做完。

## 收尾

```bash
gerdr session close reviewer          # 用完了：从界面上收掉它的 pane（进程停掉，记录和上下文留着）
gerdr session send reviewer "/clear"  # 还要接着派活、但不想带着之前的上下文：清空（claude、codex 都认）
```

`close` 和人手动关 pane 一样：agent 那边关掉（正在跑的那一轮停掉，claude 进程收掉），记录留着，`gerdr session open` 能开回来、再发话会接着原来的对话。
你开出来的会话活干完了就关掉，别让用户自己一个个去关。
用户叫你做完把自己关掉时，`gerdr session close` 你自己的 id（`gerdr session current` 拿）：它不会当场关，
等你这一轮答完才关，所以把它放在最后、照常把要说的话说完。

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
gerdr tab move <tab> --workspace lb [--position 1]             # a whole tab, splits and all
gerdr tab move <tab> --tab <other-tab> --split right           # merge it into another tab
gerdr workspace move <ws> --position 1
gerdr workspace rename <ws> <label>;  gerdr tab rename <tab> <label>
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
in Settings → Agent, Gerdr Agent's model providers…), use `gerdr config`, not an editor on config.json:
it changes one field, checks the value, and open windows update within seconds.

```bash
gerdr config keys                         # every setting and what it does
gerdr config get defaultModes             # read one (no path = all)
gerdr config set theme dark               # value is JSON if it parses, else a string
gerdr config set agents.reviewer.prompt "Review only, don't edit"   # array elements by id or index
gerdr config unset defaultConfig.codex
```

`theme`, `sendKey`, `notifications`, `keepTabs`, `virtualList` and `copyOnSelect` are per device: a device that
picked its own in Settings (marked 仅本设备) keeps it, `gerdr config set` only changes the others. If the user says
it didn't take, tell them to change it in Settings on that device.

Gerdr Agent compacts the context automatically at 90% of the model's window (the composer's context % counts
against the whole window). To compact earlier or later, set `compactAt` on that model, in tokens, not percent:
`gerdr config set llm.providers.<provider>.models.<model>.compactAt 500000` (on a 1M window that is 50%). `window`
on the same model fixes a wrong guess of the window size. Claude Code and Codex compact on their own; this doesn't
touch them.

API keys read back as `<hidden>`; the real ones are in secrets.json. Don't open that file or print the
environment variables named in `apiKeyEnv`: whatever you read ends up in this transcript. To set a key the
user gives you, `gerdr config set llm.providers.<id>.apiKey <key>`.

## Setting up an agent (role)

An agent in Settings → Agent is an engine (claude / codex / native) plus its own prompt, skills, MCP servers and
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

MCP tokens (headers, env) are secrets like API keys: put in only what the user hands you; `list` / `show` print them as
`<hidden>`, and `create` fills a `<hidden>` back from the pool entry of the same name.

### Per project instead of per agent

When the difference is between projects (one project needs company skills / MCP servers, another doesn't), switch
them off in the project, not with a new agent: a role is offered in every project, so a role per project means picking
the right one each time. `projects.<id>.disabledSkills` / `.disabledMcp` take names out of every session in that
project, whatever agent runs it; everything else is given as usual. The user's way is the project's right-click
menu → Skills & MCP. `gerdr config keys projects` has the rest.

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
- Done when `gerdr addon list` shows it with the expected `enabled` and no `error`. A non-empty `error` is a broken
  addon.json or code that doesn't build; a service add-on's stderr is in its `log`.
- Writing a new add-on: read `~/.gerdr/addons/GUIDE.md` first.

## Panels

Besides Code, a project has panels: each a whole app of its own (pages, local functions, skills, schedules, data, its
agent), level with Code on the strip at the left. `gerdr panel` manages them, the same as Settings → Panels; the
project is the current directory's (or `--project`). A panel is named by its name, the project's own by the project id.

```bash
gerdr panel list                                   # key, name, ui.json (title, icon, color), where it came from
gerdr panel install owner/repo [--name board]      # clone a published panel (its code runs here: confirm with the user)
gerdr panel new <name>                             # a blank one, for its agent to build
gerdr panel update [<panel>]                       # git pull; the user's own changes stay on top
gerdr panel rename <panel> "任务看板"                # the name shown for it (ui.json title)
gerdr panel icon <panel> kanban [--color blue]     # glyph / line icon key, or sprite:<key>; no icon = default
gerdr panel rm <panel>                             # to the trash, data and all
gerdr panel restore                                # the project's own panel back (blank) after removing it
```

## Being told when something happens (events)

Gerdr keeps **events** from outside the conversation — MR comments, pipelines, chat messages, whatever a panel's poller
or an add-on reports — as `{id, source, kind, key, title, text, url}`. Instead of polling or sleeping, subscribe and end
your turn; each matching event arrives as a message to you:

```bash
gerdr event list --since 6h                    # what has been coming in (source/kind/key to subscribe by)
gerdr event watch --on 'gitlab/mr.note' --key 'group/repo!34' --note "fix the review comments and push"
gerdr event watch --on 'ci/pipeline.*' --key 'group/repo' --once   # just the next one
gerdr event watches                            # yours; gerdr event unwatch <id>
```

`--on` is `source/kind` with `*`; `--key` narrows to one thing (`*` crosses slashes). `--note` comes back with every
event, so you remember what you meant to do. Only events someone emits arrive: if `gerdr event list` shows none of the
kind you need, nothing polls it yet (a panel's schedule or an add-on has to).

## 并行

要几个 agent 同时干，就开几个会话、都不带 `--wait`，然后按上面「Hand it off without waiting」等它们（能在后台等就每个一个后台 `wait`，否则 `--report-to self`）。别开超过用户要的数量，
每个会话都是一个真的 agent 进程，会花钱、占内存。

## 规矩

- 不要 `cancel` / `close` 不是你开的会话，除非用户要求。
- 把对方的结论转述给用户时说清楚是哪个会话说的。
- 对方在 `waiting`（等审批 / 回答）时，你没有权限替人点，交给用户。
