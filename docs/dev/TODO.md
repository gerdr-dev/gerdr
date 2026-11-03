# TODO

Work that was looked into, measured and put off. Each entry says why it's worth doing and how, so it can be picked up
without redoing the investigation.

## Work: triggers, background sessions, a watchdog

**Why**: discussed on 2026-11-03 against DeepSeek's "Super-agent" write-up (a resident router agent dispatching
isolated workers, driven by events). Reports, tags, `ctx.sessions.close` and `session new --workspace` went in first
(ARCHITECTURE.md, "Coordinating without blocking"); these three wait until the dispatcher has been used for real and
we know which events matter.

- **Triggers**: schedules went in (2026-11-03, `work/schedule.go`, Annulo's format: `schedules/<id>.json` with fn /
  prompt, every / at). Still open: events (a session's status change, a POST to `/api/work/<project>/hook/<name>`),
  and Annulo's jitter / due / params / user overrides, plus a Settings page listing them. The function does the cheap deterministic filtering (fast path) and only then
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
