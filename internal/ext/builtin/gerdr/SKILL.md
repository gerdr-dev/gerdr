---
name: gerdr
description: "Coordinate other coding-agent sessions inside gerdr (Claude Code / Codex sessions in the gerdr GUI): list sessions, start a new agent session next to you, hand it a task, wait for it, read its reply, or message an existing session. Use when the user asks you to delegate to / start / ask / check on another agent or session in gerdr, or to run several agents in parallel. Also for arranging gerdr's layout: sorting tabs into workspaces, splitting panes together, renaming or reordering workspaces / tabs. Also for changing gerdr's own settings (theme, default agent / mode / model, shortcuts, agents, model providers) with `gerdr config`. Requires GERDR_ENV=1."
---

# gerdr：和其他会话协作

gerdr 是一个跑 Claude Code / Codex 的图形界面，里面同时开着好几个会话（每个会话是一个 agent）。
`gerdr session …` 这组命令让你列出它们、开新的、给它们派活、等它们做完、读它们说了什么。

先确认你确实跑在 gerdr 里，不是就说明一下然后停：

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
- `waiting`：在等人审批命令或回答问题 —— 只有人能处理，告诉用户去 gerdr 界面上看，别替人决定；
- `error`：上一轮出错了（`error` 字段是原因）。

`list` 只列**开着的**会话 —— 就是用户在界面上看得见的（和 herdr 一样：用户关掉 pane 就是关掉那个会话）。
关掉的会话记录还在、能接着聊：`gerdr session list --all` 能看到（`closed: true`），
`gerdr session open <会话>` 把它开回界面上（开在你旁边，不抢焦点）；给它 `send` 也会自动开回来。

The user splits gerdr into isolated **projects** (like herdr's `--session`): each has its own workspaces (Code) and its own
Work view with a Work agent. You only see, and can only reach, the conversations and workspaces of the project you're in:
`list` leaves the others out, and naming one of them fails with "no such session". That is deliberate. Don't try to get
around it, and don't ask the user to move things across for you unless they brought it up.

Which project you're in is worked out from where you are, every time: the pane showing you, or for the Work agent its Work
directory, or the gerdr terminal a command is typed in. `--project <name>` on `gerdr session|workspace|tab|pane` is only for
running them from outside gerdr; don't use it to reach into another project.

## 开一个新会话派活

默认就在当前目录、开在你旁边一屏、不抢用户的焦点。没被要求就别换目录、别开一堆：

```bash
gerdr session new --agent codex --title reviewer \
  --prompt "Review the uncommitted diff in this repo and list only actionable problems." \
  --wait --timeout 20m
```

- `--agent` 是 `claude`、`codex` 或 `native`（gerdr 自带的 Gerdr Agent，直接用配置里的 GPT / DeepSeek 等模型，配了才有）；
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

When the user asks you to tidy up gerdr (sort tabs into workspaces, put two conversations side by side, rename things), use
`gerdr workspace|tab|pane …`. They're shaped like herdr's commands of the same names and output JSON. Only do what was asked:
the layout is the user's desk.

```bash
gerdr workspace list                     # workspace_id, label, number (sidebar order), tab_count
gerdr tab list [--workspace <ws>]        # tab_id, label, pane_count, split: row(a, b) = a left of b, col(a, b) = a above b
gerdr pane list [--workspace <ws>]       # pane_id, kind (agent / terminal / empty), session {id, title, status, cwd}; self: true is you
gerdr pane current                       # your own pane
gerdr pane layout [<pane>] [--tab <tab>] # each pane's rect in its tab
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

## Changing gerdr's settings

When the user asks to change a gerdr setting (theme, send key, default agent / mode / model, shortcuts, agents
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

API keys read back as `<hidden>`; the real ones are in secrets.json. Don't open that file or print the
environment variables named in `apiKeyEnv`: whatever you read ends up in this transcript. To set a key the
user gives you, `gerdr config set llm.providers.<id>.apiKey <key>`.

## 并行

要几个 agent 同时干，就开几个会话、都不带 `--wait`，然后挨个 `wait`。别开超过用户要的数量，
每个会话都是一个真的 agent 进程，会花钱、占内存。

## 规矩

- 不要 `cancel` / `close` 不是你开的会话，除非用户要求。
- 把对方的结论转述给用户时说清楚是哪个会话说的。
- 对方在 `waiting`（等审批 / 回答）时，你没有权限替人点，交给用户。
