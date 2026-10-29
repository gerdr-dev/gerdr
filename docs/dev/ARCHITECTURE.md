# gerdr 的结构和那些实测出来的坑

## 为什么不是 TUI

herdr-web 那一套是往 TUI 的输入框里模拟按键、再读屏抽话。审批、提问、回退、接续会话全都
只能这么做，脆（换个 agent 版本屏幕长得不一样就坏）。gerdr 直接对接 agent 的**结构化协议**，
界面自己画。

## 分层

```
React（ui/，不打包）  ←WebSocket / HTTP→  hub（internal/hub）  ←ACP（进程内管道）→  适配器  →  agent
                                                                            claude：stream-json（stdio）
                                                                            codex ：app-server JSON-RPC（stdio）
                                                                            native：直接调模型接口（GPT / DeepSeek…）
                                                        ←ACP（stdio）→  外部 ACP agent（gemini…）
```

- **适配器是完整的 ACP agent**（`internal/agents/{claude,codex}`，实现 acp-go-sdk 的 `Agent`）。
  hub 和它们之间走的是真的 ACP JSON-RPC，只是管道在进程内（`agenthost.StartInProc`）。
  同一份代码 `gerdr acp claude` 就是一个 stdio ACP agent。参考实现是官方 TS 适配器
  `agentclientprotocol/claude-agent-acp` 和 `agentclientprotocol/codex-acp`（不是 zed-industries
  下那个已归档的）—— 改适配器前先去读它们对应的那段和测试，边界情况都在那儿。
- ACP v1 没有的三件事是扩展方法（`internal/acpx`）：
  - `_session/steering`：插话。名字和形状照抄两个官方适配器，以后换回官方的 hub 不用改。
  - `_gerdr/rewind`：回退对话（只管对话，文件归 hub）。
  - `_gerdr/ask`：agent → client 提问（claude 的 AskUserQuestion、codex 的 requestUserInput）。
- **hub 的会话日志是唯一事实来源**（`~/.gerdr/sessions/<id>.jsonl`）：用户消息、原样的 ACP
  `session/update`、一轮的开始结束、审批 / 提问及其回答。前端拿整份日志用纯函数归并成
  AI SDK 的 `UIMessage[]`（`ui/src/lib/transcript.ts`），用 AI Elements 画。刷新、换设备、
  重启看到的都一样。同一条消息的流式片段在内存里合并后才落盘（`chunkKey`），一轮几千个
  token 级片段落到文件里只剩几条。
- **布局**（工作空间 / tab / pane 树）存在 `~/.gerdr/layout.json`，服务端不解释，前端说了算，
  多个窗口之间靠广播同步。

## Claude（stream-json）—— 没有公开文档，照 Agent SDK 的 sdk.d.ts 和抓包写的

起法：`claude --output-format stream-json --verbose --input-format stream-json
--permission-prompt-tool stdio --include-partial-messages [--model] [--permission-mode] [--effort]
[--session-id | --resume [--resume-session-at --fork-session]]`，先发 `control_request{subtype:initialize}`。

会静默出错的几条：

1. **环境变量 `CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS=1` 必须带**。不带的话 CLI 不报
   `system/session_state_changed`，只剩 `result` 能当「一轮结束」—— 而插话会把正在跑的那次生成
   截断、先出一个 `result`（stop_reason=tool_use），插进来的话作为第二次接着跑。拿第一个 result
   当结束的表现是：插话之后这一轮被提前判完，插的那句话的回复没人收。官方 SDK 起 CLI 时也带它。
   一轮结束 = 看到过 `running` 之后的 `idle`。
2. **插话**就是往 stdin 再写一条 user 消息，带 `priority`：平时 `"now"`；正等人审批 / 回答时
   用 `"later"` —— `now` 会把那个等待中的 can_use_tool 回调掐掉，卡片撤了，这一轮卡死（官方
   适配器踩过）。user 消息要带 `origin:{kind:"human"}`。
3. **回退**：每条 user 消息带 `uuid`（= gerdr 的 messageId，必须是 UUID），CLI 原样存进会话记录。
   回退时在 `~/.claude/projects/*/<sessionId>.jsonl` 里找到这条的 `parentUuid`，从那儿
   `--resume 旧 --resume-session-at parent --fork-session --session-id 新` 分叉。原会话不动；
   ACP 的 sessionId 因此会变（`RewindResponse.SessionId`），hub 要换 key。
   **分叉出来的新会话要等下一句话写进去才有记录**：进程起来了不算数（这句被 Esc 撤回、进程被收掉，下次还得照样分叉），
   适配器以「新会话的记录在不在」判断分叉成了没有。「从哪分叉」agent 只记在内存里，所以 `RewindResponse.Fork`
   交给 hub 存进 `Record.Fork`，接回会话时放在 `_meta.gerdr.fork` 带回去 —— 不然 gerdr 一重启，
   第 4 条的兜底会把它起成一个空会话，前面的对话全丢（踩过：claude 说「这是第一条消息」）。
