package codex

import (
	"context"
	"fmt"
	"sort"

	"github.com/gerdr-dev/gerdr/internal/acpx"
)

// 只声明读到的字段（完整的见 generate-ts 出来的 v2/GetAccountRateLimitsResponse.ts）。
type rateLimitWindow struct {
	UsedPercent        float64 `json:"usedPercent"`
	WindowDurationMins *int    `json:"windowDurationMins"`
	ResetsAt           *int64  `json:"resetsAt"` // unix 秒
}

type rateLimitSnapshot struct {
	LimitID   *string          `json:"limitId"`
	LimitName *string          `json:"limitName"`
	Primary   *rateLimitWindow `json:"primary"`
	Secondary *rateLimitWindow `json:"secondary"`
	PlanType  *string          `json:"planType"`
}

type rateLimitsResp struct {
	RateLimits          rateLimitSnapshot             `json:"rateLimits"`
	RateLimitsByLimitID map[string]*rateLimitSnapshot `json:"rateLimitsByLimitId"`
}

// usage：account/rateLimits/read（codex 的 /status 用的就是它）。app-server 本来就常驻，问一次就是一个 RPC。
// excludeResetCreditDetails：后台轮询用，省掉「重置券」明细那一次额外的查询。
func (a *Agent) usage(ctx context.Context) (acpx.UsageResponse, error) {
	rpc, err := a.client(ctx)
	if err != nil {
		return acpx.UsageResponse{}, err
	}
	var res rateLimitsResp
	if err := rpc.Call(ctx, "account/rateLimits/read", map[string]any{"excludeResetCreditDetails": true}, &res); err != nil {
		return acpx.UsageResponse{}, err
	}
	return convertRateLimits(res), nil
}

func convertRateLimits(res rateLimitsResp) acpx.UsageResponse {
	out := acpx.UsageResponse{Limits: []acpx.UsageLimit{}}
	snaps := []*rateLimitSnapshot{&res.RateLimits}
	if len(res.RateLimitsByLimitID) > 0 {
		// 多桶的视图（按 limitId，比如 codex）：有它就用它，单桶那份只是它的兼容写法
		ids := make([]string, 0, len(res.RateLimitsByLimitID))
		for id := range res.RateLimitsByLimitID {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		snaps = snaps[:0]
		for _, id := range ids {
			if s := res.RateLimitsByLimitID[id]; s != nil {
				snaps = append(snaps, s)
			}
		}
	}
	for _, s := range snaps {
		if out.Plan == "" && s.PlanType != nil {
			out.Plan = *s.PlanType
		}
		// 不止一个桶时标上是哪个桶的（只有默认的 codex 那一个就不标）
		prefix := ""
		if s.LimitName != nil && *s.LimitName != "" {
			prefix = *s.LimitName + " · "
		} else if s.LimitID != nil && *s.LimitID != "" && *s.LimitID != "codex" {
			prefix = *s.LimitID + " · "
		}
		for i, w := range []*rateLimitWindow{s.Primary, s.Secondary} {
			if w == nil {
				continue
			}
			l := acpx.UsageLimit{Kind: []string{"primary", "secondary"}[i], Percent: w.UsedPercent}
			if w.WindowDurationMins != nil {
				l.WindowMins = *w.WindowDurationMins
			}
			if w.ResetsAt != nil {
				l.ResetsAt = *w.ResetsAt * 1000
			}
			l.Label = prefix + windowLabel(l.WindowMins)
			out.Limits = append(out.Limits, l)
		}
	}
	return out
}

// 300 →「5 小时」，10080 →「本周」
func windowLabel(mins int) string {
	switch {
	case mins <= 0:
		return "额度"
	case mins == 7*24*60:
		return "本周"
	case mins%(24*60) == 0:
		return fmt.Sprintf("%d 天", mins/(24*60))
	case mins%60 == 0:
		return fmt.Sprintf("%d 小时", mins/60)
	}
	return fmt.Sprintf("%d 分钟", mins)
}
