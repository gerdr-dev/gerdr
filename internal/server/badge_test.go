package server

import (
	"testing"

	"github.com/gerdr-dev/gerdr/internal/hub"
)

func TestPanelBadges(t *testing.T) {
	dir := t.TempDir()
	h, err := hub.New(hub.Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Dir: dir, Hub: h}
	_ = s.SetBadge("lb/board", 0, false) // a dot
	if got := s.Badges(); len(got) != 1 || got["lb/board"] != 0 {
		t.Fatalf("dot: %v", got)
	}
	_ = s.SetBadge("lb/board", 1, true) // a dot counts as 0
	_ = s.SetBadge("lb/board", 2, true)
	if got := s.Badges()["lb/board"]; got != 3 {
		t.Fatalf("add: %d", got)
	}
	_ = s.SetBadge("other", 5, false)
	s2 := &Server{Dir: dir, Hub: h} // survives a restart
	if got := s2.Badges(); len(got) != 2 || got["other"] != 5 {
		t.Fatalf("after reload %v", got)
	}
	_ = s2.SetBadge("lb/board", -1, false)
	if got := s2.Badges(); len(got) != 1 {
		t.Fatalf("after clearing %v", got)
	}
	if err := s2.SetBadge("", 1, false); err == nil {
		t.Fatal("no panel accepted")
	}
}