4. **建了会话没说过话就没有会话记录**，`--resume` 会报 "No conversation found"。适配器起进程前
   先看记录在不在，不在就按新会话（同一个 id）起；hub 那一侧对任意 agent 也兜了一层
   （接不回 + 一条用户消息都没有 → 新建一个顶上）。
5. 会话记录的目录名是 cwd 的有损变形（非字母数字换成 `-`，长路径还会截断），**同一个目录下
   可能混着别的 cwd 的会话** —— 列「以前的会话」时以记录里的 `cwd` 字段为准。
6. 审批：`control_request{subtype:can_use_tool}`，回 `{behavior:"allow", updatedInput, toolUseID}`
   或 `{behavior:"deny", message}`。AskUserQuestion 也走这里：answers 塞进 `updatedInput.answers`
   （`{问题原文: 答案}`，多选用 ", " 连）。CLI 撤回请求发 `control_cancel_request`。
7. 用户的 `~/.claude/settings.json` 照常生效（比如 `defaultMode: auto` 时多数操作不会来问）。
8. **auto 模式下分类器拦的调用不走 can_use_tool**，CLI 自己拒、只在工具结果里留一句
   「…denied by the Claude Code auto mode classifier…」；stream-json 里也没有 /permissions。所以
   适配器认这句话，给工具调用挂 `_meta.gerdr.autoDenied`（能放行它的规则），界面上「允许并重试」=
   `_gerdr/allow_rules` 把规则写进 `<cwd>/.claude/settings.local.json` + 发一句话让它重试。实测：
   - 设置文件改了 CLI **不用重启**就生效（它监视文件）。
   - Bash 规则**逐段匹配**：`a && b` 写成一条精确规则匹配不上，要拆成 `Bash(a)`、`Bash(b)`（它自己的审批建议也是一段一条）。
     带重定向的命令，写的目标路径另有一道检查，规则管不到。
   - 用户在对话里明说要跑的命令分类器基本都放行，想造一个稳定的拒绝很难，所以 hook（PreToolUse 返回 allow）
     那条路没验证成，没用。

内存：一个会话一个 claude 进程（200–400MB）。闲置超时收掉进程（`claude.Options.IdleTimeout`），
下一轮 `--resume`。改思考强度只能换进程（`--effort`），标记 `respawn` 在下一轮前换。

## Codex（app-server）—— 有文档、有 schema

`codex app-server` 一个进程挂所有 thread。协议：按行 JSON-RPC（没有 `"jsonrpc"` 字段）。
类型：`codex app-server generate-ts --out <dir>`。npm 装的 `codex` 是 node 壳，`sysenv.FindCodex`
找包里的原生二进制直接起。

1. 插话 = `turn/steer {threadId, expectedTurnId, input}`，追加进当前这一轮（不打断）。
2. 回退 = `thread/revert {threadId, beforeTurnId}`，就地截断。哪一轮：`turn/start` 带的
   `clientUserMessageId` 会被存成 userMessage item 的 `clientId`，重启后也能用 `thread/turns/list` 找回。
3. 默认模式下 codex **没有阻塞式提问工具**：它把问题写在一条 `delivery:"async"` 的 agentMessage
   里（带 `questions`），然后自己 sleep 等回复。适配器把选项挂在 `_meta.gerdr.quickReplies`，
   界面画成按钮，点了就是一次插话。`item/tool/requestUserInput` 只在 plan 协作模式里出现，也接了。
4. 审批按轮覆盖：模式 = `approvalPolicy` + `sandboxPolicy`，每次 `turn/start` 带上。
5. userMessage item 在实时流里忽略（client 自己已经显示了），只在 LoadSession 重放时发。

## Gerdr Agent（native）—— 自己写的 agent，直接调模型接口

`internal/agents/native`：没有子进程，主循环、工具、会话记录都在 gerdr 里；模型层 `native/llm` 只有两种协议
（Chat Completions、Responses）。为什么不用 shuttle 的 pi：插话、子 agent、回退这几样 pi 的主循环里没有或形状不对，
而它整套有六万行；自己的主循环一百多行（`loop.go`），每一步看得见。配置是 config.json 的 `llm`（README 有例子），每轮现取。

会静默出错的几条（实测 / 抄 pi 的）：

1. **一条 assistant 的每个工具调用都要有结果**，少一个下一轮两家都 400。打断（cancel）时没跑完的调用补「被打断」的结果再结束；
   被打断的半截生成只留正文、不留工具调用和推理（半截的推理还回去会被拒）。
