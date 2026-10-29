package hub

import (
	"context"
	"sync"
	"time"

	"github.com/gerdr-dev/gerdr/internal/acpx"
	"github.com/gerdr-dev/gerdr/internal/agenthost"
	"github.com/gerdr-dev/gerdr/internal/sysenv"
)

// UsageInfo：一个 agent 账号的用量额度（acpx.MethodUsage），界面常驻显示用。
type UsageInfo struct {
	Agent string              `json:"agent"`
	Name  string              `json:"name"`
	Data  *acpx.UsageResponse `json:"data,omitempty"` // 最近一次拿到的；后来出错也留着，界面照样画、标上「旧的」
	Error string              `json:"error,omitempty"`
	At    int64               `json:"at,omitempty"` // Data 是什么时候拿到的（unix ms）
}

// 额度变得慢，界面几分钟问一次；多个窗口 / 设备一起问只真问一次。claude 没有活着的会话进程时要临时起一个
// （1～2 秒、几百 MB），所以缓存不能太短。force（人点了「刷新」）也至少隔 usageMinGap。
const (
	usageTTL    = 2 * time.Minute
	usageMinGap = 10 * time.Second
)

type usageCache struct {
	mu   sync.Mutex
	info map[string]*UsageInfo
	busy map[string]chan struct{}
	last map[string]time.Time // 上一次真去问的时间（成功失败都算）
}

func (h *Hub) usageCache() *usageCache {
	h.usageOnce.Do(func() {
		h.usage = &usageCache{info: map[string]*UsageInfo{}, busy: map[string]chan struct{}{}, last: map[string]time.Time{}}
	})
	return h.usage
}

// Usage：claude、codex 两家（本机装了的）各自的额度。只问内置的这两个：变体多半是同一个账号，换账号的变体以后再说。
func (h *Hub) Usage(ctx context.Context, force bool) []UsageInfo {
	type item struct{ id, name string }
	var ids []item
	if _, err := sysenv.FindClaude(); err == nil {
		ids = append(ids, item{"claude", "Claude Code"})
	}
	if _, err := sysenv.FindCodex(); err == nil {
		ids = append(ids, item{"codex", "Codex"})
	}
	out := make([]UsageInfo, len(ids))
	var wg sync.WaitGroup
	for i, it := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i] = h.usageOf(ctx, it.id, it.name, force)
		}()
	}
	wg.Wait()
	return out
}

func (h *Hub) usageOf(ctx context.Context, agent, name string, force bool) UsageInfo {
	c := h.usageCache()
	for {
		c.mu.Lock()
		cur := c.info[agent]
		fresh := cur != nil && time.Since(c.last[agent]) < usageTTL
		if cur != nil && (fresh && !force || time.Since(c.last[agent]) < usageMinGap) {
			v := *cur
			c.mu.Unlock()
			return v
		}
		if ch := c.busy[agent]; ch != nil {
			// 别人正在问：等它的结果
			c.mu.Unlock()
			select {
			case <-ch:
				continue
			case <-ctx.Done():
				return UsageInfo{Agent: agent, Name: name, Error: ctx.Err().Error()}
			}
		}
		ch := make(chan struct{})
		c.busy[agent] = ch
		c.mu.Unlock()

		// 不跟着请求的 ctx：问到一半页面走了，结果照样存进缓存给下一个人用
		qctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		res, err := h.queryUsage(qctx, agent)
		cancel()

		c.mu.Lock()
		next := UsageInfo{Agent: agent, Name: name}
		if cur != nil {
			next.Data, next.At = cur.Data, cur.At
		}
		if err != nil {
			next.Error = err.Error()
			h.log.Warn("usage failed", "agent", agent, "err", err)
		} else {
			next.Data, next.At = &res, time.Now().UnixMilli()
		}
		c.info[agent] = &next
		c.last[agent] = time.Now()
		delete(c.busy, agent)
		close(ch)
		c.mu.Unlock()
		return next
	}
}

func (h *Hub) queryUsage(ctx context.Context, agent string) (acpx.UsageResponse, error) {
	c, err := h.host(ctx, agent)
	if err != nil {
		return acpx.UsageResponse{}, err
	}
	return agenthost.Call[acpx.UsageResponse](ctx, c, acpx.MethodUsage, map[string]any{})
}
