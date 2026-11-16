# Work: triggers, background sessions, a watchdog

**Why**: discussed on 2026-11-03 against DeepSeek's "Super-agent" write-up (a resident router agent dispatching
isolated workers, driven by events). Reports, tags, `ctx.sessions.close` and `session new --workspace` went in first
(ARCHITECTURE.md, "Coordinating without blocking"); these three wait until the dispatcher has been used for real and
we know which events matter.

- **Triggers**: schedules went in (2026-11-03, `work/schedule.go`, a hosted function platform's format: `schedules/<id>.json` with fn /
  prompt, every / at). Still open: events (a session's status change, a POST to `/api/work/<project>/hook/<name>`),
  and that platform's jitter / due / params / user overrides, plus a Settings page listing them. The function does the cheap deterministic filtering (fast path) and only then
  sends something to the Work agent or starts a worker. Long-lived streams (`lark-cli event consume`) already fit an
  add-on service; what's missing there is a supported way for a service to push into a session or a trigger.
- **Background sessions**: `session new --background`: no pane, only in lists / the Work board until it has something.
  Speculative work ("try to reproduce this CI failure, only tell me if you find a fix") would otherwise fill the
  user's workspaces. Needs care: a session no pane shows is shut down by the UI today (keepTabs / close = shutdown).
- **Watchdog**: a scheduled trigger over `session list` (`turnStartedAt`, `updatedAt`, `waitingSince` are there now)
  that flags "running 40 min with no output" or "waiting 10 min" to the Work agent; judging whether a worker drifted
  off its goal stays the Work agent's job.