2. **DeepSeek 开着思考时历史里每条 assistant 都要带 `reasoning_content`**（没有给空串），带工具调用的那条要把原来的思考带回去。
   Responses 用 `store:false` + `include:["reasoning.encrypted_content"]`，推理 item 原样放回 input；中途换了协议的就丢掉。
3. 经网关的 GPT 在 Chat Completions 里，工具调用第一片之后每片都带 `"name":""`，拼的时候不能拿空名字覆盖（subhub 实测）。
   Responses 的 `input` 必须是数组。工具结果里放不了图：图另起一条 user 消息跟在这批结果后面。
4. **插话不打断工具**：排队，下一次调模型前接到历史末尾；`Now` 只掐当前这次生成。「模型说完了 → 标成空闲」和插话入队在同一把锁里
   （`endIfIdle`），不然中间进来的插话被回成 injected 却没人处理。
5. 流断了（没有 finish_reason / response.completed）报 `stream disconnected`，hub 的 `retryable` 认它、发「继续」重来；
   拿到响应头之前的 429 / 5xx / 网络错误在 llm 层自己重试几次。
6. 没有沙箱，所以「Accept edits」只放行改工作目录里的文件，命令照样问；只读命令（ls、git status、rg……，`permission.go`）任何模式都不问。
7. bash 起在单独的进程组里，打断 / 超时整组杀（`sh -c` 起的 go test、npm 不跟着死会一直挂着）。
8. 会话记录 `<dir>/native/sessions/<id>.jsonl` 存**完整历史**，压缩是追加一条 `compact`，上下文从最后一条往后算 ——
   回退到压缩之前，截掉那条就回到压缩前的上下文。`/clear` 换新 id（和 claude / codex 的同一条路）。
9. 指令文件：`~/.claude/CLAUDE.md`、`~/.codex/AGENTS.md`、git 根到 cwd 每层的 AGENTS.md / CLAUDE.md 都拼进系统提示词；
   skill 列表（内置 + Claude Code 的 + gerdr 的，按设置的来源）只给名字、描述、路径，模型自己去读。
10. bash 的环境里有 `GERDR_NATIVE_SESSION_ID`，`gerdr session current` 拿它认自己。

测试：`go test ./internal/agents/native/...` 用假接口跑主循环；`NATIVE_LIVE_BASE / NATIVE_LIVE_KEY / NATIVE_LIVE_MODELS` 设了才跑真模型（花钱）。

## 用量额度（`_gerdr/usage` → hub/usage.go → `/api/usage` → 侧栏的 UsageMeter）

不发 `/usage`（那会进对话记录、输出是给终端看的文字）。两边都有结构化的口：

- claude：stream-json 的 `control_request{subtype:"get_usage", skip_behaviors:true}`，就是 /usage 背后那个（CLI 里标着
  Experimental）。`rate_limits.limits[]` 是服务端给的整份清单（session / weekly_all / weekly_scoped 带模型名），有它以它为准。
  `skip_behaviors` 省掉扫 7 天本地记录（/usage 的「花在哪」）。没有活着的会话进程时临时起一个、initialize 后问、问完关：
  没说过话的进程不写会话记录，1～2 秒；临时那个不带 skill / MCP 参数。
- codex：app-server 的 `account/rateLimits/read`，`rateLimitsByLimitId` 每个桶 primary / secondary 两个窗口，`resetsAt` 是秒。

hub 每家缓存 2 分钟、并发的只真问一次，所以界面几个窗口一起轮询也不会一直起 claude 进程。

## 回退只回退对话，不动文件

以前 hub 用影子 git 仓库（`~/.gerdr/snapshots/`）给 cwd 打快照、回退时一起恢复文件，已经删了：
cwd 以外改的文件（Bash 写 `~/.zshrc`、隔壁项目）管不到；同一目录的多个会话共享快照，恢复会把别的
会话的改动一起退掉；大目录打快照拖慢发消息。两家 agent 自带的文件回退也靠不住（claude 的 checkpoint
不管 Bash 改的文件，codex 没有），所以干脆不做，要回文件让人自己用 git。

## 界面：没有打包环节（internal/uisite，照 shuttle 的 internal/localsite）

界面就是 `ui/` 这个目录的源码（默认装到 `~/.gerdr/ui`，`GERDR_UI` 指到别处）。目的是**让 agent 能直接改界面**：
「加一个每周额度的显示」不用开发、编译、发版，agent 改完保存，用户眼前的界面就变了（内置的 gerdr-ui skill 教它怎么改）。

- 每个文件用 esbuild 单独编成一个 ESM（`/_ui/m/<路径>?v=<版本>`），本地 import 换成依赖的 URL，第三方包 external。
  版本号 = 自己的内容 + 依赖的版本的哈希，依赖变了往上一路变；循环 import 的一组共用一个版本（Tarjan）。
  编译结果按「路径 + 内容」缓存在内存和 `~/.gerdr/cache/ui/modules/`。
