package codex

import (
	"encoding/json"
	"testing"
)

// 实测 account/rateLimits/read 回的形状（prolite 账号只有一个周窗口）
func TestConvertRateLimits(t *testing.T) {
	raw := `{"rateLimits":{"limitId":"codex","primary":{"usedPercent":1,"windowDurationMins":10080,"resetsAt":1793774649},"secondary":null,"planType":"prolite"},
	"rateLimitsByLimitId":{"codex":{"limitId":"codex","primary":{"usedPercent":1,"windowDurationMins":10080,"resetsAt":1793774649},"secondary":{"usedPercent":40,"windowDurationMins":300,"resetsAt":1793700000},"planType":"prolite"}}}`
	var res rateLimitsResp
	if err := json.Unmarshal([]byte(raw), &res); err != nil {
		t.Fatal(err)
	}
	u := convertRateLimits(res)
	if u.Plan != "prolite" || len(u.Limits) != 2 {
		t.Fatalf("got %+v", u)
	}
	if l := u.Limits[0]; l.Label != "本周" || l.Percent != 1 || l.ResetsAt != 1793774649000 {
		t.Fatalf("primary %+v", l)
	}
	if l := u.Limits[1]; l.Label != "5 小时" || l.Percent != 40 {
		t.Fatalf("secondary %+v", l)
	}
}
