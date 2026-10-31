package hub

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Daily stats: how many turns finished and what they cost, per local day. Add-ons show them (the sidebar's "today"
// block, GET /api/stats); the wristband's Agent cat had the same two numbers and computed them on its own.
//
// Done = a turn that ended normally (end_turn, auto turns included): what a person would call "it finished a task".
// Cancelled and failed turns aren't done.
//
// Cost = what the agent itself reports. Only Claude Code does: its stream-json "result" carries total_cost_usd, the
// running total of that process (the adapter passes it on as _meta.gerdr.cost). The delta since the last one is
// added to today; a smaller total means a new process (resume after the idle reaper), counted from zero. Codex and
// gerdr's own agent report tokens, not money, so they don't add to it.

type DayStats struct {
	Date string             `json:"date"` // 2026-10-31, local time
	Done int                `json:"done"`
	Cost map[string]float64 `json:"cost,omitempty"` // USD by agent
}

type statsFile struct {
	Days map[string]*DayStats `json:"days"`
}

type stats struct {
	path string

	mu    sync.Mutex
	data  statsFile
	last  map[string]float64 // session id → the last total_cost_usd seen
	dirty bool
	timer *time.Timer
}

func newStats(dir string) *stats {
	st := &stats{path: filepath.Join(dir, "stats.json"), data: statsFile{Days: map[string]*DayStats{}}, last: map[string]float64{}}
	if b, err := os.ReadFile(st.path); err == nil {
		_ = json.Unmarshal(b, &st.data)
		if st.data.Days == nil {
			st.data.Days = map[string]*DayStats{}
		}
	}
	return st
}

func dayKey(t time.Time) string { return t.Format("2006-01-02") }

func (st *stats) today() *DayStats {
	k := dayKey(time.Now())
	d := st.data.Days[k]
	if d == nil {
		d = &DayStats{Date: k}
		st.data.Days[k] = d
	}
	return d
}

func (h *Hub) statsChanged() {
	st := h.stats
	if st == nil {
		return
	}
	st.dirty = true
	if st.timer == nil {
		// Coalesce: a busy afternoon ends a turn every few seconds; windows and the file needn't follow each one.
		st.timer = time.AfterFunc(2*time.Second, func() {
			st.mu.Lock()
			st.timer = nil
			st.mu.Unlock()
			st.save()
			h.Broadcast("stats", nil)
		})
	}
}

// countDone: a turn ended (called from emit for EvTurnEnd).
func (h *Hub) countDone(stop string) {
	if h.stats == nil || stop != "end_turn" {
		return
	}
	h.stats.mu.Lock()
	h.stats.today().Done++
	h.statsChanged()
	h.stats.mu.Unlock()
}

// addCost: an agent process reported its running total.
func (h *Hub) addCost(sessionID, agent string, total float64) {
	if h.stats == nil || total < 0 {
		return
	}
	st := h.stats
	st.mu.Lock()
	defer st.mu.Unlock()
	prev, seen := st.last[sessionID]
	st.last[sessionID] = total
	delta := total
	if seen && total >= prev {
		delta = total - prev
	}
	if delta <= 0 {
		return
	}
	d := st.today()
	if d.Cost == nil {
		d.Cost = map[string]float64{}
	}
	d.Cost[agent] += delta
	h.statsChanged()
}

func (st *stats) save() {
	st.mu.Lock()
	if !st.dirty {
		st.mu.Unlock()
		return
	}
	st.dirty = false
	// Keep a bit over a year
	keys := make([]string, 0, len(st.data.Days))
	for k := range st.data.Days {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys[:max(0, len(keys)-400)] {
		delete(st.data.Days, k)
	}
	b, _ := json.Marshal(st.data)
	st.mu.Unlock()
	tmp := st.path + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, st.path)
	}
}

// Stats returns the last n days (today last), days without anything included as zeros.
func (h *Hub) Stats(n int) []DayStats {
	if n <= 0 {
		n = 1
	}
	out := make([]DayStats, 0, n)
	if h.stats == nil {
		return out
	}
	h.stats.mu.Lock()
	defer h.stats.mu.Unlock()
	now := time.Now()
	for i := n - 1; i >= 0; i-- {
		k := dayKey(now.AddDate(0, 0, -i))
		d := DayStats{Date: k}
		if v := h.stats.data.Days[k]; v != nil {
			d.Done = v.Done
			if len(v.Cost) > 0 {
				d.Cost = map[string]float64{}
				for a, c := range v.Cost {
					d.Cost[a] = c
				}
			}
		}
		out = append(out, d)
	}
	return out
}