- 第三方包：importMap 只给代码里**真用到**的 specifier 生成（esbuild 报上来的），版本取 `ui/package.json`（写精确版本）。
  URL 指向本机的 `/_esm/` 代理（转 esm.sh，结果落盘，离线也能开）。全页面只能有一份 React：react / react-dom 用 `?dev`
  （React Refresh 要开发版），**别的包一律 `external=react,react-dom`，react-dom 自己也 `external=react`** ——
  不然 esm.sh 按 peer 范围给 react-dom 挑一个 react，和我们的不是同一个模块，hooks 当场报错。
- esm.sh 返回的模块里 import 的是 `/react@…/es2022/…` 这种根路径，经过代理落在我们自己的根上，所以代理要把
  import / export from 里以 `/` 开头的串改写成 `/_esm/…`（`esm.go`）。esm.sh 第一次被请求某个包时现场编译，偶尔 5xx，
  代理重试几次（实测 hast-util-to-parse5 第一次 500、再要就好）。
- 页面：`src/pages/` 按文件路由（react-router），`src/layout.tsx` 可选。入口是生成出来的（`routes.go`），它 import 的
  `react-router` 不在依赖图里，importMap 要无条件带上（漏过一次：入口加载失败、整页卡在「加载中」）。
- 热更新：250ms 扫一次签名，稳定一轮才建（agent 一次改好几个文件别推好几次），经 SSE（`/_ui/events`）推给页面。
  能找到 React Refresh 边界（导出全是组件的 .tsx）就就地替换、状态保留；找不到就重新渲染入口；编译出错盖一层报错，修好整页刷新。
- 样式用 Tailwind browser（浏览器里扫 class 生成），所以 `src/index.css` 里不能 `@import` / `@plugin`，代码里不能 import CSS。
- importMap 只能内联，内联脚本靠每次请求一个 nonce 过 CSP。
- 页面里的报错（渲染出错、点按钮抛异常）POST 回 `/api/ui/errors`，`gerdr ui status` 能看到 —— agent 改完界面靠它确认没改坏。
- 界面目录用 **git 三方合并**升级（`internal/webui/gitmerge.go`，照 shuttle 的模板升级）：`upstream` 分支的每个提交是 gerdr 发的一版
  界面快照（临时 index + commit-tree，不碰工作目录），`main` 是正在用的、人和 agent 在上面改。新版来自两处，走同一条路：
  App 内嵌的那份（启动时 `Install`）和开发时仓库的 ui/（`gerdr ui sync ui` / `make sync-ui`，开着的窗口直接热更新）。
  合之前先把 main 上没提交的改动提交掉；**冲突时 abort、照旧用现在这份** —— 冲突标记留在源码里界面当场编译失败，整个 App 就没法用了。
  `gerdr ui status` 报 `pending_upstream`，agent 跑 `gerdr ui merge` 才把冲突摆出来解决。
  版本号是文件内容的哈希（快照提交里的「界面版本:」），内嵌那份和 sync 那份的排除规则必须一致（`skipFile` ↔ Makefile 的 rsync），
  不然同一份界面算出两个版本、每次启动都白合一次。没有 git 的机器退回老办法（按 `.gerdr-ui.json` 的哈希判断改没改过）。

## 多个会话互相调用（`gerdr session …` + 内置的 gerdr skill）

和 herdr 的 `herdr agent …` 一个用途：一个会话里的 agent 开新会话、派活、等、读回复。

- 找服务：gerdr 起 agent 时往环境里塞 `GERDR_ENV=1`、`GERDR_DIR`，并把 gerdr 所在目录加进 PATH（`sysenv.SetGerdrContext`）。
  其余 `GERDR_*`（令牌、端口）不往下传，CLI 自己从 `<dir>/server.json`、`<dir>/token` 读。
- 认自己：claude 给命令的环境里有 `CLAUDE_CODE_SESSION_ID`，codex 有 `CODEX_THREAD_ID`（实测），CLI 拿它们问
  `/api/resolve` 换 gerdr 会话 id。claude 回退之后会话 id 会变，hub 那边跟着 rekey，所以按 agent id 查永远是当前的。
- 发消息带 `from`，对方界面上标「来自会话 X」；`gerdr session new` 默认让界面在调用者旁边分屏打开、不抢焦点（WS 推 `open`）。
- **自动放行**：codex 的沙箱连本机端口都算联网，每条 `gerdr session …` 都会来要审批；codex 的「本会话都允许」又只放行一模一样的那条
  命令 —— 协作时每一步都要人点，没法用。所以两个适配器对「只是一条 gerdr session / ui 命令」直接放行（`sysenv.SafeGerdrCLI`：
  不能拼别的命令、不能有 `$()`、重定向；**不能带 `--mode`**，否则沙箱里的 agent 开一个 bypassPermissions 的会话就等于给自己提权）。

