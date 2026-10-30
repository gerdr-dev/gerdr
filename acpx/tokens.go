package acpx

import (
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
