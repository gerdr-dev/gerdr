# web_search tool for the native agent

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
