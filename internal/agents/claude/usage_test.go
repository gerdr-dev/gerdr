package claude

import "testing"

// 实测 get_usage（skip_behaviors）回的形状，删掉了无关字段
func TestParseUsage(t *testing.T) {
	raw := `{"subscription_type":"team","rate_limits_available":true,"rate_limits":{
	"five_hour":{"utilization":69,"resets_at":"2026-10-28T15:20:00.025330+00:00"},
	"seven_day":{"utilization":29,"resets_at":"2026-11-03T07:00:00.025351+00:00"},
	"limits":[{"kind":"session","percent":69,"resets_at":"2026-10-28T15:20:00.025330+00:00","scope":null},
	{"kind":"weekly_all","percent":29,"resets_at":"2026-11-03T07:00:00.025351+00:00","scope":null},
	{"kind":"weekly_scoped","percent":0,"resets_at":"2026-11-03T07:00:00+00:00","scope":{"model":{"id":null,"display_name":"Fable"},"surface":null}}]}}`
	u, err := parseUsage([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if u.Plan != "team" || len(u.Limits) != 3 {
		t.Fatalf("got %+v", u)
	}
	want := []string{"5 小时", "本周", "本周 · Fable"}
	for i, l := range u.Limits {
		if l.Label != want[i] {
			t.Errorf("limit %d label %q, want %q", i, l.Label, want[i])
		}
	}
	if u.Limits[0].ResetsAt != 1793200800025 {
		t.Errorf("resetsAt %d", u.Limits[0].ResetsAt)
	}

	// API key：没有套餐额度
	u, err = parseUsage([]byte(`{"subscription_type":null,"rate_limits_available":false,"rate_limits":null}`))
	if err != nil || len(u.Limits) != 0 {
		t.Fatalf("api key: %+v %v", u, err)
	}
}
