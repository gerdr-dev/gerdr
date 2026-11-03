package hub

import (
	"strings"
	"testing"
)

// Tags change key by key and survive a restart; a bad key is refused without touching the others.
func TestSetTags(t *testing.T) {
	dir := hubDir(t)
	h := newHubWith(t, dir, "s1")
	s, _ := h.Session("s1")
	if err := s.SetTags(map[string]string{"task": "t1", "mr": "g/r/3"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetTags(map[string]string{"mr": "", "step": "2"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetTags(map[string]string{"bad key": "x"}); err == nil {
		t.Error("a key with a space: want an error")
	}
	want := map[string]string{"task": "t1", "step": "2"}
	if got := s.Summary().Tags; len(got) != 2 || got["task"] != "t1" || got["step"] != "2" {
		t.Errorf("tags = %v, want %v", got, want)
	}
	h2, err := New(Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	s2, _ := h2.Session("s1")
	if got := s2.Summary().Tags; got["task"] != "t1" || got["step"] != "2" || len(got) != 2 {
		t.Errorf("after restart: %v", got)
	}
	if err := s2.SetTags(map[string]string{"task": "", "step": ""}); err != nil || s2.Summary().Tags != nil {
		t.Errorf("all removed: %v, err %v", s2.Summary().Tags, err)
	}
}

func TestHasTags(t *testing.T) {
	tags := map[string]string{"task": "t1", "mr": "g/r/3"}
	for _, c := range []struct {
		want map[string]string
		ok   bool
	}{
		{nil, true},
		{map[string]string{"task": "t1"}, true},
		{map[string]string{"mr": ""}, true},
		{map[string]string{"task": "t2"}, false},
		{map[string]string{"nope": ""}, false},
		{map[string]string{"task": "t1", "nope": ""}, false},
	} {
		if HasTags(tags, c.want) != c.ok {
			t.Errorf("HasTags(%v) = %v", c.want, !c.ok)
		}
	}
}

// Reports may chain (a → b → c) but not loop, or two sessions would wake each other forever.
func TestReportToRefusesLoops(t *testing.T) {
	h := newHubWith(t, hubDir(t), "a", "b", "c")
	a, _ := h.Session("a")
	b, _ := h.Session("b")
	c, _ := h.Session("c")
	if err := a.SetReportTo("b"); err != nil {
		t.Fatal(err)
	}
	if err := b.SetReportTo("c"); err != nil {
		t.Fatal(err)
	}
	if err := c.SetReportTo("a"); err == nil || !strings.Contains(err.Error(), "loop") && !strings.Contains(err.Error(), "绕回") {
		t.Errorf("c → a closes a loop: err = %v", err)
	}
	if err := a.SetReportTo("a"); err == nil {
		t.Error("reporting to itself: want an error")
	}
	if err := a.SetReportTo("zzzz"); err == nil {
		t.Error("no such target: want an error")
	}
	if err := b.SetReportTo(""); err != nil || b.Summary().ReportTo != "" {
		t.Errorf("clearing: %v / %q", err, b.Summary().ReportTo)
	}
	if err := c.SetReportTo("a"); err != nil {
		t.Errorf("no loop any more: %v", err)
	}
}
