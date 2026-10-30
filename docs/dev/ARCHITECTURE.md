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
- ACP v1 没有的三件事是扩展方法（`acpx`）：
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
  The one exception is `gerdr workspace|tab|pane …` (internal/server/layoutapi.go, for agents): the server edits layout.json
  itself, then broadcasts it exactly like a PUT from a window. That way it runs once (not once per open window) and works with no
  window open. It edits generic JSON so fields it doesn't know survive. Its tree rules mirror ui/src/lib/actions.ts (movePane,
  mergeTab, removeTab), so change both together. Last writer wins: a window that changed the layout less than 300 ms before (its
  PUT still pending) takes the server's version when the broadcast arrives and drops its own change, which the user then redoes.

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
   **Steers can be rewound too** (Claude Code's own `/rewind` lists them, marked "No code restore"). One the CLI
   picked up after the turn ended is a plain user message with our uuid. One that landed mid-turn is not a user
   message but an `attachment{type:"queued_command", source_uuid:<our uuid>}`: fork at that entry's parent.
   The adapter advertises this as `steering.rewind`; codex doesn't (a steer joins the running turn, `thread/revert`
   only cuts whole turns). The hub closes the cut turn with a `turn_end`, since the real one went with the rest.
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
6. **Code mode leaves no item for `exec` / `wait`** (default on since ~0.166): the model calls one `exec` tool with a JS cell
   that calls the real tools, and `wait` to poll a cell that yielded. The app-server emits items for the nested tool calls only;
   `exec` / `wait` themselves appear nowhere in the protocol (not live, not in `thread/read` / `thread/turns/list`), so a pure-JS
   cell left nothing in the chat (the codex TUI doesn't show them either). `codemode.go` reads them from the rollout file
   (`custom_tool_call` / `function_call` + `*_output`): live by polling before each non-delta notification plus a ticker during
   the turn (the call line is written before the nested tools run, so the card lands above them), on replay merged with the
   turn items by timestamp. `sleep` *is* an item (`type: "sleep"`, id = call id).

## Gerdr Agent（native）—— 自己写的 agent，直接调模型接口

`native` (a public package: other programs embed it, e.g. Annulo; the protocol, extension methods and `_meta`
conventions are in its package doc, an in-process example in `native/example_test.go`): no subprocess; the loop,
tools and transcripts all live in gerdr. It imports none of gerdr's internal packages: gerdr's skills / MCP settings,
MCP sign-in, the login shell's PATH, `GERDR_NATIVE_SESSION_ID` and secrets.json keys are injected through
`native.Options` by `internal/agents/gerdrnative` (keep it that way — `go list -deps ./native | grep internal` must
stay empty). The extension-method types moved to the public `acpx` package for the same reason. The model layer `native/llm`
speaks three protocols (Chat Completions, Responses, Anthropic Messages; the protocol is picked per provider,
`Provider.API`), each hand-written rather than through an SDK so every gateway quirk is visible and fixable in one place.
Why not shuttle's pi: interjections, subagents and rewind are missing from or the wrong shape in pi's loop, and pi is
sixty thousand lines; our loop is about a hundred (`loop.go`) and every step is visible. Config is the `llm` key of
config.json (example in the README), read fresh every turn.

会静默出错的几条（实测 / 抄 pi 的）：

1. **一条 assistant 的每个工具调用都要有结果**，少一个下一轮两家都 400。打断（cancel）时没跑完的调用补「被打断」的结果再结束；
   被打断的半截生成只留正文、不留工具调用和推理（半截的推理还回去会被拒）。
2. **DeepSeek 开着思考时历史里每条 assistant 都要带 `reasoning_content`**（没有给空串），带工具调用的那条要把原来的思考带回去。
   Thinking off ("off" effort, which embedders send even when the model doesn't list it): DeepSeek
   `thinking:{type:disabled}`; Claude Sonnet 5.x `thinking:{type:"between_tools"}` (disabled is a 400), other adaptive
   Claude (Opus 5.5…) `thinking:{type:"disabled"}` — leaving the param out keeps thinking on. Matched by model id.
   Responses 用 `store:false` + `include:["reasoning.encrypted_content"]`，推理 item 原样放回 input；中途换了协议的就丢掉。
3. 经网关的 GPT 在 Chat Completions 里，工具调用第一片之后每片都带 `"name":""`，拼的时候不能拿空名字覆盖（经网关实测）。
   Responses 的 `input` 必须是数组。工具结果里放不了图：图另起一条 user 消息跟在这批结果后面。
   Anthropic: assistant turns are replayed as the exact content blocks the API returned (`Message.Blocks`), not rebuilt
   from Text / Reasoning / ToolCalls: thinking blocks carry signatures, newer Claude models check that earlier turns
   weren't edited, and the split fields lose the block order. Anything that edits a message must clear `Blocks`
   (`keepPartial` does). Thinking needs `display:"summarized"`, or newer models stream empty thinking text.
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
11. **后台任务**（`bg.go`）：bash / task 的 `run_in_background`，或界面上「转到后台」（`_gerdr/task_background`，前台的命令 / 子 agent
    在等的 select 里收到就把自己交给后台 goroutine）。跑完时会话在跑 → 通知排进插话队列；闲着 → 自己开一轮，前后发 `autoTurn` true / false
    （hub 当成 agent 自己跑起来了，和 claude 后台任务跑完一样）。子 agent 的 ctx 不挂在这一轮上，前台时这一轮被打断由 select 转达。
    后台任务只在内存里，gerdr 重启就没了。
12. **MCP**（`mcp.go`，自己写的客户端，只有 initialize / tools/list / tools/call）：stdio、streamable HTTP（响应可能是 JSON 也可能是一段 SSE，
    带 Mcp-Session-Id）、老式 SSE（先等 endpoint 事件）。**只用 Gerdr 那份**（~/.gerdr/ext.json 里开着的），不看设置里的来源：
    以前「Agent 自己的」对它来说是借 Claude Code 配的（~/.claude.json、项目 .mcp.json），分出「引擎」之后用户看不懂一个引擎为什么跟着
    另一个引擎的配置走（用户定的改法）。升级后头一回启动 `ext.MigrateNativeMCP` 把以前借着用的用户级那些拷进 Gerdr 那份一次
    （`<dir>/native-mcp-migrated` 记着；项目 .mcp.json 里的不拷）。skill 还是借 Claude Code 的（~/.claude/skills），没改。
    同一份配置整个 agent 只起一个、各会话共用；起不来的 1 分钟内不重试；每轮最多等 30 秒，没起完的这轮先不带。
    工具名 `mcp__<服务器>__<工具>`，最长 64（两家接口的限制），标了 readOnlyHint 的不审批。**不支持 OAuth**：要登录的服务器（401）给一行提示。
    设置里不单独列它连了哪些、连没连上（以前有一块「在用的」+「检查连接」，用户觉得没必要，删了）：要连的就是「Gerdr 的 MCP」里开着的。
13. web_fetch 按域名审批（`fetch:<host>`）：联网能把数据带出去，不算只读。HTML 自己转成 Markdown 风格的文字（x/net/html），不另调模型总结。
14. **长期记忆**（`memory.go`）：照 Claude Code 的自动记忆，文件 + 索引，不用向量库。`<dir>/native/memory/global`（人的偏好）和
    `projects/<git 根的路径变形>`（项目的事，同一仓库的子目录共用），一条一个 .md（frontmatter 带 type），每层一个 MEMORY.md 索引，
    两层索引原样放进系统提示词，模型按需 read 全文。不加工具：用 write / edit 写、`rm` 删，**这两样在记忆目录里不审批**
    （`decide` 的 memoryWrite / `memoryRm`：只认 .md、rm 不能带通配符和别的命令）—— 每记一条都要人点，就没人让它记了。
    和 Claude Code 的记忆分开存（用户定的）。索引变了系统提示词就变，那一轮前缀缓存不命中，可以接受（记东西不频繁）。
    系统提示词里写明模型名：不写的话问「你是什么模型」它只能猜（用户问过）。
15. **设置 →「记忆」**（`internal/memstore` + `server/memoryapi.go` + `ui/…/MemorySettings.tsx`）：三家的记忆放一页看、改、删。
    Gerdr Agent 的在 `native/memory`；Claude Code 的自动记忆在 `~/.claude/projects/<cwd 变形>/memory/`（格式和我们一样）；
    Codex 的在 `~/.codex/memories/`（它从过去的会话自动总结、原始数据在 memories_*.sqlite —— 那是它的内部格式，不碰）。
    接口能写文件，所以只认这几个目录里的 .md：原样路径对原样根、解开链接的路径对解开链接的根各判一次（macOS 的 /var、/tmp 本身是链接，
    混着比会对不上），新建的文件解开最深一层存在的目录；Claude 那边只认 `<项目>/memory/` 下面的（同一层还有会话记录）。
    Claude 的项目目录名是 cwd 的有损变形，认不回路径：从同目录的会话记录（.jsonl 每行带 cwd）读。删一条时把 MEMORY.md 里指向它的行一起删。
16. **What the user sees while it works** comes from two things that only work together (measured on claude-opus-5-5
    through an Anthropic-compatible gateway, 2026-10-30):
    - The "keep the user posted" line in `basePrompt` (`prompt.go`), copied from Claude Code's system prompt: before
      the first tool call say in a sentence what you're about to do, give a brief update when something matters or the
      direction changes, in the user's language. Without it the model often ran several tool calls in a row without a
      word, and there was nothing to show. It helps every model (GPT / DeepSeek notes show as text between tool calls).
    - Claude's `thinking.display` (`Model.Thinking`): `updates` (beta `thinking-display-updates-2026-08-18`, default on
      Opus / Sonnet 5.5 and Fable 5+, what Claude Code sends) hides the reasoning and returns only the model's own short
      notes, sometimes as thinking, mostly as text. `adaptive` (= `summarized`, default on older Claude) is a separate
      model's long English summary of the reasoning: always there, hard to follow. The default `omitted` streams thinking
      blocks with empty text. With `updates` but without the prompt line, eight steps in a row showed nothing at all.

测试：`go test ./native/...` 用假接口跑主循环；`NATIVE_LIVE_BASE / NATIVE_LIVE_KEY / NATIVE_LIVE_MODELS` 设了才跑真模型（花钱）。

## 用量额度（`_gerdr/usage` → hub/usage.go → `/api/usage` → 侧栏的 UsageMeter）

不发 `/usage`（那会进对话记录、输出是给终端看的文字）。两边都有结构化的口：

- claude：stream-json 的 `control_request{subtype:"get_usage", skip_behaviors:true}`，就是 /usage 背后那个（CLI 里标着
  Experimental）。`rate_limits.limits[]` 是服务端给的整份清单（session / weekly_all / weekly_scoped 带模型名），有它以它为准。
  `skip_behaviors` 省掉扫 7 天本地记录（/usage 的「花在哪」）。没有活着的会话进程时临时起一个、initialize 后问、问完关：
  没说过话的进程不写会话记录，1～2 秒；临时那个不带 skill / MCP 参数。
- codex：app-server 的 `account/rateLimits/read`，`rateLimitsByLimitId` 每个桶 primary / secondary 两个窗口，`resetsAt` 是秒。

hub 每家缓存 2 分钟、并发的只真问一次，所以界面几个窗口一起轮询也不会一直起 claude 进程。

输入框里的 `/usage` 也走这一份：Composer 的 `runLocal` 接住、在输入框上面弹一小块 UsagePanel，不发给 agent（claude 的会起一轮，
codex 的 app-server 没这个命令）。代价是没有 claude 那段「用量花在哪」—— 那段只在 CLI 的 /usage 文字里有。

## 输入框里的 `!命令`（hub/shell.go）

照 Claude Code 的 bash 模式：不发给 agent、不起一轮，gerdr 在会话目录里用登录 shell 直接跑（没有 stdin，自己一个进程组，停止时整组杀）。
stream-json / app-server 都没有这个口（Claude Code 的 `!` 是 TUI 输入框自己做的），三家 agent 一个做法。

- 日志记 `shell` / `shell_out`（攒 200ms 一段，最多 256KB）/ `shell_done`（退出码；stopped / lost；Text = 完整输出的文件），界面画成命令卡片（chat/ShellCard）。
  完整输出另写一份文件（`sessions/<id>.shell/<运行 id>.log`，最多 64MB，删会话时一起删）：日志只记开头，结尾（报错）常在后面。
- 卡片默认收起只露 3 行（照 Claude Code 的「… +N lines」，正好多 1 行就全露）：在跑 / 失败露最后 3 行，成功露开头；收起时不折行。
  原来整段摊开，跑两条 ls 就占满一屏（用户报的）。日志记满时卡片上给「打开完整输出」（文件查看器开那个文件）。
- 长输出照 Claude Code 处理 Bash 长输出的办法：超过 30000 字（或日志记满）不内联，给 agent `<persisted-output>`（文件路径 + 开头、结尾各 2KB），
  要细看它自己去读。原来是掐头去尾、中间直接删掉，agent 再也看不到。
- 带给 agent：下一条消息（包括插话）前面接上 `<bash-input>…</bash-input>\n<bash-stdout>…</bash-stdout><bash-stderr>…</bash-stderr>\n`
  （Claude Code 自己记 `!` 的格式），一条命令一块。「还没带的」= 最后一条用户消息之后跑完的，从日志推，不另存：撤回 / 回退掉那条消息，命令自动算回没带。
  日志里的用户消息记的是带上之后的原样 —— agent 记录里就是这样，CatchUp 比对、重新载入才对得上；块末尾的换行不能省（claude 记录里几块文字直接拼，hub 按换行拼）。
  界面、标题、撤回放回输入框都认 `<bash-input>` 开头跳过这几块；重新载入后没有 shell 事件，靠解析这几块画回卡片。
- agent 不知道有这个功能：Claude Code 只在交互式终端里告诉模型「让用户敲 `! <command>`」，stream-json 下没有。`acpx.ShellHint` 补上，
  并要它写成 `!` 开头的行内代码（或每行都以 `! ` 开头的代码块），界面在旁边画「运行」（chat/Markdown.tsx）。
  认命令要求 `!` 后面有空格、再接像命令开头的字（字母、数字、路径…）：正文里说起这个功能写的「`!命令`」「`! <command>`」、
  代码里的取反 `!Se()` 都不能冒按钮（都踩过）。
- 「运行」旁边的箭头还能放进终端跑（actions.runInTerminal）：新开一个（会话右边分屏，建终端时带 `input`，shell 还在读 profile 时写进去也不丢，
  PTY 先攒着；代价是那行命令会在提示符出来之前先回显一次），或者敲进会话旁边已经开着的终端（`POST /api/terms/{id}/input`，那个 shell 没了就新开）。
  终端能交互（gh auth login 选选项、输密码），但输出不进对话、agent 看不到。ShellHint 只有一句（照 Claude Code 原话的写法），
  末尾括号里交代没有 stdin、要输入的让人从菜单改在终端跑 —— 省掉的话 agent 会把 `! gh auth login` 当成点了就能跑的（实测）。

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
- **Vendor bundle** (`vendor.go`): the import map doesn't point at esm.sh's files directly but at a bundle the
  backend builds from them with esbuild (sources still fetched through `/_esm/` and its disk cache; no npm), one
  entry chunk per specifier plus shared chunks, under `/_esm/~vendor/<key>/`. esm.sh splits packages very finely
  (every package also starts with a re-export stub), and the browser discovers those files one import level at a
  time: measured 414 requests, 67 s cold and 17 s with a warm cache at 150 ms per request (phone via relay);
  bundled, 34 requests, 9.4 s cold (8.7 s of it the server-side build) and 6.6 s at 150 ms. The key is the
  specifier → URL map, so editing the UI's own files never touches it and hot reload is unchanged; adding a package
  changes the import map (already a full reload) and the page shows a "preparing" screen until the new bundle is out
  (about 1–2 s when the packages are cached). One build = one React, the same rule as before. `import()` inside a
  package (shiki's languages) stays external and loads per file. A failed bundle falls back to the per-file map.
  The cold build's time is almost all downloads, not esbuild (0.2 s): 414 files from the esm.sh instance, at
  least 8 import levels deep (a level is only known once the previous one is parsed), each request 1.3–2 s from
  here (through a proxy exiting in Frankfurt; even CloudFront cache hits). Measured 8.7 s and 16.6 s on two runs.
- The page lists everything the first render loads statically as `<link rel="modulepreload">` (after the import map:
  a preload starts module loading, and a later import map is ignored): the UI's own non-lazy modules plus the vendor
  entry files they use and the shared chunks those pull in (`manifest.json`'s `static`). The browser then fetches it
  all in one round instead of one import level at a time. The UI's own files are **not** bundled: hot reload
  imports a changed file by its new URL, and a bundled copy next to it would be a second instance of the same
  module (two zustand stores). Over HTTP/2 (the relay and LAN ports negotiate h2) the request count itself is
  cheap; the levels were the cost. Measured over h2 at 150 ms: 3.6 s → 1.4 s (20 Mbit/s), 5.2 s → 3.8 s (4 Mbit/s,
  where bandwidth is now the limit). Preloading only fetches and parses, it doesn't run anything, so React Refresh
  still hooks in before react-dom executes.
- Responses to non-loopback clients are gzipped (`server/gzip.go`): the first load over the relay is 5.75 MB of text
  uncompressed, 1.42 MB gzipped; at 4 Mbit/s + 150 ms that's 14.0 s → 7.2 s. Loopback is skipped (the App gains nothing).
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
  `/clear` changes the id too, but the tab's gerdr session keeps running under the new one and the conversation before
  it is split off into a detached session that keeps the old agent id and title (`hub/clear.go`): one gerdr session per
  agent session, so /resume lists it like any other. A side list of old ids on the tab (`Record.Cleared`, now migrated
  away on load) went stale once an old context was resumed and showed up twice.
- 发消息带 `from`，对方界面上标「来自会话 X」；`gerdr session new` 默认让界面在调用者旁边分屏打开、不抢焦点（WS 推 `open`）。
- **自动放行**：codex 的沙箱连本机端口都算联网，每条 `gerdr session …` 都会来要审批；codex 的「本会话都允许」又只放行一模一样的那条
  命令 —— 协作时每一步都要人点，没法用。所以两个适配器对「只是一条 gerdr session / ui / workspace / tab / pane 命令」直接放行（`sysenv.SafeGerdrCLI`：
  不能拼别的命令、不能有 `$()`、重定向；**不能带 `--mode`**，否则沙箱里的 agent 开一个 bypassPermissions 的会话就等于给自己提权）。
- **Sessions (herdr's `--session`) keep agents apart.** In the layout a session is a `space` on each workspace
  (`ui/src/lib/layout.ts` `Space`; unset = `default`, so old layouts need no migration). Workspaces stay in one flat list, so
  everything that walks the whole layout (locating a conversation, shutting an agent down once no pane shows it) is unchanged;
  only the views filter (sidebar workspaces and agents, the phone pane switcher, next / previous workspace). The current session
  is the active workspace's, and the active workspace is part of each client's own `View`, so each window shows its own session,
  like attaching to a herdr session. Focusing a conversation that's in another session (a notification) just switches over.
  Isolation for agents: `server.SessionSpaces` maps every conversation shown in a pane to its session; the CLI's `list` (and so
  `resolve`, which every command goes through) drops the ones open in another session, and `send` / `open` / `close` refuse to
  cross on the server (`crossSpace`, using `from` / `near`). Closed conversations belong to no session (`--all` lists them);
  reopening one puts it next to the caller, in the caller's session. This is not a security boundary: an agent has the token
  and can call the API itself. herdr's isn't one either: it's a separate socket.
- **One window per session (App).** "Open in new window" (session bar context menu) and ⌘N open another BrowserWindow on
  `/?space=<id>` (desktop/main.js `pageURL`, through `/auth?next=`). Every window is a full client with its own view, like a
  second browser tab. The page keeps `?space=` current (App.tsx) so a reload stays put; it must not touch it before the layout
  has loaded (the first render uses the default layout and used to overwrite the parameter), and sync.ts reads it at module
  load for the same reason. Each page reports its session to the shell (`space` message): a notification click goes to the
  window showing that session. Every page also sends its own notifications, so the shell drops repeats for 5 s. Menu commands
  go to the focused window. On mac only the last window hides on close; extra windows really close. No ⌘` for switching
  sessions: macOS cycles an app's windows with it.

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
- Scope: Gerdr only manages MCP servers from local config files (its own `ext.json`, `~/.claude.json`, `.mcp.json`,
  `~/.codex/config.toml` `[mcp_servers.*]`). Account-side connectors (claude.ai connectors, the apps connected to the
  ChatGPT account, which Codex exposes through its built-in apps channel) and MCP bundled in Codex `[plugins.*]` are
  neither listed nor toggled: they are managed in the account settings, Codex gives no way to list the account apps, and
  their state changes with the account. That's why an agent asked "what MCP do you have" names more than Settings → MCP.
  One asymmetry with "only Gerdr's": for Claude, `--strict-mcp-config` drops the claude.ai connectors too (tested: 22
  `claude.ai *` servers in the init event without it, none with it); for Codex they stay, since only the
  `mcp_servers.<name>` entries Gerdr read from config.toml get `enabled = false`. Accepted for now: "only Gerdr's" asks
  for a clean set anyway. Keeping the connectors would mean dropping strict and denying the local servers by name
  (`permissions.deny: ["mcp__<name>"]`), which still starts their processes.
- 内置 skill（`internal/ext/builtin/`：gerdr、gerdr-ui）不管来源选什么都给，设置里能单个关。

## MCP OAuth (internal/mcpauth + server/mcpauthapi.go)

- Status = probe: an `initialize` POST per remote server. Claude Code's token is tried first (macOS keychain
  `Claude Code-credentials` → `mcpOAuth`, matched by server URL; elsewhere `~/.claude/.credentials.json`), then gerdr's own
  (`<GERDR_DIR>/mcp-auth/<name>.json`). So a server already authorized in Claude Code is "signed in" here too, and when that
  token dies the row flips to "needs auth" and gerdr can sign in by itself. We never refresh Claude Code's token: a refresh
  can rotate the refresh token and log Claude Code out.
- A 401 doesn't land in the native MCP pool's one-minute failure cache, so a fresh login is used on the next call.
- The callback is `/mcp-oauth/callback`, outside `/api`: the authorization server redirects there cross-site, and the
  SameSite=Strict login cookie isn't sent. The single-use, in-memory `state` is the credential.
- Only gerdr-side MCP clients use these tokens (Native engine, Work functions). Claude Code / Codex sessions still sign in
  their own way.

- Reconnecting one server (Settings → MCP, `POST /api/mcp/{name}/reconnect`): `native.ReconnectMCP` drops its connections in
  every pool of this process (each native agent's and Work's, registered at creation), forgets recent failures, connects once
  to report the tool count or the error; then `_gerdr/mcp_reconnect` asks every live Claude Code process to reconnect it too
  (stream-json control request `mcp_reconnect` with `serverName`, found in the CLI; a process without that server errors and is
  skipped). Codex has no equivalent, so its sessions aren't touched.
- Settings → MCP is one list: a row per server however many configs list it (same name + address), tagged Gerdr / Claude /
  Codex; status = last reconnect, else the sign-in check; edit / disable / delete / add-to-Gerdr / sign out live in its menu.

## Work view (internal/work + server/workapi.go + ui WorkView.tsx)

- **Project** = a layout space (herdr's "session", renamed in the UI): its Code workspaces plus its own Work. **Its id is its
  name** (`layout.ts` projectNameError; no `/ \ .`, not a gerdr route name, unique ignoring case since macOS / Windows paths
  are), and there's no renaming: the address `/<name>` and the Work directory are named after it, so closing a project and
  creating one with the same name later gets its Work back. Older random ids (`s236q0e1`) are renamed to the name on load
  (`nameSpaceIds`, in `withView`). A project's Code comes back too: every layout.json save also writes each open project's
  workspaces to `<GERDR_DIR>/projects/<name>/layout.json` (`config.Store.OnSet` → `work.Runner.SaveLayouts`; closed projects
  keep their last copy), and creating a project reads it back (`GET /api/projects/{name}/layout`, `addSpace`). The global
  layout.json stays the live state of the open projects (windows, devices and `gerdr workspace` work on it). Each project has a
  Work directory — default project `<GERDR_DIR>/work` (where it lived before projects), others `<GERDR_DIR>/projects/<id>/work`
  (`work.Runner.Dir`), installed on first use. The Work agent's current conversation and default agent are per project
  (`config.workSessions` / `workAgents`).
- Which project a `gerdr` command acts in (`server.ResolveSpace`, used by the CLI's `session list` and the layout API alike)
  is looked up from where the caller is, never fixed in an env var at start-up, because workspaces move between projects:
  `--project`, else the pane showing the agent's session, else the Work directory the session runs in (the Work agent is in
  no pane), else the pane showing the terminal (`GERDR_TERM`, the terminal's id, set in every gerdr shell), else unscoped
  (outside gerdr). `crossSpace` places a Work agent in its project the same way, so it can't reach other projects either, and
  sessions it starts open in its project's workspace (`openSessionNear`). Work conversations get no title at creation: a given
  title counts as the user's and would turn off automatic naming.
- Code / Work is gerdr's own switch, at the start of the top row in both modes together with the project switcher (sidebar
  header / Work top bar, same classes) so switching modes doesn't move it; `store.mode` per device. Everything inside Work is
  the user's: `<work dir>/ui` is a React site with no build step, served by its own `uisite.Renderer` under
  `/work/<project>/` (`uisite.NewAt`: module / event / public URLs and the router basename carry the prefix; `/_esm/` is shared),
  started on first request. Menu, pages and styling are plain code in that site, not configuration.
- It runs in a same-origin iframe (the only page allowed to be framed: `X-Frame-Options: SAMEORIGIN`, `frame-ancestors 'self'`),
  so a broken Work page can't take gerdr down; there's no rescue panel inside it. It calls `/api/work/run` with the same
  cookie and talks to gerdr by postMessage: `gerdr:ask` (send a task to the agent), `gerdr:settings`, and gerdr pushes
  `gerdr:theme`. `gerdr work status` shows its build / runtime errors.
- Page logic lives in local functions `<GERDR_DIR>/work/local/*.ts` (esbuild → goja, `ctx.mcp(server, tool, args)`), called by
  name (`gerdr work run file.export`). Bundled files (`internal/work/builtin/base` for every project: site skeleton, blank
  home page, CLAUDE.md; `builtin/okr` laid over it in the default project only) are upgraded unless edited, and removed when
  no longer shipped for that project unless edited.
- The right-hand agent is an ordinary session in the project's Work directory (its CLAUDE.md / AGENTS.md teach it the site),
  not in the layout; `watchedSessions` adds it while in Work mode. `gerdr work run|status` find the project from the cwd.
  New conversations use an engine or a role from Settings → Agent (prompt, skills, MCP, default model).

## 角色和新会话默认（设置 → Agent）

- **角色** = config.json `agents` 里一条带 base（claude / codex / native）的定义：追加的系统提示（`prompt`）、只给哪些 skill / MCP
  （`skills` / `mcp`，按名字；**不写 = 照全局，空数组 = 一个不给**，所以 hub.AgentDef 的这两项不能加 omitempty）、换账号用的 bin / args / env。
  每个角色是一个单独的适配器实例（和「另一个账号」的变体一样），定义一改 SetAgents 就收掉它的连接。
- 限定靠 `ext.Manager.Scoped`：设置、目录、锁和全局的是同一份（`core`；内置 skill 各装各的会在别的 claude 读着时把目录删了重写），
  只在三家各自取 skill / MCP 的地方再筛一遍。只能在「全局来源本来会给的」里面挑，不会加东西进来。
  - claude：skill 壳目录只链挑中的；claude 自己的 skill 没挑的走 `skillOverrides: off`；插件的 skill 关不了单个，**一个都没挑的插件整个关**
    （它的 MCP / hook 一起没）。MCP 按名字关不掉：把挑中的（gerdr 的 + ~/.claude.json / .mcp.json 里的）全写进 `--mcp-config` 再 `--strict-mcp-config`，
    代价是插件的 MCP 和 claude.ai 的 connector 也没了。提示词和 MediaHint 拼成**一个** `--append-system-prompt`（给两个后一个盖前一个）。
  - codex：没挑的 `mcp_servers.<名>.enabled = false`、`skills.config` 里关；system 范围的 skill 界面列不出来，不动。提示词接在 `developerInstructions` 后面。
  - native：`skillsFor` / `resolveMCP` 里筛；提示词接在系统提示词最后。基于 native 的角色和原版共用会话目录和记忆。
    还能只用某几家模型服务商（`providers`，`native.Config.Only`）：两个 agent 都用 native 引擎、各走一家。界面上叫「引擎」
    （Claude Code / Codex / Native，Native 的服务商在 config.json 的 llm）和「agent」（引擎 + 提示词 / skill / MCP / 默认值 / 服务商），用户定的说法。
- **新会话默认**：config.json `defaultModes[agent]` / `defaultConfig[agent][配置项 id]`（内置的和角色一样按 id；Gerdr Agent 的默认模型就是
  `llm.defaultModel`，不另存）。**在后端套**（`hub.Create`，`Options.Defaults` 每次现取）：界面和 `gerdr session new` 开出来的一样。
  三家在 session/new 里能接的配置项不一样（claude 接 model / effort，codex、native 只接 model），所以新建之后对着 agent 报回来的当前值
  补 `SetConfig`：先 model（换模型会重置思考强度）再别的，值不在可选项里的跳过（存的模型可能已经没了）。默认模式 agent 不认不报错
  （`checkMode` 只查调用方点名的）。`gerdr session new` 开的（FromCLI）不套「全放开」的默认模式：那条命令不审批，不能让沙箱里的 agent
  借用户的默认值开一个不设防的会话（和不许带 `--mode` 同一个理由）。
- 编辑默认值的下拉框拿 optCache（上次这种 agent 报的模式 / 配置项，`Agents()` 的 `options`）画；没开过会话的角色用底子的。
  输入框下面每个下拉底部「设为新会话的默认」直接存当前值（`ui/src/lib/defaults.ts`）。

## 后台任务、子 agent、agent 自己开的一轮

- claude 在 stream-json 里报 `task_started / task_progress / task_updated / task_notification / background_tasks_changed`；
  停任务是 control `stop_task`，把前台的命令 / 子 agent 转后台是 `background_tasks {tool_use_id}`（= claude 里的 ctrl+b）。
- **后台任务跑完后 CLI 会自己开一轮**（running → idle，前面没有 user 消息）。没有 session/prompt 对应，适配器用
  session_info_update 的 `_meta.gerdr.autoTurn` 告诉 hub；它可能比上一轮 Prompt 的响应先到，hub 用 `autoNext` 接上，不然那一轮在界面上是「闲着」。
- **`--resume` 一个有没跑完的后台任务的会话**（进程被收掉 / 关了 pane / gerdr 重启时任务还在跑）：CLI 一起来先报那个任务
  stopped、自己跑起来，这时我们那句的 turn 还没挂上；那句常被并进同一段 running 跑完才 idle，中间不再有 running。
  所以「这一轮开始了」还要认 `command_lifecycle started`（command_uuid = 那句的 uuid），只认 running 的话一直转（实测）。
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
- **关 pane = 关 agent 那边**（`Hub.Shutdown`，界面上 pane / tab / 工作空间关掉、会话在布局里哪儿都不显示了才调）：
  claude 进程当场杀掉，不然要白挂到闲置超时（15 分钟、几百 MB）。gerdr 的记录留着，⌘⇧T 放回来、下次发话 `--resume` 接上。
  杀进程让在跑的那一轮报「进程意外退出」—— 正好是上面会自动重试的那种，不拦的话 5 秒后发一句「继续」又把进程拉起来：
  `Session.shut` 标着「是我们关的」，这一轮记成取消、不重试，再接上时清掉。

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
- The composer is shared per session across devices (phone and computer see the same box): the tab sends
  `{t:"draft", id, draft:{text, sel, imgs, from}}` over /ws (throttled 120ms), the hub keeps it in memory (`hub/draft.go`)
  and pushes it to every tab watching that session, the writer included, plus once to a tab that starts watching
  (`init`). Whole-draft last writer wins, no merging. What keeps devices from drifting is the server's order: a tab
  ignores foreign drafts while one of its own is unsent or not echoed back yet (that one lands after them everywhere),
  so it never undoes a newer write of its own with an older one from elsewhere (`sync.ts` gotDraft).
  - `sel`: the caret / selection of whoever typed or moved it last, applied on the other devices (not to an unfocused
    box on a touch screen: the tap that focuses it places the caret, and moving an unfocused field's selection can pop
    the iOS keyboard). Putting a remote draft in moves the caret around (React resets it on the value change), so the
    composer doesn't push selection changes until it's done (`applying`); otherwise two devices echo carets forever.
  - `imgs`: references only, never bytes (the websocket read limit is 1 MiB). A pasted image is stored with
    `POST /api/blobs` (same content-addressed store as transcript images) and shared as its `/api/blobs/<sha256>`; an
    uploaded video as its path. A device that got an image this way has no base64 for it: send fetches the bytes then.
    Images still uploading stay out of the shared list and are kept at the end when a remote draft is applied.

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