## 内置 skill、skill / MCP 的来源（internal/ext）

- 来源三选一（skill、MCP 各自选）：只用 agent 自己的（默认）/ 两者都用 / 只用 gerdr 的。gerdr 自己那份：skill 在
  `~/.gerdr/skills/<名>/SKILL.md`，MCP 在 `~/.gerdr/ext.json`。
- claude：skill 走 `--add-dir`（目录下的 `.claude/skills` 会被加载，名字不带前缀）。每次起进程建一个壳目录，只链进这次要给的 skill；
  只用 gerdr 的时候 agent 自己的 skill 用 `skillOverrides: off` 关，**插件里的 skill 按名字关不掉**，只能整个插件 `enabledPlugins: false`。
  MCP 走 `--mcp-config <json>`，只用 gerdr 的加 `--strict-mcp-config`。参数只能启动时给，所以设置一变（指纹）下一轮前换进程。
  claude 自己配的 MCP（~/.claude.json、.mcp.json）也只在启动时读一次，`claude mcp add` 之后进程里的 /clear 不会重读：
  它的指纹另记，变了只在 /clear 时换进程（`--resume` 再清空）—— 中途换的话工具列表变了，提示缓存全废，长对话下一轮整段重新计费。
- codex：`thread/start` / `thread/resume` 的 `config` 是**合并**进用户配置的，给 `mcp_servers = {}` 删不掉任何东西，只能逐个
  `mcp_servers.<名>.enabled = false`；skill 用 `skills.config = [{path, enabled:false}]` 按会话关（会整个覆盖用户的那张表，所以用户自己关掉的要照抄）；
  gerdr 的目录用 `skills/extraRoots/set`（整个 app-server 的设置）。**对一个已经加载着的 thread 再 resume，新 config 不生效**，
  要先 `thread/unsubscribe` 让它卸载再 resume（实测）。
- 内置 skill（`internal/ext/builtin/`：gerdr、gerdr-ui）不管来源选什么都给，设置里能单个关。

## 后台任务、子 agent、agent 自己开的一轮

- claude 在 stream-json 里报 `task_started / task_progress / task_updated / task_notification / background_tasks_changed`；
  停任务是 control `stop_task`，把前台的命令 / 子 agent 转后台是 `background_tasks {tool_use_id}`（= claude 里的 ctrl+b）。
- **后台任务跑完后 CLI 会自己开一轮**（running → idle，前面没有 user 消息）。没有 session/prompt 对应，适配器用
  session_info_update 的 `_meta.gerdr.autoTurn` 告诉 hub；它可能比上一轮 Prompt 的响应先到，hub 用 `autoNext` 接上，不然那一轮在界面上是「闲着」。
- 子 agent：claude 的子 agent 工具调用带 `parent_tool_use_id`；它的话打上 `_meta.gerdr.subagent` 推出来（主对话不显示，只读面板显示）。
  前台子 agent 最后那句话只在 Agent 工具的结果里，而且被 CLI 包了一层「[Subagent hand-back] … The report follows:」+ agentId + usage，要剥掉（`subReport`）。
  codex 的子 agent 是另一个 thread：主 thread 上有 `subAgentActivity` item，子 thread 的通知照样推到同一条连接上，适配器把它挂到主会话下。

## 从 herdr 导入（internal/herdrimport + server/importapi.go）

数据全来自 herdr 的命令行（workspace / tab / pane list，多 pane 的 tab 再问 pane layout），只读。
- 会话 id 是 pane list 里的 `agent_session`（`herdr integration install` 装的 hook 报的，kind 实测是 `id`）。没装 hook 的 agent pane 导不了，列出来说明原因。
- 分屏：pane layout 只给每个 pane 的矩形，按「一刀能把它们分成两边」递归切回二叉树。**rect 的字段是 width / height**，
  结构体不写 json tag 的话 x / y 靠大小写不敏感碰巧对上、宽高全是 0，比例全变 0（踩过）。
- tab 名去掉 tab 编号插件加的「1.」「 Ⅱ」，纯数字（herdr 的默认名）当没有名字。
- 会话还在 herdr 里开着：claude 能接（两边写同一份记录会分叉，界面上提醒先退出），codex 直接拒绝（`already has an active writer`）。
- 已经在 gerdr 里的会话（按 agent 会话 id 认）复用，不导两份。布局是前端的，服务端只建会话、回「哪个位置放哪个会话」。

## 重试

