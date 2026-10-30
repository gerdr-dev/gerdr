# Embedding gerdr's agents

Three ACP agents (`acp.Agent` from github.com/coder/acp-go-sdk), importable by other Go programs:

| Package | Engine | Process model |
|---|---|---|
| `github.com/gerdr-dev/gerdr/agents/native` | gerdr's own loop, calls model APIs directly (`native/llm`) | in-process, no subprocess |
| `github.com/gerdr-dev/gerdr/agents/claude` | Claude Code (`claude` CLI, stream-json) | one `claude` process per session, idle ones reaped |
| `github.com/gerdr-dev/gerdr/agents/codex` | Codex (`codex app-server`, JSON-RPC) | one app-server for all sessions |

Supporting packages: `acpx` (gerdr's ACP extension methods and `_meta` keys), `sysenv` (finding the CLIs, the login
shell's PATH, the child environment), `rpcline` (line-delimited JSON-RPC, used by codex). None of them import gerdr's
`internal/` packages; gerdr injects its own settings through the same Options (`internal/agentwire`).

Wiring is the same for all three: `a := X.New(log, opts)`, `conn := acp.NewAgentSideConnection(a, toClient, fromClient)`,
`a.SetConn(conn)`, then talk to it from an `acp.NewClientSideConnection` over two `io.Pipe`s. A runnable example
(client included) is `agents/native/example_test.go`; the protocol details are in the native package doc.

## Options for embedders

All zero by default (= gerdr's behaviour).

native (`native.Options`): `Dir`, `Config`, `Prompt`, `SystemPrompt` (replaces the base prompt), `NoInstructionFiles`,
`NoMemory`, `Skills`, `Tools` (Go tools), `MCP`, `MCPAuth`, `Env`, `BashEnv`, `SafeCommand`, `Files`, `Images`.

claude (`claude.Options`):

- `Bin`, `Args` (extra CLI flags, e.g. `--allowedTools Bash`), `Env` (K=V on top of the inherited environment;
  `CLAUDE_CONFIG_DIR` picks another account), `Prompt`, `IdleTimeout`.
- `NoGUIHint`: leave gerdr's GUI hint out of `--append-system-prompt`.
- `AppendPrompt func(sessionID, cwd string, meta map[string]any) string`: per-session text for
  `--append-system-prompt` (read when the session's process starts).
- `SessionEnv func(sessionID string, meta map[string]any) []string`.
- `StrictMCP`: only session/new's `mcpServers` (`--strict-mcp-config`: none of the user's servers, no claude.ai
  connectors). Client servers are always passed with `--mcp-config` and pre-approved (`--allowedTools mcp__<name>`).
- `Ext`: gerdr's skill / MCP settings; nil = claude's own (`~/.claude/skills`, the project's `.claude/skills`…).
- `claude.Check(ctx, opts) (Status, error)`: `*sysenv.NotFoundError` = not installed, `claude.ErrNotLoggedIn`.

codex (`codex.NewWith(log, ext, codex.Options)`; ext nil = codex's own skills / MCP):

- `Bin`, `Args`, `Env`, `Prompt`.
- `NoGUIHint`, `AppendPrompt` (goes into the thread's `developerInstructions` — codex's system-level instructions,
  so nothing has to be prepended to the first message; sessionID is "" while the thread is being created),
  `SessionEnv` (one app-server serves every session, so this is the thread's `shell_environment_policy.set`).
- `StrictMCP`: every server in `~/.codex/config.toml` is turned off for the thread; session/new's `mcpServers` are
  added either way (stdio and streamable HTTP; codex has no legacy SSE).
- `Network`: network access in the workspace-write sandbox. `NoApprovals`: approval policy "never" (what the
  sandbox doesn't allow fails instead of asking). Mode `auto` + both = workspace-write, network on, no questions.
- `codex.Check(ctx, opts) (Status, error)`: `*sysenv.NotFoundError`, `codex.ErrNotLoggedIn`.

## session/new `_meta`

The whole `_meta` reaches the per-session hooks (`AppendPrompt`, `SessionEnv`, native's `SystemPrompt` / `BashEnv` /
tools). gerdr's own keys live under `_meta.gerdr`:

| Key | native | claude | codex |
|---|---|---|---|
| `mode` | default · acceptEdits · plan · bypassPermissions | default · acceptEdits · plan · auto · bypassPermissions | ask · auto · read-only · full-access |
| `model` | `provider/model` | claude model name / alias | codex model id |
| `effort` | (config option only) | yes | (config option only) |
| `fork` | – | from `RewindResponse.Fork`, on resume / load after a rewind | – |

Modes and the `model` / `effort` config options can also be changed later with session/set_mode and
session/set_config_option.

## What differs between the engines

Hide the buttons an engine doesn't support:

| | native | claude | codex |
|---|---|---|---|
| `_session/steering` (interject) | yes; `now` cuts the generation | yes | yes (`now` ignored) |
| `_gerdr/withdraw` (take back a queued interjection) | yes | yes | **no** |
| `_gerdr/rewind` | yes, same session id | yes, **forks a new session id** (+ `fork` to keep) | yes, same id |
| `/clear` (prompt text) | yes (new id via `sessionId` + `cleared`) | yes (same) | yes (same) |
| `/compact` | yes | yes | yes |
| background tasks list (`tasks` in session_info_update) | yes | yes | sub-agents only |
| `_gerdr/task_stop` | yes | yes | **no** |
| `_gerdr/task_background` (move to background) | yes | yes | **no** |
| `_gerdr/ask` (agent asks the user) | ask_user tool | AskUserQuestion | request_user_input |
| `_gerdr/usage` (account quota) | **no** | yes | yes |
| `_gerdr/complete` (one-off question) | **no** (use `native.Complete`) | yes (haiku) | yes |
| token progress / context size | yes | yes | yes |
| PromptResponse.Usage | yes | no | no |
| Go-implemented tools | yes (`Options.Tools`) | no (use an MCP server) | no (use an MCP server) |

A method an engine doesn't handle answers JSON-RPC "method not found".
