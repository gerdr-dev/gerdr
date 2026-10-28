---
name: gerdr
description: "Coordinate other coding-agent sessions inside gerdr (Claude Code / Codex sessions in the gerdr GUI): list sessions, start a new agent session next to you, hand it a task, wait for it, read its reply, or message an existing session. Use when the user asks you to delegate to / start / ask / check on another agent or session in gerdr, or to run several agents in parallel. Requires GERDR_ENV=1."
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

## 开一个新会话派活

默认就在当前目录、开在你旁边一屏、不抢用户的焦点。没被要求就别换目录、别开一堆：

```bash
gerdr session new --agent codex --title reviewer \
  --prompt "Review the uncommitted diff in this repo and list only actionable problems." \
  --wait --timeout 20m
```

- `--agent` 是 `claude` 或 `codex`（用户在设置里加过别的，比如另一个账号的 `claude-2`，也可以用它的 id —— `gerdr session list` 里的 `agent` 字段就是），用户指定了哪个就用哪个；
- `--title` 起个短名字，之后可以 `gerdr session send reviewer …`；
- 带 `--wait`：等它这一轮做完，输出里 `reply` 就是它的回复；不带就是派出去不等，之后再 `wait`；
- 要它能改文件 / 跑命令而不每次问人，按用户的意思给 `--mode`（claude：`acceptEdits` / `auto` / `bypassPermissions`；codex：`auto` / `full-access`）。
  不确定就别给，默认模式下它要做危险操作会在界面上弹审批给人。

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

## 并行

要几个 agent 同时干，就开几个会话、都不带 `--wait`，然后挨个 `wait`。别开超过用户要的数量，
每个会话都是一个真的 agent 进程，会花钱、占内存。

## 规矩

- 不要 `cancel` 不是你开的会话，除非用户要求。
- 把对方的结论转述给用户时说清楚是哪个会话说的。
- 对方在 `waiting`（等审批 / 回答）时，你没有权限替人点，交给用户。
