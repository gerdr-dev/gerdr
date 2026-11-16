# Move the quotas out of the kernel

**Why**: decided 2026-11-04 against the kernel's boundary (ARCHITECTURE.md, "The kernel's boundary"): quotas are a
reading of third-party accounts, polled for display, and none of the kernel's four jobs. Today the hub asks each
adapter (`_gerdr/usage`: claude's stream-json `get_usage` control request, borrowing a live session process or starting
a throwaway one; codex's `account/rateLimits/read`), caches 2 minutes, and serves `/api/usage` / `g.usage()`.

**How**: the `usage-meter` add-on (gerdr-addon repo, the only known user of `g.usage`) gets a service that asks the CLIs
itself: a throwaway `claude` (stream-json, initialize, `get_usage` with `skip_behaviors`, close; agents/claude/usage.go
has the exact request) and `codex app-server` (`account/rateLimits/read`, agents/codex). It caches and sends the result
to its UI half. Then remove `hub/usage.go`, `/api/usage`, `acpx.MethodUsage` in both adapters, and `g.usage` from
gerdr.d.ts.

**Open**:
- The composer's `/usage` (UsagePanel, `localCommands` in lib/chatHost.tsx) reads the same data. Either it goes with
  the add-on (add-ons can't add composer slash commands yet: that would be a new general hook) or `/usage` goes back to
  the agent (claude prints its own text; codex's app-server has no such command).
- `gerdr doctor` checks quotas (`usageChecks`): drop that check or let it ask the add-on.
- Lost: borrowing a live claude process. Every ask starts one (1–2 s), so the add-on must cache like the hub did.
- `g.usage` going away breaks add-ons that call it: keep it a few versions as a shim that says it's gone.
