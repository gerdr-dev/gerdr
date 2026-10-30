# Chat event log

The format `@gerdr/chat` renders and `chatlog` (Go) writes: a list of events per session, JSONL on disk, one JSON
object per event. The UI folds the whole list into the conversation with pure functions (`buildTranscript`), so
the log is the single source of truth. Change both sides together: Go `chatlog.Event` (event.go) and TypeScript
`HubEvent` (`ui/packages/chat/src/types.ts`).

Every event has `seq` (increasing; ordering) and `ts` (unix ms) and a `kind`:

| kind | fields | meaning |
|---|---|---|
| `user` | `messageId`, `content` (ACP ContentBlock[]), `steer`, `from` | A user message. `steer`: sent as an interjection into a running turn. `from`: another session that sent it (gerdr's `gerdr session send`). |
| `turn_start` | `messageId` (the user message that started it), `auto` | A turn began. `auto`: the agent started it by itself (a background task finished), no user message before it. |
| `turn_end` | `messageId`, `stopReason` (end_turn · cancelled · error · max_tokens · refusal…), `error`, `durationMs`, `outputTokens`, `auto` | The turn ended. |
| `update` | `update`: an ACP `SessionUpdate`, verbatim | Message / thought chunks, tool calls and updates, plans, user_message_chunk (replayed history). Consecutive text chunks of one message are merged into one event when written. Not logged: available_commands_update, config_option_update, current_mode_update, usage_update, session_info_update (those go to the session state, or become the events below). |
| `permission` | `reqId`, `request` (ACP RequestPermissionRequest) | The agent asks for approval. |
| `permission_done` | `reqId`, `response` (`{"optionId": …}` or `{"cancelled": true}`) | Answered, or void (the turn ended). |
| `ask` | `reqId`, `request` (acpx.AskRequest) | The agent asks the user questions. |
| `ask_done` | `reqId`, `response` (acpx.AskResponse) | Answered, cancelled, or void. |
| `steer_state` | `messageId`, `text`: `queued` · `started` | Where an interjection is in the agent's queue (claude reports it; "queued" ones can be withdrawn). |
| `notice` | `level` (info · warn · error · retry · clear), `text`, `until` (retry: unix ms when it fires) | A line in the conversation. `retry` collapses once the agent writes again; `clear` marks a /clear (rewinding past it isn't possible). |
| `task` | `task` (acpx.Task) | A background task or sub-agent finished (the tasks bar shows running ones from the session state). |
| `shell` · `shell_out` · `shell_done` | `messageId` (run id), `text` (command / output), `exit`, `level` (stopped · lost) | gerdr only: a `!command` the user ran from the composer. |

Rewriting: rewind, withdraw and stop can remove events. `chatlog.Session` then hands the whole list to `OnEvents`
with `replace = true`; the UI replaces what it had.

Session state (not in the log; `chatlog.State`, pushed through `OnState`): `sessionId`, `status` (idle · running ·
waiting · error), `error`, `tasks`, `commands`, `modes`, `configOptions`, `ctxTokens`, `ctxWindow`,
`turnStarted`, `turnTokens`. Open requests: `Session.Pending()`.
