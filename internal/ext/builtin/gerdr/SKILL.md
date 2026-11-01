---
name: gerdr
description: "Coordinate other coding-agent sessions inside Gerdr (Claude Code / Codex sessions in the Gerdr GUI): list sessions, start a new agent session next to you, hand it a task, wait for it, read its reply, or message an existing session. Use when the user asks you to delegate to / start / ask / check on another agent or session in Gerdr, or to run several agents in parallel. Also for arranging Gerdr's layout: sorting tabs into workspaces, splitting panes together, renaming or reordering workspaces / tabs. Also for changing Gerdr's own settings (theme, default agent / mode / model, shortcuts, model providers) with `gerdr config`, and for setting up agents (roles) with their own prompt, skills and MCP servers (`gerdr agent`, `gerdr skill`, `gerdr mcp`). Also for add-ons (`gerdr addon`): browsing add-on stores, installing / updating / removing add-ons, switching them on or off, changing their settings. Requires GERDR_ENV=1."
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
Work view with a Work agent. You only see, and can only reach, the conversations and workspaces of the project you're in:
`list` leaves the others out, and naming one of them fails with "no such session". That is deliberate. Don't try to get
around it, and don't ask the user to move things across for you unless they brought it up.

Which project you're in is worked out from where you are, every time: the pane showing you, or for the Work agent its Work
directory, or the Gerdr terminal a command is typed in. `--project <name>` on `gerdr session|workspace|tab|pane` is only for
running them from outside Gerdr; don't use it to reach into another project.

## 开一个新会话派活

默认就在当前目录、开在你旁边一屏、不抢用户的焦点。没被要求就别换目录、别开一堆：

```bash
gerdr session new --agent codex --title reviewer \
  --prompt "Review the uncommitted diff in this repo and list only actionable problems." \
  --wait --timeout 20m
```

- `--agent` 是 `claude`、`codex` 或 `native`（Gerdr 自带的 Gerdr Agent，直接用配置里的 GPT / DeepSeek 等模型，配了才有）；
  用户在设置里建的**角色**（比如只读代码的 `reviewer`，带自己的提示词、只开某几个 skill / MCP）和另一个账号（`claude-2`）也用它的 id。
  `gerdr session agents` 列出全部能用的、角色带着它的提示词；用户说「让审查那个看看」这种就去那里找对应的角色。用户指定了哪个就用哪个；
- `--title` 起个短名字，之后可以 `gerdr session send reviewer …`；
- 带 `--wait`：等它这一轮做完，输出里 `reply` 就是它的回复；不带就是派出去不等，之后再 `wait`；
- 要它能改文件 / 跑命令而不每次问人，按用户的意思给 `--mode`（claude：`acceptEdits` / `auto` / `bypassPermissions`；codex：`auto` / `full-access`；native：`acceptEdits` / `bypassPermissions`，只读用 `plan`）。
  不确定就别给：用用户给这种 agent 设的默认模式（没设、或者设的是全放开的那种，就是 agent 自己的默认），要做危险操作会在界面上弹审批给人。

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
gerdr session close reviewer          # 用完了：从界面上收掉它的 pane
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
"what the global settings give"; `[]` means none.

MCP tokens (headers, env) are secrets like API keys: put in only what the user hands you; `list` / `show` print them as
`<hidden>`, and `create` fills a `<hidden>` back from the pool entry of the same name.

## Add-ons (Settings → Add-ons)

Add-ons change Gerdr itself: a sidebar panel (quota meter, today's stats), a line under each turn (cache clock),
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

## 并行

要几个 agent 同时干，就开几个会话、都不带 `--wait`，然后挨个 `wait`。别开超过用户要的数量，
每个会话都是一个真的 agent 进程，会花钱、占内存。

## 规矩

- 不要 `cancel` / `close` 不是你开的会话，除非用户要求。
- 把对方的结论转述给用户时说清楚是哪个会话说的。
- 对方在 `waiting`（等审批 / 回答）时，你没有权限替人点，交给用户。
