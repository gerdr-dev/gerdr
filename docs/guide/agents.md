# More agents and accounts

<sub>[简体中文](agents.zh-CN.md)</sub>

Settings → Agents → *Add a Claude*: give it a name (say "Claude 2") and an environment. For a second subscription
use its own `CLAUDE_CONFIG_DIR=~/.claude-2` (then `CLAUDE_CONFIG_DIR=~/.claude-2 claude` in a terminal and `/login`
once); for an API key set `ANTHROPIC_API_KEY=…`. You can also swap the executable or append arguments. It shows up
next to the original when starting a session, with its own process and transcript directory. Codex works the same
way (`CODEX_HOME` / `OPENAI_API_KEY`). External ACP agents (e.g. `gemini --experimental-acp`, `opencode acp`, or `pi-acp` for
[pi](https://github.com/badlogic/pi-mono)) are added on the same page, with their own environment variables if needed. Changes apply immediately. Stored under `agents` in `config.json`:
`{"id": "claude-2", "name": "Claude 2", "base": "claude", "env": {"CLAUDE_CONFIG_DIR": "~/.claude-2"}, "bin": "", "args": []}`.

## Gerdr Agent (GPT, DeepSeek… straight from the API)

Gerdr ships its own agent that skips Claude Code / Codex and calls OpenAI-compatible APIs (Responses for GPT, Chat
Completions for DeepSeek and others), Anthropic's Messages API (`"api": "anthropic"` on the provider, with a base URL
like `https://api.anthropic.com`) or Google's native Gemini API (`"api": "gemini"`, base URL
`https://generativelanguage.googleapis.com/v1beta`, a Google AI Studio key). Configure models in `config.json` and *Gerdr Agent* appears when starting a
session:

```json
"llm": {
  "providers": [{
    "id": "my", "name": "My gateway", "baseUrl": "https://example.com/v1", "apiKey": "sk-…",
    "models": [{"id": "gpt-6.1-sol"}, {"id": "deepseek-v4-pro"}]
  }],
  "defaultModel": "my/gpt-6.1-sol"
}
```

A key written into `apiKey` (by Settings, `gerdr config set`, or by hand) is moved to `~/.gerdr/secrets.json` (0600)
and `config.json` keeps `"<hidden>"`, so an agent that reads your settings doesn't pull the key into its transcript.
Or use `"apiKeyEnv": "VAR_NAME"` to take it from Gerdr's environment; that variable is not passed on to agents or
terminals. A model id is usually enough; protocol, reasoning
levels and context window are guessed from it, and you can override with `api` (chat / responses), `efforts`,
`window`, `compactAt` (tokens at which the context is auto-compacted; default 90% of `window`), `images`,
`maxOutput`, and on Claude `thinking` (`updates`: the model's short progress notes, the default on Opus/Sonnet 5.5 and Fable 5+; `adaptive`: a summary of its reasoning). The bottom of Settings → Agents can do this too (fetch the model list, test a model).

Transcripts go to `~/.gerdr/native/`. Interjecting, rewind, approvals, subagents (backgroundable), background
commands, web fetch, `/compact`, `/clear`, skills and MCP (Claude Code's servers plus Gerdr's; no OAuth servers) all
work. **Long-term memory**: corrections, your preferences and project conventions are remembered across sessions, one
Markdown file each under `~/.gerdr/native/memory/` (`global/`, and `projects/` per project). `/memory` lists them,
`/memory forget xxx` deletes one; Settings → Memory shows them too, alongside Claude Code's and Codex's.
