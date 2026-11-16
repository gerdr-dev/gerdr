# TODO

Work that was looked into, measured and put off. Each entry says why it's worth doing and how, so it can be picked up
without redoing the investigation.

## The background service spends its CPU in garbage collection

**Why**: found 2026-11-04 while chasing a ~2s Esc (stop and take the words back) on the user's machine. The installed
`gerdr --service` sat at 150–210% CPU with nothing visibly running; a 3s `sample` resolved through the binary's pclntab
(`go tool addr2line` works on the stripped release build) was almost all `runtime.scanObject*` / `tryDeferToSpanScan`,
i.e. GC. Something allocates heavily and steadily. That slows everything on the machine, and is the likeliest cause of
the slow Esc: the same session copied into a debug instance stopped and gave the words back in ~270ms end to end, and
the service's own log showed that Esc under 1s server-side.

**Done** (pprof on the running service, 2026-11-04): it allocated ~14 MB/s with a ~85 MB heap, GC every ~0.5 s.
Fixed: optCache decoded options.json for every session in every Summary (c683f66); panel functions were bundled and
compiled on every run, ~1 run/s from pages polling (fe8b724); every Summary carried the agent's mode / option lists,
920 KB of the 1.07 MB /api/hello (3803781).

Second round, measured on the fixed build (131 MB / 30 s, down from 524): UI source scan every 250 ms → 1 s when
quiet (31c9c2e); WebSocket messages under 4 KB no longer compressed (deflate writers, aaec506); option values map
reused (163e358); `gerdr session` asks /api/hello?only=sessions,layout (a217328); panels got core usePanelVisible
(d7bb0f2) and the Superagent panel polls only while on screen (its repo, c9c2d29).

**Left**: the lb project's `chat-intake` panel (no repo of its own, ~/.gerdr/projects/lb/panels/chat-intake) still
runs intake.list every 5 s whatever is on screen: its ui/src/pages/index.tsx should poll with usePanelVisible like
Superagent's ui/src/lib/visible.ts. Measure again with the steps below.

Panels are pushed to now (4272bd4: core usePanelChange for files their functions change, useSessions for live
conversations; the Superagent board refreshes on those, e464d59). Not done: session state changes as events
(source "gerdr", kind like session.status) so a panel's reconciling can run from a trigger instead of from its page
being open; the Superagent board still reconciles inside tasks.list, run when its page sees a worker change.

Function VMs are pooled now (internal/work/pool.go: globals installed once, the run's fields reset, the shared JS
world restored from a descriptor snapshot on release, untrusted VMs dropped; pool_test.go). A run went from 4.9 MB /
5.4 ms to 0.6 MB / 2.0 ms, nearly all of it the restore reading each of ~700 property descriptors. Cheaper is possible
(compare values only and hook defineProperty / freeze / seal to learn of attribute changes), but a Proxy over a
builtin reaches its [[DefineOwnProperty]] past any hook, so it was left at the full compare.
How to read it: `curl -H "Authorization: Bearer $(cat ~/.gerdr/token)" -o a.pb.gz
http://127.0.0.1:7870/api/debug/pprof/allocs`, then `go tool pprof -top -cum -sample_index=alloc_space a.pb.gz`
(`/profile?seconds=30` for CPU; server/debug.go).

