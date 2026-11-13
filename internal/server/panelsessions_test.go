package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/gerdr-dev/gerdr/internal/hub"
)

// A conversation works for the panel that started it, or, handed work by one, for that one's panel.
func TestPanelOf(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "sessions"), 0o700)
	recs := []hub.Record{
		{ID: "w", Agent: "claude", AgentSessionID: "a1", Tags: map[string]string{PanelTag: "lb/board"}},
		{ID: "helper", Agent: "claude", AgentSessionID: "a2", ReportTo: "w"},
		{ID: "deeper", Agent: "claude", AgentSessionID: "a3", ReportTo: "helper"},
		{ID: "mine", Agent: "claude", AgentSessionID: "a4"},
		{ID: "loop1", Agent: "claude", AgentSessionID: "a5", ReportTo: "loop2"},
		{ID: "loop2", Agent: "claude", AgentSessionID: "a6", ReportTo: "loop1"},
	}
	b, _ := json.Marshal(recs)
	os.WriteFile(filepath.Join(dir, "sessions", "index.json"), b, 0o600)
	h, err := hub.New(hub.Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	s := &Server{Hub: h}
	for id, want := range map[string]string{"w": "lb/board", "helper": "lb/board", "deeper": "lb/board", "mine": "", "loop1": ""} {
		sess, err := h.Session(id)
		if err != nil {
			t.Fatal(err)
		}
		if got := s.panelOf(sess.Summary()); got != want {
			t.Errorf("%s: panel %q, want %q", id, got, want)
		}
	}
}
