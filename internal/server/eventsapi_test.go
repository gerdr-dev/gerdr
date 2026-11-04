package server

import (
	"log/slog"
	"strings"
	"testing"

	"github.com/gerdr-dev/gerdr/internal/events"
)

func TestWatchText(t *testing.T) {
	e := events.Event{Source: "gitlab", Kind: "mr.note", Key: "g/r!34", Title: "alice commented", Text: "rename this", URL: "https://x/34"}
	got := watchText(e, events.Watch{ID: "w-1", Note: "fix review comments"})
	for _, want := range []string{"[事件 gitlab/mr.note] alice commented (g/r!34)", "rename this", "https://x/34", "fix review comments", "gerdr event unwatch w-1"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if once := watchText(e, events.Watch{ID: "w-2", Once: true}); strings.Contains(once, "unwatch") || !strings.Contains(once, "一次性") {
		t.Errorf("a once watch says it's gone:\n%s", once)
	}
}

// A watch whose session no longer exists is dropped when an event comes for it.
func TestWatchOfGoneSessionDropped(t *testing.T) {
	st, err := events.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Hub: reportHub(t), Events: st, Log: slog.Default()}
	if _, err := st.AddWatch(events.Watch{On: "ci/*", Session: "ghost"}); err != nil {
		t.Fatal(err)
	}
	s.deliverWatches(events.Event{Source: "ci", Kind: "pipeline.failed"})
	if l := st.Watches(""); len(l) != 0 {
		t.Fatalf("gone session's watch kept: %+v", l)
	}
}
