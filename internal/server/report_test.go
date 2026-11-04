package server

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gerdr-dev/gerdr/internal/hub"
)

func TestReportKind(t *testing.T) {
	for _, c := range []struct{ old, cur, want string }{
		{"running", "idle", "done"},
		{"waiting", "idle", "done"},
		{"starting", "idle", "done"},
		{"running", "waiting", "waiting"},
		{"running", "error", "error"},
		{"idle", "running", ""},
		{"running", "running", ""},
		{"", "idle", ""}, // first sight: not a change
		{"error", "idle", ""},
	} {
		if got := reportKind(c.old, c.cur); got != c.want {
			t.Errorf("%s → %s: %q, want %q", c.old, c.cur, got, c.want)
		}
	}
}

// reportHub: sessions w1, w2 (workers reporting to "boss") and boss, w1 with a reply in its log.
func reportHub(t *testing.T) *hub.Hub {
	t.Helper()
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "sessions"), 0o700)
	recs := []hub.Record{
		{ID: "w1", Agent: "claude", AgentSessionID: "a1", Title: "fix login", Tags: map[string]string{"task": "t1"}, ReportTo: "boss"},
		{ID: "w2", Agent: "claude", AgentSessionID: "a2", ReportTo: "boss"},
		{ID: "boss", Agent: "claude", AgentSessionID: "a3"},
	}
	b, _ := json.Marshal(recs)
	os.WriteFile(filepath.Join(dir, "sessions", "index.json"), b, 0o600)
	var log bytes.Buffer
	for _, e := range []*hub.Event{
		{Seq: 1, TS: 1, Kind: hub.EvUser, MessageID: "m1"},
		{Seq: 2, TS: 2, Kind: hub.EvUpdate, Update: json.RawMessage(`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"MR opened: g/api/34"}}`)},
		{Seq: 3, TS: 3, Kind: hub.EvTurnEnd, StopReason: "end_turn"},
	} {
		j, _ := json.Marshal(e)
		log.Write(append(j, '\n'))
	}
	os.WriteFile(filepath.Join(dir, "sessions", "w1.jsonl"), log.Bytes(), 0o600)
	h, err := hub.New(hub.Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	return h
}

type sent struct{ target, text, from string }

