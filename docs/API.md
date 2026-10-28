# 用程序接 gerdr（手环、脚本、自己的小工具）

gerdr 界面上能做的事都走一套 HTTP 接口，外面的程序拿着令牌也能调：看哪些会话在跑、读它说了什么、给它发消息、
**替人点审批**。agent 自己用的是 `gerdr session …` 命令行（内置的 gerdr skill），见 `gerdr session --help`。

> 命令行**有意**不提供审批：那是给 agent 用的，agent 不该批准自己（或自己派出去的活）要做的事。
> 审批留给人 —— 人在界面上点，或者用下面这套接口，从手环 / 手机上点。

## 认证

- 地址：本机 `http://127.0.0.1:7870`（`GERDR_PORT` 改过就用改的；`gerdr url` 能看到）。
- 令牌：`~/.gerdr/token`，放在 `Authorization: Bearer <令牌>` 里。
- 所有**写操作**（POST / PUT / DELETE）再带一个 `X-Gerdr: 1` 头（挡 CSRF：跨站网页发不出自定义头）。
- 令牌等于能在这台机器上跑命令，别把它放到别人能看见的地方。远程（手机在外面）走的是另一条路，本机令牌在远程不认，
  见 [dev/REMOTE.md](dev/REMOTE.md)。

```bash
T=$(cat ~/.gerdr/token)
curl -s -H "Authorization: Bearer $T" http://127.0.0.1:7870/api/pending
```

## 有哪些会话、谁最近动过

`GET /api/sessions` —— 全部会话。每个有：

| 字段 | |
|---|---|
| `id` | gerdr 的会话 id（下面的接口都用它） |
| `agent` | `claude` / `codex` / 设置里加的变体 id；`base` 是它实际是哪一种 |
| `title`、`cwd`、`branch` | 标题、目录、git 分支 |
| `status` | `idle` / `running` / `waiting`（在等人审批或回答）/ `error` |
| `pending` | 在等人处理的请求有几个 |
| `waiting` | 在等人处理什么，一句话（最早的那个）：`回答：下一步做什么？（提交 / 先看看）`、`审批：Write x.txt`。手环只轮这个列表就能显示 |
| `updatedAt` | 最后一次有动静的时间（unix 毫秒）。按它倒序 = 最近动过的在前 |

命令行 `gerdr session list` 给的是同一份（只列界面上开着的，`--all` 全部），已经按 `updatedAt` 排好。

## 读它说了什么

`GET /api/sessions/{id}/events` —— 这个会话的完整日志（gerdr 界面就是拿它画的），一个事件数组。看 agent 说的话：
`kind: "update"` 且 `update.sessionUpdate == "agent_message_chunk"` 的 `update.content.text`。

只要「最后一轮的回复」的话，命令行更省事：`gerdr session read <会话> --last 5`、`gerdr session wait <会话>`。

## 替人点审批

1. `GET /api/pending` —— 所有会话里在等人处理的请求，早的在前：

   ```json
   [{
     "sessionId": "2f1b3b5f63da", "title": "approvetest2", "agent": "claude", "cwd": "/private/tmp",
     "reqId": "e199aafa7d8a", "kind": "permission", "at": 1793164600000,
     "summary": "审批：Write gerdr-approve-test.txt",
     "request": {
       "toolCall": { "title": "Write gerdr-approve-test.txt", "kind": "edit", "rawInput": { "...": "..." } },
       "options": [
         { "optionId": "allow_once",   "name": "允许",             "kind": "allow_once" },
         { "optionId": "allow_always", "name": "允许，以后不再问", "kind": "allow_always" },
         { "optionId": "reject_once",  "name": "拒绝",             "kind": "reject_once" }
       ]
     }
   }]
   ```

   `kind` 有两种：`permission`（要不要让它跑这个工具，`request` 是 ACP 的 RequestPermissionRequest）和
   `ask`（agent 问人问题，claude 的 AskUserQuestion / codex 的 requestUserInput，`request.questions` 是题目）。

2. `POST /api/sessions/{sessionId}/answer`，body：

   ```json
   { "reqId": "e199aafa7d8a", "response": { "optionId": "allow_once" } }
   ```

   - 审批：`response` 是 `{"optionId": "<options 里的一个>"}`，拒绝也是挑 `reject_*` 那个；
   - 提问：`{"answers": {"<题目>": "<答案>"}}`；
   - 提问不想答：`{"cancelled": true, "reason": "skipped"}` —— agent 会被告知「人主动跳过了，别再问」；
     不带 `reason` 的话 agent 只知道问题作废了。
   - 提问挂着时直接 `send` 一句话，也算回答（照 Claude Code：提问时打字 = 回答），agent 拿到的是这句原话。

   回过的、或者那一轮已经结束了的请求会报「这个请求已经过期了」—— 界面上和手环上同时点，后点的那个拿到这个错，正常。

**选项按 `optionId` 挑，别按位置**：不同 agent、不同工具给的选项不一样多（有的没有「以后不再问」）。
这也是用 gerdr 做这件事比往终端里模拟按键（herdr 的 `send-keys`）可靠的地方：不用猜光标停在哪一项上。

## 发消息

`POST /api/sessions/{id}/send`，body `{"content": [{"type": "text", "text": "…"}], "id": "<自己生成的 UUID>"}`。
它在跑的话会插进它这一轮（不打断）。`id` 用来去重：网络不好重发同一条时拿同一个 id，不会发出两条。

回 409 + `"code": "held_elsewhere"`：这个会话还在别处（herdr / 终端里的 claude、另一个 Codex）开着，两边同时写会分叉。
界面上会让人挑怎么处理；程序这边可以 `POST /api/sessions/{id}/takeover` `{"action": "fork"}` 另起一份再重发。

## 实时推送

`GET /ws`（WebSocket，同样带令牌）：发 `{"t": "watch", "ids": [...]}` 订阅会话，收会话列表和日志的变化。
界面就是靠它实时刷新的；手环这种不需要实时的，隔几秒轮一次 `/api/pending` 就够了。
