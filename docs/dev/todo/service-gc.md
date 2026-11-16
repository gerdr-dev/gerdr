# The background service spends its CPU in garbage collection

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
