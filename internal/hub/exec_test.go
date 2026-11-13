package hub

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRunCommand(t *testing.T) {
	dir := t.TempDir()
	r, err := RunCommand(context.Background(), "cat; echo; pwd; exit 2", dir, "from-stdin", 0)
	if err != nil || r.Code != 2 || !strings.Contains(r.Output, "from-stdin") || r.Truncated {
		t.Fatalf("%+v %v", r, err)
	}
	// Stopped by the caller going away: the whole group, sleep included, and quickly
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	r, err = RunCommand(ctx, "sleep 30 & wait", "", "", time.Minute)
	if err != nil || r.Code == 0 || r.TimedOut || time.Since(start) > 5*time.Second {
		t.Fatalf("cancel: %+v %v after %s", r, err, time.Since(start))
	}
	if _, err := RunCommand(context.Background(), "true", "relative/dir", "", 0); err == nil {
		t.Fatal("a relative directory: want an error")
	}
}

func TestHeadTail(t *testing.T) {
	h := &headTail{}
	_, _ = h.Write([]byte("FIRST"))
	_, _ = h.Write([]byte(strings.Repeat("x", 3*execKeep)))
	_, _ = h.Write([]byte("LAST"))
	s, cut := h.text()
	if !cut || !strings.HasPrefix(s, "FIRST") || !strings.HasSuffix(s, "LAST") || len(s) > 2*execKeep+200 {
		t.Fatalf("cut=%v len=%d", cut, len(s))
	}
}
