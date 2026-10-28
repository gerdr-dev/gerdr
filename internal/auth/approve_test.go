package auth

import (
	"testing"
	"time"
)

// 本机批准 = 给那台设备一次 step-up；只认发起的那台，过期 / 拒绝之后作废。
func TestApprovalGrantsStepUp(t *testing.T) {
	s := newStore(t, Config{})
	code, _ := s.MintCode()
	dev, token, _ := s.Redeem(code, "Android · Edge", "")
	id := s.Authenticate(withCookie(token))
	if !StepUpNeeded(id, 5*time.Minute, 1, time.Now()) {
		t.Fatal("刚配对的设备在已有 passkey 时该要 step-up")
	}
	a := NewApprovals()
	r := a.Request(dev.ID, dev.Label)
	if a.State(r.ID, "别的设备") != "expired" {
		t.Fatal("别的设备不该查得到这个请求")
	}
	if a.State(r.ID, dev.ID) != "pending" || len(a.Pending()) != 1 {
		t.Fatal("请求该挂着等批准")
	}
	got, ok := a.Decide(r.ID, true)
	if !ok || got != dev.ID || !s.GrantStepUp(got) {
		t.Fatal("批准没生效")
	}
	if a.State(r.ID, dev.ID) != "approved" {
		t.Fatal("发起的设备该看到已批准")
	}
	if _, ok := a.Decide(r.ID, true); ok {
		t.Fatal("批过的不该能再批")
	}
	id = s.Authenticate(withCookie(token))
	if StepUpNeeded(id, 5*time.Minute, 1, time.Now()) {
		t.Fatal("批准之后该能加 passkey")
	}
	// 拒绝 / 过期
	r2 := a.Request(dev.ID, dev.Label)
	a.Decide(r2.ID, false)
	if a.State(r2.ID, dev.ID) != "expired" {
		t.Fatal("拒绝之后该作废")
	}
	r3 := a.Request(dev.ID, dev.Label)
	a.now = func() time.Time { return time.Now().Add(ApprovalTTL + time.Second) }
	if _, ok := a.Decide(r3.ID, true); ok {
		t.Fatal("过期的请求不该能批")
	}
}