// Workers stopping while the coordinator is busy are held, then go as one message once it's free; a worker that
// stopped twice in the meantime is reported once, with its latest state.
func TestReportsBatchWhileBusy(t *testing.T) {
	h := reportHub(t)
	rp := newReporter(&Server{Hub: h})
	rp.delay = 10 * time.Millisecond
	var mu sync.Mutex
	bossStatus := hub.StRunning
	var out []sent
	rp.state = func(id string) (string, bool) {
		mu.Lock()
		defer mu.Unlock()
		if id != "boss" {
			return "", false
		}
		return bossStatus, true
	}
	rp.deliver = func(target, text, from string) error {
		mu.Lock()
		out = append(out, sent{target, text, from})
		mu.Unlock()
		return nil
	}
	sum := func(id, status string) hub.Summary {
		s, _ := h.Session(id)
		x := s.Summary()
		x.Status = status
		return x
	}
	rp.onSession(sum("boss", hub.StRunning))
	for _, st := range []string{hub.StRunning, hub.StWaiting, hub.StRunning, hub.StIdle} {
		rp.onSession(sum("w1", st))
	}
	rp.onSession(sum("w2", hub.StRunning))
	rp.onSession(sum("w2", hub.StError))
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	if len(out) != 0 {
		t.Fatalf("delivered while the coordinator was busy: %+v", out)
	}
	bossStatus = hub.StIdle
	mu.Unlock()
	rp.onSession(sum("boss", hub.StIdle))
	time.Sleep(80 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(out) != 1 {
		t.Fatalf("want one batched message, got %d: %+v", len(out), out)
	}
	m := out[0]
	if m.target != "boss" || m.from != "w1" {
		t.Errorf("to %q from %q", m.target, m.from)
	}
	if strings.Count(m.text, "(w1)") != 1 || !strings.Contains(m.text, "(w2)") {
		t.Errorf("each worker once:\n%s", m.text)
	}
	for _, want := range []string{"fix login", "task=t1", "MR opened: g/api/34", "gerdr session read w1"} {
		if !strings.Contains(m.text, want) {
			t.Errorf("missing %q in:\n%s", want, m.text)
		}
	}
	// w1's latest state is done, not the waiting it went through; w2 failed
	i1, i2 := strings.Index(m.text, "(w1)"), strings.Index(m.text, "(w2)")
	if i1 < 0 || i2 < 0 || i1 > i2 {
		t.Errorf("oldest first: w1 at %d, w2 at %d", i1, i2)
	}
	if seg := m.text[i1:i2]; strings.Contains(seg, "等人") || strings.Contains(seg, "waiting for a person") {
		t.Errorf("w1 reported as waiting, want its latest state (done):\n%s", seg)
	}
}

// A session without ReportTo, or seen for the first time, reports nothing.
func TestReportsOnlyForReportTo(t *testing.T) {
	h := reportHub(t)
	rp := newReporter(&Server{Hub: h})
	rp.delay = 5 * time.Millisecond
	var n int
	var mu sync.Mutex
	rp.state = func(string) (string, bool) { return hub.StIdle, true }
	rp.deliver = func(string, string, string) error { mu.Lock(); n++; mu.Unlock(); return nil }
	boss, _ := h.Session("boss")
	x := boss.Summary()
	x.Status = hub.StRunning
	rp.onSession(x)
	x.Status = hub.StIdle
	rp.onSession(x) // boss reports to nobody
	w2, _ := h.Session("w2")
	y := w2.Summary()
	y.Status = hub.StIdle
	rp.onSession(y) // first sight
	time.Sleep(40 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if n != 0 {
		t.Errorf("delivered %d reports, want none", n)
	}
}

// A turn the person drove in the worker's own chat isn't reported: they read the answer there.
func TestReportsSkipTurnsByPerson(t *testing.T) {
	h := reportHub(t)
	rp := newReporter(&Server{Hub: h})
	rp.delay = 5 * time.Millisecond
	var n int
	var mu sync.Mutex
	rp.state = func(string) (string, bool) { return hub.StIdle, true }
	rp.deliver = func(string, string, string) error { mu.Lock(); n++; mu.Unlock(); return nil }
	w, _ := h.Session("w1")
	x := w.Summary()
	if x.ReportTo == "" {
		t.Fatal("w1 should report somewhere in reportHub")
	}
	x.ByPerson = true
	x.Status = hub.StRunning
	rp.onSession(x)
	x.Status = hub.StIdle
	rp.onSession(x)
	time.Sleep(40 * time.Millisecond)
	mu.Lock()
	if n != 0 {
		t.Errorf("a turn by a person was reported (%d)", n)
	}
	mu.Unlock()
	x.ByPerson = false // the coordinator (or code) sends next: reported again
	x.Status = hub.StRunning
	rp.onSession(x)
	x.Status = hub.StIdle
	rp.onSession(x)
	time.Sleep(40 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if n != 1 {
		t.Errorf("delivered %d reports after a coordinator's turn, want 1", n)
	}
}

// A turn the person drove that stops on a question still reaches the coordinator: they said a word and left, and the
// question is the coordinator's to answer (a message answers it). An approval is reported as the user's to give.
func TestReportsQuestionsEvenByPerson(t *testing.T) {
	h := reportHub(t)
	rp := newReporter(&Server{Hub: h})
	rp.delay = 5 * time.Millisecond
	var texts []string
	var mu sync.Mutex
	rp.state = func(string) (string, bool) { return hub.StIdle, true }
	rp.deliver = func(_ string, text, _ string) error { mu.Lock(); texts = append(texts, text); mu.Unlock(); return nil }
	w, _ := h.Session("w1")
	x := w.Summary()
	// The report is written from the session as it is then: this one's state, as set below
	rp.source = func(string) (hub.Summary, []*hub.Event, error) { return x, nil, nil }
	x.ByPerson = true
	x.Status = hub.StRunning
	rp.onSession(x)
	x.Status, x.WaitingKind, x.Waiting = hub.StWaiting, "ask", "回答：选哪个？（A / B）"
	rp.onSession(x)
	time.Sleep(40 * time.Millisecond)
	mu.Lock()
	if len(texts) != 1 || !strings.Contains(texts[0], "gerdr session send w1") || !strings.Contains(texts[0], "选哪个") {
		t.Fatalf("question not handed to the coordinator: %q", texts)
	}
	mu.Unlock()
	x.Status, x.WaitingKind, x.Waiting = hub.StRunning, "", ""
	rp.onSession(x)
	x.Status, x.WaitingKind, x.Waiting = hub.StWaiting, "permission", "审批：rm -rf x"
	rp.onSession(x)
	time.Sleep(40 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(texts) != 2 || !strings.Contains(texts[1], "只有用户能处理") || strings.Contains(texts[1], "gerdr session send") {
		t.Fatalf("approval: %q", texts)
	}
}
