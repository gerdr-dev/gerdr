package hub

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Stop answers before the agent has stopped (a claude starting up took ~1.2s, and the stop button hung that long):
// the message is back at once, the session reads idle, the turn's late output never reaches the log, and the agent
// is rewound in the background before the next message goes.
func TestEscapeRollbackDoesNotWaitForAgent(t *testing.T) {
	s, f := startFake(t, false)
	f.silent, f.stopDelay, f.started = true, 500*time.Millisecond, make(chan struct{})
	r := send(t, s, "hello")
	<-f.started
	t0 := time.Now()
	res, err := s.Escape(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(t0); d > 200*time.Millisecond {
		t.Fatalf("escape took %v, waited for the agent", d)
	}
	if !res.RolledBack || contentText(res.Content) != "hello" {
		t.Fatalf("got %+v", res)
	}
	if st := s.Summary().Status; st != StIdle {
		t.Fatalf("status %q right after stop", st)
	}
	if got := texts(s.Events()); len(got) != 0 {
		t.Fatalf("log still has %q", got)
	}
	send(t, s, "again") // waits for the stop to finish
	f.mu.Lock()
	rw := append([]string(nil), f.rewinds...)
	f.mu.Unlock()
	if len(rw) != 1 || rw[0] != r.MessageID {
		t.Fatalf("agent rewinds %q, want %q", rw, r.MessageID)
	}
	waitPrompt(t, f)
	waitIdle(t, s)
	for _, e := range s.Events() {
		if e.Kind == EvUpdate && strings.Contains(string(e.Update), "late") {
			t.Fatal("the stopped turn's late output reached the log")
		}
	}
	if got := texts(s.Events()); !eq(got, []string{"again"}) {
		t.Fatalf("log %q", got)
	}
}

// A turn the agent already answered in: stopped (turn_end) at once, once, without its late output.
func TestEscapeEndsTurnAtOnce(t *testing.T) {
	s, f := startFake(t, false)
	f.stopDelay, f.started = 500*time.Millisecond, make(chan struct{})
	send(t, s, "work")
	<-f.started
	waitAnswer(t, s)
	t0 := time.Now()
	if _, err := s.Escape(context.Background()); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(t0); d > 200*time.Millisecond {
		t.Fatalf("escape took %v", d)
	}
	ends := func() (n int, late bool) {
		for _, e := range s.Events() {
			if e.Kind == EvTurnEnd {
				n++
			}
			if e.Kind == EvUpdate && strings.Contains(string(e.Update), "late") {
				late = true
			}
		}
		return
	}
	if n, _ := ends(); n != 1 || s.Summary().Status != StIdle {
		t.Fatalf("%d turn ends, status %q right after stop", n, s.Summary().Status)
	}
	waitIdle(t, s)
	time.Sleep(100 * time.Millisecond)
	if n, late := ends(); n != 1 || late {
		t.Fatalf("%d turn ends, late output logged: %v", n, late)
	}
}
