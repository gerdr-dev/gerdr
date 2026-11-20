---
name: gerdr
description: "Work with other coding-agent sessions inside Gerdr (the GUI you run in; Claude Code / Codex / Gerdr Agent): list sessions, start one next to you and hand it a task, wait for it, read its reply, message an existing one, fork a conversation (same agent or another), import the Claude Desktop App's Code-tab conversations (`gerdr session import claude-desktop`), run several in parallel, sign a session's agent in again. Use when the user asks you to delegate to / start / ask / check on another agent or session. Also another computer paired with this one: run commands there (`gerdr host exec`), copy files / folders (`gerdr host cp`), sync a folder, start a session there; and typing into / reading a terminal pane (`gerdr pane send / read`). Requires GERDR_ENV=1."
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

Other built-in Gerdr skills, each read on its own: **gerdr-browser** (web pages, the Browser pane), **gerdr-setup**
(settings, agents / roles, skills, add-ons, panels, layout, remote access), **gerdr-events** (events, `gerdr wait`,
artifacts).

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
- `--model` / `--effort` pick the model and thinking effort: the ids under that agent's `models` / `efforts` in
  `gerdr session agents`. An id is often an alias (claude's `fable`); its `name` says which version it is now
  (`Fable 5.2`), so check there when the user asks for a specific version instead of guessing. Not given = the user's
  default for that agent; an id it doesn't offer is refused with the list. `models` is missing for an agent that
  hasn't run on this computer yet;
- `--title` 起个短名字，之后可以 `gerdr session send reviewer …`；这个名字固定：不会被自动起的标题换掉，/clear 之后也还在。
  已经开着的会话改名用 `gerdr session rename <会话> <标题>`（加 `--fixed` 一样固定；标题给 `""` 交回自动起名）；
- Fork a conversation (the session menu's "Fork…"): `gerdr session fork <session> [--agent <id>] [--text] [--prompt … --wait]`
  starts a new session carrying that conversation so far, named "Fork: <its title>" (auto-renamed "Fork: <its own topic>" once it's talked to), with the same agent or another;
  the original is left as it is. With the same engine (a claude role forked to claude…) the agent's session is
  copied, so it has the whole context exactly as it was (`--text` hands it over as text instead); another engine
  always gets the history as text with the first message, like switching agents, and so does a session that can't
  be copied. A running source can be forked too (your own session included): the fork gets the conversation up to the turn in progress, which stays with the original. Use it when the user wants to try another
  direction, or another agent's take, without losing the conversation they have;
- Import the Claude Desktop App's Code-tab conversations (Settings → Import does the same): `gerdr session import
  claude-desktop` lists them (JSON: `id`, `title`, `cwd`, `existing` = imported already, `skip` = why it can't be);
  `gerdr session import claude-desktop <id…>` or `--all` (`--archived` to include archived ones) takes them over with
  their whole history, into the current project: one in a project folder into that folder's workspace, the ones in
  the Desktop App's scratch folders into a "Claude Desktop" workspace. The Desktop App's chat conversations (the
  claude.ai kind) live on Anthropic's servers and can't be imported; say so instead of looking for them on disk;
- A project folder was moved or renamed: `gerdr session cwd <session> <new folder>` moves each of its conversations
  there (the conversation and Claude's transcript come along; not while it runs), and `gerdr workspace cwd <ws> <folder>`
  changes where a workspace's new tabs start;
- 带 `--wait`：等它这一轮做完，输出里 `reply` 就是它的回复；不带就是派出去不等，之后再 `wait`；
- 要它能改文件 / 跑命令而不每次问人，按用户的意思给 `--mode`（claude：`acceptEdits` / `auto` / `bypassPermissions`；codex：`auto` / `full-access`；native：`acceptEdits` / `bypassPermissions`，只读用 `plan`）。
  不确定就别给：用用户给这种 agent 设的默认模式（没设、或者设的是全放开的那种，就是 agent 自己的默认），要做危险操作会在界面上弹审批给人；
- `--cwd` defaults to your current directory. As a panel's agent that is the panel's directory, not a repository: always
  give `--cwd` when the task is about code;
- `--workspace <id|number|label|new>` puts it in that Code workspace as a new tab (`gerdr workspace list`); without it,
  it opens next to you.
- A panel's conversation (one a panel started, e.g. a board's worker) can only start a helper that reports back to it:
  `gerdr session new --cwd <repo> --report-to self` (add `--host <id>` for another computer); anything else is refused,
  because the panel looks after the agents it runs. You look after the helper like a subagent: its reports wake you,
  you answer it (`gerdr session send`), the panel shows it under your task, and it closes with you (and with the task).
  It doesn't appear in Code. For a separate piece of work, do it the panel's way, as its instructions say.
- Gerdr records you as the new session's `parent` (also when a panel function you ran with `gerdr panel run` starts it),
  so who handed what to whom can be followed any number of levels deep — a panel can show that tree for its work.
  Starting a helper from your own session keeps the chain; don't hand work out through some other session.


### On another computer: `gerdr host`

The user can pair other computers with this one (Settings → Remote access). `gerdr host list` lists them.

**To run a command there, run it yourself with `gerdr host exec`** — don't start or ask an agent there for it (a turn per
command, slow, and it may refuse or guess). Its output comes back here and the exit code is passed on:

```bash
gerdr host list
gerdr host exec mini -- uname -a
gerdr host exec mini --cwd '~/work/app' -- 'git status --short && npm test'    # one quoted string for && | ; >
cat fix.diff | gerdr host exec mini --cwd '~/work/app' --stdin -- git apply      # --stdin: this side's stdin
```

- It runs in that computer's login shell (its PATH), stdout and stderr together; `--timeout` (default 2m, at most 1h)
  or Ctrl-C stops it and what it started. No interactive input: a command that asks for a password / y/n hangs until
  the timeout; give it its answer up front (`-y`, `--stdin`). `--stdin` sends raw bytes of any size and checks they
  all arrived (exit code 75 if not).
- Something that keeps running (a dev server, `tail -f`, a REPL) goes in a terminal the user can watch. When the user
  has one open on that computer (`gerdr pane list`: kind terminal), type into it and read it back; both work the same
  on this computer's terminals:

  ```bash
  gerdr pane send <pane> 'npm run dev'      # types it and presses Enter (--no-enter: not); returns at once
  gerdr pane read <pane> --lines 50         # what it shows now: read again until the output you wait for is there
  ```

  Don't put a long-running command in `exec` with `&` to dodge this: nobody sees it, and nothing stops it later.
- `gerdr host exec` and `gerdr pane send` run whatever you give them on someone's computer, so they ask for approval like
  any command; don't chain them to get around that.

**To copy files or folders there or back, use `gerdr host cp`** (like scp; not `cat | exec --stdin`, not a session):

```bash
gerdr host cp ./build/app mini:'~/bin/'              # → ~/bin/app there (trailing /: into that folder)
gerdr host cp ./dist mini:'~/www/site'               # a folder: ~/www/site becomes the copy (merged into if there)
gerdr host cp ./src ./go.mod mini:'~/work/app/' --exclude node_modules
gerdr host cp mini:'~/work/promo/out/cover.png' .    # back here
gerdr host cp mini:'~/logs' ./from-mini/             # → ./from-mini/logs
```

- Every file is checked by sha256 on arrival (a mismatch is retried, then exit code 1 with `failed` in the JSON),
  files already the same are skipped (re-run after a break), the executable bit goes along, hidden files and `.git`
  are copied (`--exclude` what you don't want). Nothing is deleted on the receiving side. Any size; progress on stderr.
- A folder source never lands one level deeper: `gerdr host cp ./dist mini:'~/www/site'` twice gives the same result.

**To hand a whole task to an agent there** (it needs that machine's files, GPU or accounts for many steps of its own):
a session there shows in `list` with `"host": "<id>"`, and its id starts with `<host>:`. Everything above works on it the
same (`send`, `wait`, `read`, `--report-to`, tags). Start one there with `--host`:

```bash
gerdr host list
gerdr session new --host mini --cwd '~/work/promo' --title cover \
  --prompt "Render the launch cover image into out/cover.png" --report-to self
```

- Its files are **that computer's**: a path in its reply doesn't exist here. To hand a file to a session on another
  computer, attach it: `gerdr session send <session> "..." --file out/cover.png` copies it there and tells it the path.
- To get a file it made: `gerdr session fetch <session> out/cover.png [dest]` (relative to its directory), or
  `gerdr host cp mini:<absolute path> .` — files and folders, no session needed.
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
- `--cwd` of `session new --host` / `host exec` is absolute or `'~/…'` in quotes: unquoted, your shell turns `~` into *this* computer's home.
  Without `--cwd`, that computer's home. Same for `gerdr folder`'s and `gerdr host cp`'s `host:'~/…'`.
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
A session you start with `--report-to self` is your helper: when your conversation ends (closed, deleted, `/clear`)
it is closed with you, and its own helpers with it, the way a subagent ends with its agent. To keep one going on its
own, stop its reports first (`gerdr session report-to <it> none`).

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

### One session's own settings

```bash
gerdr session set "nightly sweep" --mute on              # no notifications when it finishes, waits or fails; it still turns unread
gerdr session set "billing task" --skill-updates off     # don't tell this one when its skill files change
gerdr session set "billing task" --skill-updates default # back to the global setting
```

`--mute`: for a session the user reads when they choose to, or when they say they don't want to hear about it (`gerdr
session mute <session>` is the same). A whole group of tabs is muted from the UI (the workspace's do not disturb).

`--skill-updates`: by default every session is told when a file of its skills (a panel's, a project's) changes: its
next message starts with the changed files and asks it to read them again, a one-line notice in the chat. That keeps
work running for days or weeks on the current rules. It's the global setting `skillUpdates` (Settings → Agent →
Skill; `gerdr config set skillUpdates false` turns it off for all); `set` overrides it for one session.

## 给已有的会话发消息

```bash
gerdr session send reviewer "Also check the tests." --wait
```

它正在跑的话，消息会插进它当前这一轮（不打断）。对方界面上会标出「来自会话 X」，它知道是你发的。

In that session's chat your message shows folded to its first line (a click opens the rest; only what a person
types shows whole); `--fold "one line"` gives the line yourself. The agent always gets all of it.

**别给用户自己正在对话的会话发消息**，除非用户明确要你这么做。

## 等、读

```bash
gerdr session wait reviewer --timeout 30m     # 等它停下来，输出最后一轮的回复
gerdr session read reviewer --last 20         # 读最近 20 条（用户说的 / agent 说的 / 调用的工具）
```

`wait` 返回时看 `session.status`：`waiting` 就是卡在等人，照实告诉用户；`timed_out: true` 是还没做完。

### Its agent's login expired

A session whose last turn failed because its agent isn't signed in any more shows `status: error` and a `login` field
(the command that signs in). Sign it in again from Gerdr, no terminal needed:

```bash
gerdr session login reviewer              # starts it: a browser opens on the Gerdr computer; prints {state, url}
gerdr session login reviewer --code XXXX  # the person isn't at that computer: they open url, sign in, give you the code
gerdr session login reviewer --status     # done / failed / still running
```

Signing in needs the person (they authorize in the browser): tell them, don't wait silently. Once `state` is `done`,
resend what failed.

## 收尾

```bash
gerdr session close reviewer          # 用完了：从界面上收掉它的 pane（进程停掉，记录和上下文留着）
gerdr session send reviewer "/clear"  # 还要接着派活、但不想带着之前的上下文：清空（claude、codex 都认）
```

`close` 和人手动关 pane 一样：agent 那边关掉（正在跑的那一轮停掉，claude 进程收掉），记录留着，`gerdr session open` 能开回来、再发话会接着原来的对话。
你开出来的会话活干完了就关掉，别让用户自己一个个去关。
用户叫你做完把自己关掉时，`gerdr session close` 你自己的 id（`gerdr session current` 拿）：它不会当场关，
等你这一轮答完才关，所以把它放在最后、照常把要说的话说完。

## 并行

要几个 agent 同时干，就开几个会话、都不带 `--wait`，然后按上面「Hand it off without waiting」等它们（能在后台等就每个一个后台 `wait`，否则 `--report-to self`）。别开超过用户要的数量，
每个会话都是一个真的 agent 进程，会花钱、占内存。

## 规矩

- 不要 `cancel` / `close` 不是你开的会话，除非用户要求。
- 把对方的结论转述给用户时说清楚是哪个会话说的。
- 对方在 `waiting`（等审批 / 回答）时，你没有权限替人点，交给用户。
