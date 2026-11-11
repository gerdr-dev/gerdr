package acpx

import (
	"encoding/json"
	"sync"
	"time"
)

// TokenMeter 数这一轮输出了多少 token（界面上「↓ 2.2k tokens」，照 Claude Code 的状态行）。
// 一次 API 请求结束时 agent 报准数，请求进行中只有流式片段 —— 按字数估（约 4 个字符一个 token），
// 准数一到就用准数。按请求（id）分开记，总数 = 每个请求取「准数，没有就估的」相加。
type TokenMeter struct {
	mu    sync.Mutex
	exact map[string]int
	chars map[string]int
	last  time.Time
}

func (m *TokenMeter) Reset() {
	m.mu.Lock()
	m.exact, m.chars = map[string]int{}, map[string]int{}
	m.last = time.Time{}
	m.mu.Unlock()
}

func (m *TokenMeter) Chars(id string, n int) {
	m.mu.Lock()
	if m.chars == nil {
		m.chars = map[string]int{}
	}
	m.chars[id] += n
	m.mu.Unlock()
}

func (m *TokenMeter) Exact(id string, n int) {
	m.mu.Lock()
	if m.exact == nil {
		m.exact = map[string]int{}
	}
	m.exact[id] = n
	m.mu.Unlock()
}

func (m *TokenMeter) Total() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := 0
	for id, c := range m.chars {
		if _, ok := m.exact[id]; !ok {
			t += (c + 3) / 4
		}
	}
	for _, n := range m.exact {
		t += n
	}
	return t
}

// Due：离上次推给界面过了 400ms 没有（流式片段很碎，别每个都推）。
func (m *TokenMeter) Due() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if time.Since(m.last) < 400*time.Millisecond {
		return false
	}
	m.last = time.Now()
	return true
}

// CostKey: session_info_update._meta.gerdr.cost = {"usd": n}, what the agent's last turn cost (Claude Code: the
// growth of result.total_cost_usd, a running total per session that a --resume'd process carries on from). A fact for the turn: the hub sums the reports
// that arrive during a turn onto its turn_end (costUsd), and add-ons add those up however they like (g.turns).
const CostKey = "cost"

// Cost: the meta of a cost report.
func Cost(usd float64) map[string]any {
	return Meta(map[string]any{CostKey: map[string]any{"usd": usd}})
}

// TokensKey: session_info_update._meta.gerdr.tokens = Tokens, one model call's usage (Gerdr Agent sends one per
// request, sub-agents and compaction included). Tokens, not money: what a token costs is the add-on's call (the
// `today` add-on prices them from its own table), so a price change or a gateway's own rates never need a release.
// The hub sums the reports of a turn per model onto its turn record (TurnRecord.Tokens).
const TokensKey = "tokens"

// Tokens is one model call's usage. Input counts every prompt token, the cached ones included: uncached input is
// Input - CacheRead - CacheWrite.
type Tokens struct {
	Model      string `json:"model"` // the agent's model key (Gerdr Agent: "provider/model")
	Input      int    `json:"input,omitempty"`
	Output     int    `json:"output,omitempty"`
	CacheRead  int    `json:"cacheRead,omitempty"`
	CacheWrite int    `json:"cacheWrite,omitempty"`
}

// TokensReport: the meta of a token report.
func TokensReport(t Tokens) map[string]any {
	return Meta(map[string]any{TokensKey: t})
}

// ProviderUsageKey: session_info_update._meta.gerdr.providerUsage = one model call's usage object, verbatim as the
// provider sent it (Gerdr Agent sends one per request that had one, the moment it finishes). Tokens above are what
// gerdr reads out of it; this is for the host, so a field gerdr doesn't know (a gateway's own usage.cost, say) still
// gets to it without a gerdr change. What it means, and its unit, is between the host and its provider. chatlog logs
// each report as a usage event.
//
// ProviderUsageAgentKey names the agent that made the call: "" (absent) = the main agent, otherwise the sub-agent's
// tool call id.
const (
	ProviderUsageKey      = "providerUsage"
	ProviderUsageAgentKey = "providerUsageAgent"
)

// ProviderUsageReport: the meta of a provider usage report; agent as in ProviderUsageAgentKey.
func ProviderUsageReport(raw json.RawMessage, agent string) map[string]any {
	m := map[string]any{ProviderUsageKey: raw}
	if agent != "" {
		m[ProviderUsageAgentKey] = agent
	}
	return Meta(m)
}

// ProgressKey：session_info_update._meta.gerdr.progress = {"outputTokens": n}
const ProgressKey = "progress"

func Progress(outputTokens int) map[string]any {
	return Meta(map[string]any{ProgressKey: map[string]any{"outputTokens": outputTokens}})
}

// ContextProgress：上下文用量（这次请求的输入有多大、模型窗口多大）。window 不知道就给 0。
func ContextProgress(tokens, window int) map[string]any {
	m := map[string]any{"contextTokens": tokens}
	if window > 0 {
		m["contextWindow"] = window
	}
	return Meta(map[string]any{ProgressKey: m})
}