- 发消息带 client 生成的 UUID，网络不好时前端拿同一个 id 重发，hub 认出来不会发两条。
- WebSocket 应用层心跳（浏览器读不到协议层的 ping/pong）：20 秒一问，45 秒没回音收掉重连；回到前台 / 网络恢复立刻问一次。
- 一轮因为网络 / 过载 / agent 进程死了失败：hub 等 5/15/30 秒发一句「继续」重来（不进对话记录，界面上是一行提示）。
  额度用完、参数错不重试。agent 自己在重试 API（claude 的 `api_retry`、codex 的 `willRetry`）也显示一行提示。

## 前端
- 对话是**虚拟列表**（react-virtuoso，`chat/Rows.tsx`）。一行不是一条消息，是消息里的一块（一段文字 / 一个工具调用 / 一张卡片）：
  一轮回复常常就有几百个工具调用，按消息切的话最长那一轮照样整个渲染。行是 memo 的，只比里面的 part 对象 —— 流式时只有最后一行重渲染。
  滚出去会卸载，所以展开状态放在组件外（`lib/kept.ts`）。流式输出时最后一行在长高，followOutput 要等下一拍才跟、落后一两行一顿一顿的，
  所以 `totalListHeightChanged` 里在底部就当场推到底（实测最大落后从 93px 降到 32px）。
- 切走的 tab 不卸载（最近 `keepTabs` 个，visibility 藏，display:none 会丢滚动位置），它们的会话照常订阅，切回来不用整份重推。
  `Transcript.reset` 的 id 从头编：同一份日志重放出来的 id 一样，组件才不会全部重建。

- 快捷键按 `KeyboardEvent.code` 认，不按 `key`：中文输入法开着时 key 是 "Process"。进前缀时
  还会让输入框失焦（不然第二个键先进输入法组字，preventDefault 都拦不住），做完动作再还回来。
- 输入框里回车要挡输入法（`isComposing` / `keyCode 229`）：中文选词就是按回车。
- 插件在前端跑：服务端用 esbuild（Go 版，不需要 node）编成 ESM，前端用 Blob URL `import()`。
  CSP 因此放行 `blob:` 脚本。插件钩子全部 try/catch，出错只记在插件名下。
- WebSocket 推送攒 30ms 一帧（流式片段很碎）；`Msg.Events` 不能 `omitempty`——空日志的会话也要
  推一帧 reset，前端靠它知道「加载完了」（踩过：空会话永远显示「加载中」）。

## 手机上（< 768px，照 herdr-web）

- 一次只画焦点那一个 pane，**不改布局里的 zoom**：布局存在服务端、和桌面共用，手机上看一眼不该把电脑上的分屏变成放大。
  换 pane / 换 tab 走「面板一览」（`PaneSwitcher`：所有工作空间的 pane 拍平，按 等你 > 出错 > 跑完没看 > 在跑 > 闲着 排）。
  标题栏上的 `1/3` 说这个 tab 里还有别的 pane。侧栏变成抽屉，tab 栏换成一行 `PhoneBar`（角标 = 在等你的个数）。
- 面板一览里点一行在 `pointerup` 上就跳，不等 `click`：键盘弹着时同一下先收键盘、`--vvh` 一变整张表重排，click 会派丢
  （herdr-web 真机上实拍的「第一下只收键盘」）。
- `--vvh` = visualViewport 的高度：iOS 弹键盘不缩布局视口，不跟着它走输入框就被盖住。输入框在手机上 16px 字：小于 16px iOS 聚焦时整页放大。
  触屏上回车是换行（软键盘没有 Shift），发送走按钮。

## 对话里的路径原地打开（ui/src/lib/paths.ts + server/fileview.go）

- 认路径的规则照搬 herdr-web（中文标点当终止符、光秃秃的相对路径必须带扩展名、截断的不认……），作为 rehype 插件排在
  streamdown 默认插件**后面**：harden / sanitize 跑完才加 `data-gerdr-path`，不会被清掉；用 data 属性不用假 scheme 的 href，
  href 会过 urlTransform，不认识的协议会被清空。
- `/api/fs/raw` 的响应一律 `CSP: sandbox`，html / xml / js 按纯文本吐：同源下被当页面打开的 .html / .svg 里的脚本会带着我们的 cookie 跑，
  而 agent 写出来 / 抓下来的文件都不可信。PDF 例外（Chrome 的 PDF 查看器在 sandbox 下不渲染）。走 ServeContent 带 Range，视频才能拖。
- 打不开时先原样问、再剥行号锚点重试（`a.md#L21` / `main.go:42`）—— 不能先剥，`notes#1.md` 是合法文件名。

## 同一个会话在别处也开着（herdr / 终端 / Codex App）

导入的会话、在终端里 resume 过的会话，常常同时在 gerdr 和别处开着。实测出来的语义：