**Next slow Esc**: gerdr.log now has `escape: slow` (server steps, cumulative, over 300ms) and `ui slow what=escape`
(the page's split: keyWait / request / paint / busy, over 500ms) for the same session and time.

## Move the quotas out of the kernel

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

## Work: triggers, background sessions, a watchdog

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

## Drop the heavy DNS SDKs from internal/acme

**Why**: they are a quarter of the backend binary, and every App update downloads the backend (docs/dev/UPDATE.md).
Measured on 2026-10-30 (`-s -w`, darwin/arm64):

| build | raw | gzip |
|---|---|---|
| all six providers | 37.7MB | 13.2MB |
| no provider SDKs | 28.0MB | 10.4MB |

Per provider: huaweicloud +1.5MB (also pulls in mongo-driver's bson), alidns +1.0MB, route53 +0.5MB (plus the
aws-sdk auth stack); cloudflare, digitalocean and tencentcloud cost ~0 (lego already talks plain HTTP to them).
Expected result: backend ~28MB, update package ~7.5MB brotli instead of ~9.7MB.

**How**: an ACME DNS-01 provider only creates one TXT record and deletes it (lego's `challenge.Provider`:
`Present` / `CleanUp`). Write those three over `net/http` with their request signing:

- alidns: RPC-style query string signed with HMAC-SHA1 (~80 lines).
- route53: AWS SigV4, XML body, ChangeResourceRecordSets + GetChange (~150 lines).
- huaweicloud: SDK-HMAC-SHA256 (APIG AK/SK) signing, find the zone first (~120 lines).

Keep lego for the ACME client itself (~3.5MB) and for the three cheap providers.

**Verifying without cloud credentials** (none were available): differential tests. Import the current SDK-based lego
providers only from `_test.go` (test-only imports don't reach the binary), point both them and the hand-written
ones at an `httptest` server, run Present / CleanUp, and compare method, path, query, body and signature headers.
Where the SDK signs with a random nonce / timestamp, take them from its request and feed them to ours: the
signatures must match byte for byte. Add the official signature examples too (AWS's SigV4 test suite, the examples
in Aliyun's and Huawei's signing docs). What this can't catch: the provider changing its API (the SDK would break
the same way). A real run against Let's Encrypt staging with real credentials is still the final check, whenever
someone has them.

After this, check what else grew the binary from 22MB (2026-10-28) to 37.7MB besides these SDKs.

## web_search tool for the native agent

**Why**: native has `web_fetch` (agents/native/webfetch.go) but no search, so the model has to guess URLs or fetch a
search engine's results page by hand. Claude Code and Codex both ship a search tool. Looked into on 2026-11-01.

**How Claude Code does it**: it doesn't search itself. When the main model calls `WebSearch(query, allowed_domains?,
blocked_domains?)`, Claude Code makes a *separate* Messages request whose only tool is Anthropic's server-side
`{"type": "web_search_20250305", "name": "web_search", "max_uses": N}` (domain filters go on that tool), with the
query as the user message. The API runs the search and returns `server_tool_use` + `web_search_tool_result` blocks
(title, url, encrypted page content) plus cited text; Claude Code turns those into a title + URL list as the
tool_result, and the model uses WebFetch for detail. Costs ~$10 / 1000 searches plus tokens; only first-party
Anthropic (and some Vertex models). Whether a gateway passes the server tool through has to be tested per gateway.
OpenAI Responses has the same idea (`{"type": "web_search"}`, `web_search_call` output items, `url_citation`
annotations; sources need `include: ["web_search_call.action.sources"]`). Chat Completions has nothing standard.

**Plan** (server-side first, local fallback):

- A: a one-off non-streaming call in `agents/native/llm/` (e.g. `llm.WebSearch(ctx, api, ep, model, query, opts)`)
  that sends only the server search tool and parses the result blocks. Keep it out of `Stream`: its block parsing and
  history replay (`Message.Blocks`) shouldn't have to learn server-tool blocks. Use the session's current model
  (`r.model`, its `client()` endpoint), or a cheaper model of the same provider.
- B, when A isn't available (Chat protocol, gateway 400s, no results): scrape `https://html.duckduckgo.com/html/?q=`
  (works without a key; result links are `duckduckgo.com/l/?uddg=<real url>` redirects to unwrap). Grey area and
  may get rate-limited / captcha'd; optionally let the user configure Brave / Tavily / SearXNG instead.
- Register it in `builtinTools` (agents/native/session.go). Not read-only (the query can carry data out): approve it
  next to `web_fetch` in `decide` (agents/native/permission.go), e.g. one `search` rule for "don't ask again".
- Probe A once per provider and cache the answer, so a gateway without the server tool doesn't cost a failed call
  every search.

## The UI's per-device settings in the backend, not only in localStorage

