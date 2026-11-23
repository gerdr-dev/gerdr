# 多个 agent 和账号

<sub>[English](agents.md)</sub>

设置 → Agent → 角色 →「加一个 Claude」，起个名字（比如「Claude 2」），给它一套环境变量：
订阅账号用一个单独的 `CLAUDE_CONFIG_DIR=~/.claude-2`（然后在终端里 `CLAUDE_CONFIG_DIR=~/.claude-2 claude` 进去 `/login` 一次），
API key 就写 `ANTHROPIC_API_KEY=…`。还能换可执行文件、追加命令行参数。新建会话时它和原版并排出现在 agent 里，
每个是单独的进程和会话记录目录。Codex 同理（`CODEX_HOME` / `OPENAI_API_KEY`）。
外部 ACP agent 也在这一页加：Gemini CLI（`gemini --experimental-acp`）、OpenCode（`opencode acp`）、[pi](https://github.com/badlogic/pi-mono)（`pi-acp`），需要的话可以给它单独的环境变量。改了立即生效，不用重启。
存在 `config.json` 的 `agents` 里：
`{"id": "claude-2", "name": "Claude 2", "base": "claude", "env": {"CLAUDE_CONFIG_DIR": "~/.claude-2"}, "bin": "", "args": []}`。

## Gerdr Agent（直接用 GPT、DeepSeek、Claude 等模型的 API）

Gerdr 自带一个 agent，不经过 Claude Code / Codex，直接调 OpenAI 兼容的接口
（GPT 走 Responses，DeepSeek 等走 Chat Completions）、Anthropic 的接口（服务商上写 `"api": "anthropic"`，地址比如 `https://api.anthropic.com`）或 Google 的 Gemini 原生接口（`"api": "gemini"`，地址 `https://generativelanguage.googleapis.com/v1beta`，key 用 Google AI Studio 的）。在 `config.json` 里配了模型，新建会话时就多一个「Gerdr Agent」：

```json
"llm": {
  "providers": [{
    "id": "my", "name": "我的网关", "baseUrl": "https://example.com/v1", "apiKey": "sk-…",
    "models": [{"id": "gpt-6.1-sol"}, {"id": "deepseek-v4-pro"}]
  }],
  "defaultModel": "my/gpt-6.1-sol"
}
```

写进 `apiKey` 的 key（设置里填的、`gerdr config set` 写的、手改的）会挪到 `~/.gerdr/secrets.json`（0600），`config.json` 里只留 `"<hidden>"`，这样 agent 读你的设置时不会把 key 带进对话记录。
也可以用 `"apiKeyEnv": "环境变量名"` 从 Gerdr 的环境变量里取，这个变量不会传给 agent 和终端。模型只写 id 就行，协议、思考档位、上下文窗口按 id 猜（服务商的模型列表里给了档位的，比如 subhub 每个模型带的 `"reasoning": {"efforts": […], "default": "…"}`，就用列表里的档位和默认档）；猜错了在模型上写
`api`（chat / responses）、`efforts`、`window`、`compactAt`（上下文到多少 token 自动压缩，默认 `window` 的 90%）、`images`、`maxOutput`，Claude 还可以写 `thinking`（`updates`：只显示模型自己写的简短进度说明，Opus/Sonnet 5.5、Fable 5 及以后默认用它；`adaptive`：显示推理摘要）。也可以在设置 → Agent → 角色 最下面配（能从接口拉模型列表、单个试一下；模型上的 🧠 按钮能看它的思考档位从哪来、自己改档位和默认档）。
**生图**：`gemini-2.5-flash-image` / `gemini-3-pro-image-preview`（Nano Banana）、`gpt-image-1` 这类是生图模型：不出现在会话的模型列表里，专门用来出图。
在设置 → Agent → 角色「引擎」下面的「生图模型」里添加（选一个已有的服务商，或当场新建一个），点星标设为默认，或在 `llm` 里写 `"imageModel": "provider/model"`；不设就自动用第一个（优先 Gemini）。
之后 Gerdr Agent 多一个 `generate_image` 工具，所有 agent（包括 Claude Code、Codex）都能跑
`gerdr image gen "描述" [-i 要改的图.png] [-o 存到.png] [--aspect 16:9]`，`gerdr image models` 列出能用的。请求由 Gerdr 带着你的 key 发出去，agent 拿不到 key。
id 看不出是生图模型的，在模型上写 `"kind": "image"`。

会话记录在 `~/.gerdr/native/`。插话、回退、审批、子 agent（可以放后台）、后台命令、网页抓取、`/compact`、`/clear`、skill、
MCP（用 Claude Code 配的那些 + Gerdr 自己的，不支持要 OAuth 登录的）都支持。
**长期记忆**：你纠正过它的、你的偏好、项目的约定它会自己记下来，新会话也记得。记在 `~/.gerdr/native/memory/`（`global/` 是全局的，
`projects/` 下按项目分），一条一个 Markdown 文件，可以直接看、改、删；`/memory` 看它记了什么，`/memory 忘掉 xxx` 让它删。设置 →「记忆」里能看、改、删，Claude Code 和 Codex 的记忆也在那一页。
