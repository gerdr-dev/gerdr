package hub

import (
	"testing"
	"time"
)

func TestStats(t *testing.T) {
	dir := t.TempDir()
	h := &Hub{stats: newStats(dir), subs: map[*Sub]struct{}{}}
	h.countDone("end_turn")
	h.countDone("cancelled")
	h.countDone("error")
	h.countDone("end_turn")
	h.addCost("s1", "claude", 1.5)
	h.addCost("s1", "claude", 2.0)  // +0.5
	h.addCost("s1", "claude", 0.25) // a new process: counted from zero
	h.addCost("s2", "claude", 1)
	days := h.Stats(7)
	if len(days) != 7 || days[6].Date != time.Now().Format("2006-01-02") {
		t.Fatalf("days = %+v", days)
	}
	d := days[6]
	if d.Done != 2 || d.Cost["claude"] != 3.25 {
		t.Fatalf("today = %+v", d)
	}
	h.stats.save()
	if got := newStats(dir).data.Days[d.Date]; got == nil || got.Done != 2 {
		t.Fatalf("not saved: %+v", got)
	}
}