The UI keeps its per-device settings (language, sizes, labs switches…; ui/src/lib/store/prefs.ts) in the page's
localStorage, which is per origin and per browser profile. So they don't follow a change of either: a reinstalled browser profile, the same Gerdr opened in a normal browser or another App
(moving from the Electron App to the Chrome shell lost them once). Worth doing: keep them in the backend as well, keyed by a
device id the page keeps in localStorage (and the App could pass in), with localStorage as the fast, offline copy the
page reads first and the backend's as what a fresh origin / profile starts from. To decide first: which settings are
really per device (window sizes, zoom) and which are per person (language, labs switches: one copy for all devices
would do), and what a remote device (phone, another computer through the relay) gets.

## Tab switches: what's left

Reported: switching tabs felt slow on another person's computer. Measured 2026-11-16 in the debug instance with
`tools/scrolltest/gen.mjs` (synthetic sessions: many images, many videos, long text with tables and tool output) plus
two real 21MB sessions, 5 tabs, keepTabs 6; `tabswitch.mjs` times a switch to the first painted frame of the new chat,
`tabrenders.mjs` counts component renders per switch, `tabprofile.mjs [--first]` writes a CPU profile.

Done, median ms to the new chat painted, M3 with the CPU 4x slowed:

| | before | after |
|---|---|---|
| switch to a kept tab | 330–450 (development React) | 37–52 |
| a tab's first visit in the page | ~800 (development React) | 350–430 (production React; not worked on yet) |

1. App's children are memoised, the rail's project chips are the only part of it reading the layout (`RailProjects`),
   sidebar rows get no per-render callbacks or active tab id (`Row` in UnifiedSidebar.tsx): component renders per
   switch 2221 → 266.
2. Production React is the default (internal/uisite/prod.go; Settings → Labs turns it off per device).

Left:
- A tab's first visit in the page (or a switch to a tab past keepTabs) mounts cold: 100-170ms on the M3, 350-430ms
  with the CPU 4x slowed (up to ~250 / 600 for a 21MB log, whose catch-up alone takes ~230ms at 4x). Nearly all of it
  is the main thread busy, not waiting: the catch-up arrives 35-65ms after the switch and applying it takes a few ms.
  Busy time by what it was doing (`tabfirst.mjs`, 4x, shares of ~520ms with the profiler on):
  React rendering + committing the chat ~35%, the browser's own style / layout / paint ~24%, streamdown turning the
  Markdown into elements ~20%, ChatList measuring rows (forced style + layout) ~16%, Tailwind's browser build ~6%
  (scanning for class names it hasn't seen on the page and rebuilding its sheet; only new names trigger that, so it
  only shows when a lot of new content mounts at once), applying the catch-up ~1%. Cold opens already mount only the
  rows near the bottom (~12 here) before the first paint; every row is heavy though: Markdown blocks, each image with
  its actions popover, each message's action bar with tooltips and a rewind popover (Radix roots mount even closed),
  ~3400 component renders in the 1.5s after a first visit (`tabrenders.mjs --first`), most of them the idle batches
  that measure the rows off screen. Done: popups mount
  their content on first open (`ui/packages/chat/src/ui/opened.ts`: the chat package's Tooltip and DropdownMenu; the
  rewind button's popover mounts on its first click, anchored by virtualRef): component renders in the 1.5s after a
  first visit 3423 -> 2842; first-visit time 5-30% lower per tab in an alternating A/B, within the noise on some (the
  machine had other agents running). Markdown per message is cheap (`mdbench.mjs`, M3, production React: a typical
  880-char reply ~2ms, a 25-row table ~3ms, our plugins +0.3-0.7ms): its share is the number and length of the rows
  mounted before the first paint (a real reply often holds a table and a code block). Ideas, not tried: first paint
  with only the rows covering the screen, the overscan a frame later; cache parsed Markdown per part across mounts
  (helps remounts: the virtual list, a tab past keepTabs; not a first visit, which must parse once anyway).
  Ruled out: Tailwind rewriting an identical sheet (it does, up to three times per first visit, but skipping identical
  writes made no difference outside the noise in an alternating A/B: the rewrites mostly land after the first paint);
  mounting likely tabs ahead of time when idle (35-80ms first visits, turned down: don't mount tabs nobody opened).
- Each ChatList render also mounts a few never-measured rows beside the range to measure them (BATCH).
