package hub

import (
	"testing"

	"github.com/coder/acp-go-sdk"

	"github.com/gerdr-dev/gerdr/acpx"
)

// TestRetryNoticeUntil: an adapter's retry notice carries its wait as noticeDelayMs; the hub turns it into a deadline
// so the UI can count down (claude CLI's own API retries used to sit at "9 秒后" until the retry fired).
func TestRetryNoticeUntil(t *testing.T) {
	h, err := New(Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	s := newSession(h, Record{ID: "s1"})
	info := func(g map[string]any) {
		s.onUpdate(acp.SessionUpdate{SessionInfoUpdate: &acp.SessionSessionInfoUpdate{Meta: acpx.Meta(g)}})
	}
	before := now()
	info(map[string]any{"notice": "连不上 API，9 秒后自动重试（第 1/9 次）", "noticeLevel": "retry", "noticeDelayMs": float64(9000)})
	info(map[string]any{"notice": "plain warning"})
	if len(s.events) != 2 {
		t.Fatalf("events: %d", len(s.events))
	}
	if e := s.events[0]; e.Level != "retry" || e.Until < before+9000 || e.Until > now()+9000 {
		t.Fatalf("retry notice: %+v", e)
	}
	if e := s.events[1]; e.Level != "warn" || e.Until != 0 {
		t.Fatalf("plain notice: %+v", e)
	}
}