- **claude 不拦**：两个进程同时 `--resume` 同一个会话，各自往同一个记录文件里追加、各接在自己看到的最后一条后面
  —— 记录分叉，两边都只看得见自己那一半。判据是交互式 claude 的登记文件 `<配置目录>/sessions/<pid>.json`
  （退出时删，强杀会残留，所以还要核 pid 活着；gerdr 自己起的 stream-json 进程**不写**这个文件）。
  发消息前问一次（`_gerdr/holders`），有人占着就回 409，界面上挑：另起一份（`--fork-session` 复制出新会话）/
  关掉那边（SIGTERM，**等进程退出**而不是等登记文件消失：它先删文件、再把记录写完，中间差一两秒）。
- **codex 自己拦**：一个 thread 同一时间只有一处能写。gerdr 先开着，终端里的 codex 显示「open in another app」；
  终端先开着，gerdr 这边 `thread/resume` 报 `already has an active writer`。不会分叉，但光秃秃一句英文人不知道怎么办，
  所以同样回 409，界面上给「另起一份」（`thread/fork`，不用先接上原来那个）/「已经在那边关掉了，重试」。
  没有登记文件，不知道是哪个进程，所以不提供「关掉那边」。
- **追上**：gerdr 的对话日志只记它经手的事。别处接着聊过，agent 的记录文件会长 —— 但**文件变长不等于别处聊过**：
  自己一轮刚完就重启（来不及记大小）、claude 一轮之后补写的收尾、切模式写的那一行，都会让它变长（第一版只比大小，
  用户撞上了误报）。所以判据是「上次对齐之后（`Record.SyncedSize`），记录里有没有 gerdr 没发过的人话」：agent 把那一段
  里人说的话列出来（`_gerdr/transcript_stat` 的 `since`），和自己日志里的比（空白归一、前 200 字）。有生人话才整份重放
  （`Session.Reload`），否则只把位置挪过去。claude 被打断时自己补写的 `[Request interrupted by user…]` 也是 user 消息，
  不算人话（不然重启一下 App 就误报「在别处接着聊过」）。重放出来的历史消息是 `user_message_chunk`（messageId = 记录里的 uuid），
  不是 gerdr 自己记的 user 事件 —— 回退要两种都认，只认后者的话重新载入过的消息全都「找不到这条消息」。
- **gerdr 被强杀时那一轮没断**：gerdr 被 SIGKILL / 崩溃（App 退出等不及也会：Electron 3 秒就强杀）时来不及杀 claude，
  claude 只看到 stdin 断了，会**把手上这一轮跑完**、写进自己的记录才退（实测又跑了一分钟）。gerdr 日志停在半截、
  重启时补了「中断」，而上面的追上只认生人话，多出来的全是 agent 回复，救不回来。所以重启时日志里有没收尾的一轮，
  就单独核对（`adoptOrphanTurn`）：孤儿还在（stream-json 进程也登记 `sessions/<pid>.json`，重启后已不算自己起的，
  holders 认得出）就等它退；之后记录在 gerdr 最后一条日志之后还被写过（超过日志落盘间隔），就整份重放，否则「中断」留着。
  这条路也顺带补回「一轮其实跑完了、日志没来得及落盘」的那一截。

另一个**静默**的坑：在一个 Claude Code 的终端 pane 里敲 `gerdr`，它会继承那个 claude 给子进程打的标记
（`CLAUDE_CODE_CHILD_SESSION` 等），带着它的子 claude **不写会话记录**（底部一行「Transcript saving is off」，
stream-json 模式下没人看得见）—— resume、回退、追上全部失效。`sysenv.ChildEnv` 按名字去掉这几个。

## 急救面板（server/rescueapi.go + server/rescue/ + webui/history.go）

主界面就是界面目录里可以改的源码，对话面板也在里面：改坏了就没地方叫 agent 修（修东西的工具和被修的是同一个）。
照 shuttle 的做法（它的对话面板在打包进二进制的 web/ 里，助手只改得到项目页面），留一页**改不到**的：

- 一个从页面右侧拉出来的面板，和主界面并排：一边看主界面坏成什么样、修好没有，一边和 agent 说话（主界面让出右边那块，不盖住）。
  源码在 `server/rescue/src`（React），`go generate ./internal/server` 用 esbuild 连 React 一起打成 `rescue/app.js` 入库、
  `go:embed` 编进二进制（包从 `ui/node_modules` 取，编 gerdr 不需要 node）。由 uisite 的页面外壳直接 `<script>` 引入，
  不走界面目录的编译、不用 importmap / esm.sh、挂在自己的 Shadow DOM 里 —— 主界面可能就是坏在这几样上，崩了它也照样能用。
  开没开记在 localStorage，刷新后还在；外壳自己的全屏层（编译错误的红屏、加载中）开着面板时也让开右边。
- 只做修界面要的：看 `/api/ui/status` 的错误、退回之前的一版、在界面目录里开一个会话对话
  （发消息、看回复和工具调用、审批 / 提问、打断）。**别往里加主界面的功能**，越简单越不会坏、越不用跟着维护。
