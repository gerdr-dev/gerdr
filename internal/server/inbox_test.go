package server

import (
	"testing"

	"github.com/gerdr-dev/gerdr/internal/hub"

	"github.com/gerdr-dev/gerdr/internal/work"
)

func TestPanelInbox(t *testing.T) {
	dir := t.TempDir()
	h, err := hub.New(hub.Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Dir: dir, Hub: h}
	a := work.InboxItem{ID: "t3", Title: "t3 问你：选哪个", Session: "s1"}
	if err := s.SetInbox("lb/board", []work.InboxItem{a}); err != nil {
		t.Fatal(err)
	}
	got := s.InboxItems()
	if len(got) != 1 || got[0].Panel != "lb/board" || got[0].At == 0 {
		t.Fatalf("got %+v", got)
	}
	at := got[0].At
	if n := len(s.watch().notices); n != 1 || s.watch().notices[0].Status != "waiting" || s.watch().notices[0].Session != "s1" {
		t.Fatalf("notices %+v", s.watch().notices)
	}
	// The same item again: its time stays, no second notice
	b := work.InboxItem{ID: "t5", Title: "t5 要你做：登录"}
	_ = s.SetInbox("lb/board", []work.InboxItem{a, b})
	if got := s.InboxItems(); len(got) != 2 || got[1].At != at {
		t.Fatalf("got %+v", got)
	}
	if n := len(s.watch().notices); n != 2 {
		t.Fatalf("want 2 notices, got %d", n)
	}
	// Another panel's list is its own; an item left out is gone; it survives a restart
	_ = s.SetInbox("other", []work.InboxItem{{ID: "x", Title: "x"}})
	_ = s.SetInbox("lb/board", []work.InboxItem{b})
	s2 := &Server{Dir: dir, Hub: h}
	got = s2.InboxItems()
	if len(got) != 2 {
		t.Fatalf("after reload %+v", got)
	}
	_ = s2.SetInbox("lb/board", nil)
	if got := s2.InboxItems(); len(got) != 1 || got[0].Panel != "other" {
		t.Fatalf("after clearing %+v", got)
	}
}
