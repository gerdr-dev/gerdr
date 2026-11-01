package hub

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gerdr-dev/gerdr/acpx"
)

// The turn log: one line per finished turn, every session, every agent, in <dir>/turns/2026-11.jsonl (local month).
// Facts only — when it ended, how, how long, how many tokens, what it cost — so add-ons can count whatever they like
// (the sidebar's "today" add-on sums a day's turns and money, g.turns). Gerdr itself keeps no tallies: what counts as
// "a task done", which day a turn belongs to, whether Codex's tokens should be priced, are the add-on's call.
//
// Why a log of its own when every session's log already has its turn_end: those go away with the session (delete,
// clear), and summing a week would mean reading every session's whole log. This is a few hundred short lines a day.
//
// Why here and not in an add-on's service: a UI add-on runs in every open window (two windows would count a turn
// twice) and in none while no window is open; the hub sees each turn exactly once, window or not.

type TurnRecord struct {
	TS           int64   `json:"ts"` // when it ended, unix ms
	SessionID    string  `json:"sessionId"`
	Agent        string  `json:"agent"`
	Cwd          string  `json:"cwd,omitempty"`
	StopReason   string  `json:"stopReason"`     // end_turn / cancelled / error / max_tokens / …
	Auto         bool    `json:"auto,omitempty"` // the agent started it itself (a background task finished), no user message
	DurationMs   int64   `json:"durationMs,omitempty"`
	OutputTokens int     `json:"outputTokens,omitempty"`
	CostUSD      float64 `json:"costUsd,omitempty"` // what the agent reported for this turn (Claude Code only; Codex reports tokens)
	// Tokens: what the turn's model calls used, one entry per model (Gerdr Agent only, acpx.TokensKey). Tokens, not
	// money: pricing them is the add-on's call (the `today` add-on keeps a price table in its settings).
	Tokens []acpx.Tokens `json:"tokens,omitempty"`
}

// addTokens adds one model call's report onto the entry of its model.
func addTokens(list []acpx.Tokens, t acpx.Tokens) []acpx.Tokens {
	for i := range list {
		if list[i].Model == t.Model {
			list[i].Input += t.Input
			list[i].Output += t.Output
			list[i].CacheRead += t.CacheRead
			list[i].CacheWrite += t.CacheWrite
			return list
		}
	}
	return append(list, t)
}

type turnLog struct {
	dir string
	mu  sync.Mutex
}

func newTurnLog(dir string) *turnLog { return &turnLog{dir: filepath.Join(dir, "turns")} }

func (l *turnLog) file(t time.Time) string { return filepath.Join(l.dir, t.Format("2006-01")+".jsonl") }

func (l *turnLog) append(r TurnRecord) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := os.MkdirAll(l.dir, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(l.file(time.UnixMilli(r.TS)), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// read returns the turns that ended in [since, until) (unix ms), oldest first, reading only the months in between.
func (l *turnLog) read(since, until int64) []TurnRecord {
	out := []TurnRecord{}
	l.mu.Lock()
	defer l.mu.Unlock()
	from, to := time.UnixMilli(since), time.UnixMilli(until)
	for m := time.Date(from.Year(), from.Month(), 1, 0, 0, 0, 0, time.Local); !m.After(to); m = m.AddDate(0, 1, 0) {
		f, err := os.Open(l.file(m))
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64*1024), 1<<20)
		for sc.Scan() {
			var r TurnRecord
			if json.Unmarshal(sc.Bytes(), &r) == nil && r.TS >= since && r.TS < until {
				out = append(out, r)
			}
		}
		f.Close()
	}
	return out
}

// recordTurn: a turn ended (Session.emit, for turn_end). Logged and broadcast as "turn" (→ g.on("turn") in add-ons).
func (h *Hub) recordTurn(r TurnRecord) {
	if h.turns == nil {
		return
	}
	if err := h.turns.append(r); err != nil {
		h.log.Warn("turn log", "err", err)
	}
	h.Broadcast("turn", r)
}

// Turns: the turns that ended in [since, until) (unix ms; until 0 = now), oldest first.
func (h *Hub) Turns(since, until int64) []TurnRecord {
	if h.turns == nil {
		return []TurnRecord{}
	}
	if until <= 0 {
		until = time.Now().UnixMilli() + 1
	}
	return h.turns.read(since, until)
}
