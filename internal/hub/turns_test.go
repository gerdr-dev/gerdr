package hub

import (
	"log/slog"
	"reflect"
	"testing"
	"time"

	"github.com/gerdr-dev/gerdr/acpx"
)

func TestTurnLog(t *testing.T) {
	l := newTurnLog(t.TempDir())
	oct := time.Date(2026, 10, 31, 23, 0, 0, 0, time.Local).UnixMilli()
	nov := time.Date(2026, 11, 1, 1, 0, 0, 0, time.Local).UnixMilli()
	for _, r := range []TurnRecord{
		{TS: oct, SessionID: "a", StopReason: "end_turn", CostUSD: 1},
		{TS: nov, SessionID: "b", StopReason: "end_turn", Auto: true},
		{TS: nov + 1, SessionID: "c", StopReason: "cancelled"},
	} {
		if err := l.append(r); err != nil {
			t.Fatal(err)
		}
	}
	// Across the month boundary: both files are read
	if got := l.read(oct, nov+2); len(got) != 3 || got[0].SessionID != "a" || got[2].SessionID != "c" {
		t.Fatalf("all = %+v", got)
	}
	// [since, until)
	if got := l.read(nov, nov+1); len(got) != 1 || got[0].SessionID != "b" || !got[0].Auto {
		t.Fatalf("one = %+v", got)
	}
}

// The cost reports of a turn add up onto its turn_end and the turn log; the next turn starts from zero.
func TestTurnCost(t *testing.T) {
	h := &Hub{turns: newTurnLog(t.TempDir()), subs: map[*Sub]struct{}{}, log: slog.Default()}
	s := &Session{h: h, loaded: true, rec: Record{ID: "s1", Agent: "claude", Cwd: "/w"}}
	cost := func(usd float64) { s.onGerdrInfo(map[string]any{"cost": map[string]any{"usd": usd}}) }
	cost(0.5)
	cost(0.25) // a network retry inside the same turn: a second result
	e := &Event{Kind: EvTurnEnd, StopReason: "end_turn", DurationMs: 1000}
	s.emit(e)
	if e.CostUSD != 0.75 {
		t.Fatalf("turn_end cost = %v", e.CostUSD)
	}
	s.emit(&Event{Kind: EvTurnEnd, StopReason: "end_turn", Auto: true})
	got := h.Turns(0, 0)
	if len(got) != 2 || got[0].CostUSD != 0.75 || got[0].Agent != "claude" || got[0].Cwd != "/w" || got[1].CostUSD != 0 || !got[1].Auto {
		t.Fatalf("turns = %+v", got)
	}
}

// Token reports add up per model onto the turn record; the next turn starts from none.
func TestTurnTokens(t *testing.T) {
	h := &Hub{turns: newTurnLog(t.TempDir()), subs: map[*Sub]struct{}{}, log: slog.Default()}
	s := &Session{h: h, loaded: true, rec: Record{ID: "s1", Agent: "agent"}}
	report := func(tk acpx.Tokens) { s.onGerdrInfo(map[string]any{"tokens": tk}) }
	report(acpx.Tokens{Model: "p/big", Input: 1000, Output: 50, CacheRead: 800, CacheWrite: 100})
	report(acpx.Tokens{Model: "p/small", Input: 300, Output: 20}) // compaction on the small model
	report(acpx.Tokens{Model: "p/big", Input: 1200, Output: 70, CacheRead: 1000})
	s.emit(&Event{Kind: EvTurnEnd, StopReason: "end_turn"})
	s.emit(&Event{Kind: EvTurnEnd, StopReason: "end_turn"})
	got := h.Turns(0, 0)
	want := []acpx.Tokens{{Model: "p/big", Input: 2200, Output: 120, CacheRead: 1800, CacheWrite: 100}, {Model: "p/small", Input: 300, Output: 20}}
	if len(got) != 2 || !reflect.DeepEqual(got[0].Tokens, want) || got[1].Tokens != nil {
		t.Fatalf("turns = %+v", got)
	}
}