- 入口都不在界面目录里：uisite 生成的两种报错页（编译错误的 overlay、渲染错误的 RouteError）和加载太久的提示上有链接
  （`window.__gerdrRescue.open()`，脚本没加载到就走 `/rescue`，它跳回 `/?rescue=1` 打开面板）；页面报了错、面板又关着时右下角冒一个「急救」；
  App 菜单「帮助 → 急救」先在页面里调 open，不行再 `loadURL`（菜单不经过页面，页面坏了也点得动）。
- 退回（`webui.Restore`）只往前提交：先把现在的样子提交一次存档，再 `read-tree -u --reset <版本>` 按那一版重建
  （它没有的文件删掉），再提交一次。坏的那版留在历史里，改动找得回来；合并到一半的先 abort。
- 新建急救对话时第一句预填好（带上现在的错误），人看一眼再发。

## 终端（internal/term + server/termapi.go + ui/src/components/Terminal.tsx）

终端是一种 pane（布局里 `PaneLeaf.term`），shell 跑在 gerdr 进程的 PTY 里（creack/pty），界面用 xterm.js 经
`/api/terms/{id}/ws` 连上来：二进制帧是输入 / 输出，文本帧是 resize / 退出码 / 心跳。

- **断开再接**：和 tmux 一样，PTY 不跟着页面走。每个终端留最近 512KB 原始输出，连上时先重放一遍（客户端先 `reset` 再写）。
  不在服务端维护屏幕状态（要一个完整的终端模拟器）：重放跨过 resize 的那段可能错位，全屏程序 ctrl+l 一下就好。
  同一个终端可以同时开在几处（电脑 + 手机），输出广播给每个连着的；跟不上的连接直接踢掉，让它重连拿重放。
- **gerdr 重启 = 终端全没**：PTY 是 gerdr 的子进程。布局里留着 id 和 cwd，pane 上给「在 xx 重开」。
- **关 pane 就杀 shell**（SIGHUP 给整个进程组，2 秒后 SIGKILL）：不然它在服务端没人看得见地一直跑。
  shell 自己退出（exit、ctrl+d）时 pane 自动收掉，**不看退出码**：ctrl+d 时 zsh 带的是上一条命令的退出码
  （前面 ctrl+c 过就是 130，踩过）。只有被信号杀掉的（退出码 -1：gerdr 退出、被 kill）留着给「重开」。
- **先建后放**：开终端的动作先 `POST /api/terms` 拿到 id 再写进布局。布局是几个客户端共用的，「pane 里有终端但还没建」
  的中间态被别的客户端看见，它们会各自去建一个。
- **环境**：`sysenv.ChildEnv` 那一份（去掉 claude 的子进程标记），加 TERM / COLORTERM；launchd 起的 gerdr 没有 LANG，
  不补的话 zsh 把中文当转义序列。shell 用 `-l`（PATH 在 profile 里）。
- **分屏跟着 shell 的当前目录**：从终端旁边开新终端时带 `from`，服务端问 shell 的 cwd（mac 上 `lsof -d cwd`，Linux 读 /proc）。
- **前缀键**：全局的前缀状态机（lib/commands.ts）在 capture 阶段先拿到键，所以 ctrl+b 照样是 gerdr 的；按两下前缀
  发 `gerdr:prefix-literal`，终端把 ^B 交给 shell（终端里跑着 tmux 时要用）。
- xterm 的 CSS 不能 import，原样搬进了 index.css 末尾；配色从主题变量取，oklch 画到 canvas 上读回 rgb（xterm 只认 rgb / hex）。
- Windows 上还没做（要走 ConPTY），建终端直接报错。

## 远程访问（internal/auth、relay、acme、remote + server/remoteapi.go）

经云端中继对外，配对码 + passkey；本机令牌在远程不认。设计和坑在 [REMOTE.md](REMOTE.md)，动这块之前先读它。

## 桌面 App（desktop/，Electron）

`desktop/main.js`：Electron 外壳，自带 Chromium（mac / Windows 同一个内核）。为什么不是系统 WebView / mygo / nsh、
外壳的几条坑（退出、Windows 上的父进程、菜单、签名、通用包）都在 [WHY-ELECTRON.md](WHY-ELECTRON.md)。
起内嵌的 `gerdr --app --parent-pid`，读 stdout 里的 `GERDR_READY <url>`；已经有 gerdr 在跑（同一个数据目录有锁）
就直接连它。从 Finder 起的 App 的 PATH 只有系统目录，所以后端开机会问一次登录 shell 的 PATH
（`sysenv.AdoptLoginPath`），不然找不到 claude / codex。打包：`make app`（本机）/ `make dmg` / `make win`。
