package work

import (
	"context"
	"testing"
	"time"
)

// A run whose end reached its reader is dropped after keepReceived, not held for keepDone with its whole result.
func TestRunReceivedShortensKeep(t *testing.T) {
	var got []time.Duration
	rn := &Run{wake: make(chan struct{}), forget: func(d time.Duration) { got = append(got, d) }}
	rn.Received() // not ended yet: nothing to shorten
	if len(got) != 0 {
		t.Fatalf("forgot a running run: %v", got)
	}
	rn.push(Event{Type: "result", Data: map[string]any{"value": 1}}, true)
	if v, err := rn.Wait(context.Background()); err != nil || v != 1 {
		t.Fatalf("Wait = %v, %v", v, err)
	}
	rn.Received() // a second reader: once is enough
	if len(got) != 1 || got[0] != keepReceived {
		t.Fatalf("forget calls = %v, want [%v]", got, keepReceived)
	}
}
