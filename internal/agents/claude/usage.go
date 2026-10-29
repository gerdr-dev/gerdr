package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/gerdr-dev/gerdr/internal/acpx"
)

// usage：账号的用量额度，走 stream-json 的 control_request get_usage（/usage 背后就是它，CLI 里标着 Experimental）。
// 带 skip_behaviors：只要额度，不扫最近 7 天的本地记录（那一段是 /usage 的「花在哪」，慢）。
//
// 进程从哪来：有会话的进程活着就借它问（一个 control_request，不进对话）；都没起就临时起一个，问完就关 ——
// 没说过话的进程不写会话记录，起一次 1～2 秒。临时那个不带 skill / MCP 的参数，免得为了问个数把 MCP 全拉起来。
func (a *Agent) usage(ctx context.Context) (acpx.UsageResponse, error) {
	if p := a.liveProc(); p != nil {
		if raw, err := p.control(ctx, map[string]any{"subtype": "get_usage", "skip_behaviors": true}); err == nil {
			return parseUsage(raw)
		}
		// 借的进程正好在退：落到下面临时起一个
	}
	bin, err := a.bin()
	if err != nil {
		return acpx.UsageResponse{}, err
	}
	o := spawnOpts{bin: bin, cwd: os.TempDir(), env: a.opts.Env, extra: a.opts.Args}
	pr, err := spawn(o, a.log.With("for", "usage"), func(map[string]json.RawMessage, []byte) {})
	if err != nil {
		return acpx.UsageResponse{}, err
	}
	defer func() { go pr.stop() }() // 收尾要等它退（最多几秒），别让问的人跟着等
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := pr.control(cctx, map[string]any{"subtype": "initialize"}); err != nil {
		return acpx.UsageResponse{}, fmt.Errorf("claude initialize: %w", err)
	}
	raw, err := pr.control(cctx, map[string]any{"subtype": "get_usage", "skip_behaviors": true})
	if err != nil {
		return acpx.UsageResponse{}, err
	}
	return parseUsage(raw)
}

// liveProc：随便一个活着、没在退的会话进程。
func (a *Agent) liveProc() *proc {
	a.mu.Lock()
	ss := make([]*session, 0, len(a.sessions))
	for _, s := range a.sessions {
		ss = append(ss, s)
	}
	a.mu.Unlock()
	for _, s := range ss {
		s.mu.Lock()
		p := s.p
		ok := p != nil && s.exiting == nil && p.alive()
		s.mu.Unlock()
		if ok {
			return p
		}
	}
	return nil
}

type cliLimitWindow struct {
	Utilization *float64 `json:"utilization"`
	ResetsAt    *string  `json:"resets_at"`
}

type cliUsage struct {
	SubscriptionType    *string `json:"subscription_type"`
	RateLimitsAvailable bool    `json:"rate_limits_available"`
	RateLimits          *struct {
		FiveHour *cliLimitWindow `json:"five_hour"`
		SevenDay *cliLimitWindow `json:"seven_day"`
		// limits：服务端给的整份清单（会话 / 每周 / 按模型的每周……），有它就以它为准
		Limits []struct {
			Kind     string  `json:"kind"`
			Percent  float64 `json:"percent"`
			ResetsAt *string `json:"resets_at"`
			Scope    *struct {
				Model *struct {
					DisplayName *string `json:"display_name"`
				} `json:"model"`
			} `json:"scope"`
		} `json:"limits"`
	} `json:"rate_limits"`
}

func parseUsage(raw json.RawMessage) (acpx.UsageResponse, error) {
	var u cliUsage
	if err := json.Unmarshal(raw, &u); err != nil {
		return acpx.UsageResponse{}, err
	}
	out := acpx.UsageResponse{Limits: []acpx.UsageLimit{}}
	if u.SubscriptionType != nil {
		out.Plan = *u.SubscriptionType
	}
	rl := u.RateLimits
	if !u.RateLimitsAvailable || rl == nil {
		// API key / Bedrock / Vertex：没有套餐额度，Limits 空着
		return out, nil
	}
	if len(rl.Limits) > 0 {
		for _, l := range rl.Limits {
			label := l.Kind
			window := 0
			switch l.Kind {
			case "session":
				label, window = "5 小时", 300
			case "weekly_all":
				label, window = "本周", 7*24*60
			case "weekly_scoped":
				label, window = "本周", 7*24*60
				if l.Scope != nil && l.Scope.Model != nil && l.Scope.Model.DisplayName != nil {
					label = "本周 · " + *l.Scope.Model.DisplayName
				}
			default:
				label = strings.ReplaceAll(l.Kind, "_", " ")
			}
			out.Limits = append(out.Limits, acpx.UsageLimit{Kind: l.Kind, Label: label, Percent: l.Percent, ResetsAt: isoMs(l.ResetsAt), WindowMins: window})
		}
		return out, nil
	}
	// 老一点的 CLI 没有 limits[]，只有这两个窗口
	add := func(kind, label string, window int, w *cliLimitWindow) {
		if w == nil || w.Utilization == nil {
			return
		}
		out.Limits = append(out.Limits, acpx.UsageLimit{Kind: kind, Label: label, Percent: *w.Utilization, ResetsAt: isoMs(w.ResetsAt), WindowMins: window})
	}
	add("session", "5 小时", 300, rl.FiveHour)
	add("weekly_all", "本周", 7*24*60, rl.SevenDay)
	return out, nil
}

func isoMs(s *string) int64 {
	if s == nil || *s == "" {
		return 0
	}
	t, err := time.Parse(time.RFC3339Nano, *s)
	if err != nil {
		return 0
	}
	return t.UnixMilli()
}
