// Package native is gerdr's own coding agent: it calls model APIs directly (Chat Completions, Responses,
// Anthropic Messages; see llm/), and the loop, tools and transcripts all live in this process. Outward it is a
// complete ACP agent (acp.Agent from github.com/coder/acp-go-sdk): gerdr connects it to its hub through an
// in-process pipe, `gerdr acp native` serves it on stdio, and other programs can embed it the same way (see
// Example). Everything gerdr-specific is injected through Options; this package imports none of gerdr's
// internal packages.
//
// Why not shuttle's pi: interjections, sub-agents and rewind are missing from or the wrong shape in pi's loop;
// this loop is about a hundred lines (loop.go) and every step is visible.
//
// # Protocol
//
// Standard ACP: initialize, session/new | load | resume | list | close, session/prompt, session/cancel,
// session/set_mode, session/set_config_option. Updates arrive as session/update notifications: message and
// thought chunks, tool calls and their updates (with diffs for edits), plan (todo_write), available commands.
//
//   - Modes (session/set_mode, or _meta.gerdr.mode on session/new | load | resume): default (ask before edits and
//     commands; read-only commands never ask), acceptEdits, plan (read-only), bypassPermissions.
//   - Config options (session/set_config_option): "model" = "provider/model" from Config (or
//     _meta.gerdr.model on session/new); "effort" = one of the model's levels, or "off" on any model that can
//     turn thinking off. Changing the model resets the effort to the model's default.
//   - session/prompt: give every user message a messageId (rewind finds messages by it). A prompt that is
//     exactly "/clear" starts a fresh context (below); "/compact [instructions]" summarises the history now;
//     "/memory [request]" works on long-term memory; "/<skill> [args]" runs a skill. PromptResponse.Usage is
//     the turn's token usage (all model calls, compaction and sub-agents included).
//   - _meta on session/new (and load / resume) is kept per session and handed to Options.SystemPrompt,
//     Options.BashEnv and caller tools (ToolCall.Meta). Its "gerdr" key holds gerdr's own settings (mode, model).
//   - mcpServers on session/new (and load / resume) are connected next to Options.MCP's.
//
// # Extension methods (package github.com/gerdr-dev/gerdr/acpx has the request / response types)
//
// Client → agent:
//
//   - _session/steering (acpx.SteerRequest → acpx.SteerResponse): interject while a turn runs. The message is
//     queued and joins the history before the next model call (a running tool isn't interrupted); Now: true
//     also cuts the current generation short. Outcome "injected", or "promptRequired" when no turn is running
//     (send it as session/prompt instead).
//   - _gerdr/withdraw (acpx.WithdrawRequest → acpx.WithdrawResponse): take back queued interjections by
//     messageId; returns the ones that hadn't been picked up yet.
//   - _gerdr/rewind (acpx.RewindRequest → acpx.RewindResponse): drop the user message with that messageId and
//     everything after it (compactions included). Not while a turn runs. The session id stays the same.
//   - _gerdr/task_stop (acpx.TaskStopRequest → {}): stop a background task (bash or sub-agent) by its id.
//   - _gerdr/task_background (acpx.TaskBackgroundRequest → acpx.TaskBackgroundResponse): move a foreground bash
//     command or sub-agent (by tool call id) to the background; the turn moves on.
//
// Agent → client:
//
//   - _gerdr/ask (acpx.AskRequest → acpx.AskResponse): the ask_user tool's multiple-choice questions. Answer
//     with answers keyed by question id, or cancelled (+ reason / text).
//   - session/request_permission: standard ACP; options allow_once, allow_always (remembered for the session:
//     "edit", "bash:<prefix>", "fetch:<host>", "tool:<name>"), reject_once.
//
// # session_info_update _meta.gerdr
//
//   - progress: {outputTokens} while generating (this turn's running total); {contextTokens, contextWindow}
//     after each model call (how full the context is).
//   - tasks: []acpx.Task, the whole list of sub-agents and background commands, replaced each time.
//   - autoTurn: true / false around a turn the agent started by itself (a background task finished while the
//     session was idle). Its updates arrive without a session/prompt.
//   - sessionId + cleared: true, sent on the old id after "/clear": the session continues under the new id
//     (the old one stays an alias).
//   - notice (+ noticeLevel "retry" and noticeDelayMs while retrying a model error): a line for the user.
//
// Sub-agents: their tool calls carry _meta.gerdr.parentToolCallId (the task tool call), their message / thought
// chunks _meta.gerdr.subagent, and the task they were given arrives as a user_message_chunk with
// _meta.gerdr.subagent.
//
// Available commands: skills are listed with _meta.skill = true.
package native
