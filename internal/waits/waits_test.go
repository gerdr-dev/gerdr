package waits

import (
	"strings"
	"testing"
)

func TestCond(t *testing.T) {
	st := map[string]any{"state": "opened", "pipeline": "failed", "reviews": float64(2), "mergeable": false, "need_rebase": true, "head": "abc", "me": "bob",
		"notes": []any{
			map[string]any{"id": float64(10), "author": "alice", "resolvable": true, "resolved": false, "path": "a.go"},
			map[string]any{"id": float64(11), "author": "bob", "resolvable": true, "resolved": false},
		},
		"weird-name": float64(1)}
	for _, c := range []struct {
		src  string
		want bool
	}{
		{"state != 'opened'", false},
		{"state == 'opened'", true},
		{`state == "merged" || pipeline == "failed"`, true},
		{"reviews > 2", false},
		{"reviews >= 2 && !mergeable", true},
		{"need_rebase", true},
		{"!(state == 'opened' && reviews <= 2)", false},
		{"notes.some(n => n.author != me && n.resolvable && !n.resolved)", true},
		{"notes.filter(n => n.author != me && n.id > 10).length > 0", false},
		{"notes.some(n => n.path == 'a.go')", true},
		{"$['weird-name'] == 1", true},
		{"notes.length", true},
	} {
		cond, err := Parse(c.src)
		if err != nil {
			t.Fatalf("%s: %v", c.src, err)
		}
		if got, err := cond.Eval(st); err != nil || got != c.want {
			t.Errorf("%s = %v (%v), want %v", c.src, got, err, c.want)
		}
	}
	for _, bad := range []string{"", "state ==", "a; b", "return 1", ") || (1"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
	// A field the state doesn't have is an error, not false
	if _, err := mustParse("reveiws > 0").Eval(st); err == nil || !strings.Contains(err.Error(), "reveiws") {
		t.Errorf("typo: %v", err)
	}
	// Nothing outside the state, and no running forever
	if _, err := mustParse("require('fs')").Eval(st); err == nil {
		t.Error("require is there")
	}
	if _, err := mustParse("(() => { while (true) {} })()").Eval(st); err == nil || !strings.Contains(err.Error(), "50ms") {
		t.Errorf("a loop: %v", err)
	}
}

func mustParse(s string) *Cond {
	c, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return c
}

// A wait on a state that already holds is due at once; one that doesn't waits; a woken wait is gone; all kept
// across a restart.
func TestStoreDue(t *testing.T) {
	root := t.TempDir()
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	changes := 0
	s.OnChange(func() { changes++ })
	if _, err := s.Add(Wait{Source: "gitlab", Key: "h/g/r!*", Until: "a", Session: "s"}); err == nil {
		t.Fatal("a key pattern was taken")
	}
	if _, err := s.Add(Wait{Source: "gitlab", Key: "h/g/r!1", Until: "a ==", Session: "s"}); err == nil {
		t.Fatal("a broken condition was taken")
	}
	w1, _ := s.Add(Wait{Source: "gitlab", Key: "h/g/r!1", Until: "reviews > 0", Session: "s"})
	w2, _ := s.Add(Wait{Source: "gitlab", Key: "h/g/r!1", Until: "state != opened", Session: "s"})
	s.Add(Wait{Source: "gitlab", Key: "h/g/r!2", Until: "reviews > 0", Session: "s2"})
	if due, _, _ := s.Due("gitlab", "h/g/r!1"); len(due) != 0 {
		t.Fatalf("due with no state: %+v", due)
	}
	_ = s.SetState(State{Source: "gitlab", Key: "h/g/r!1", State: map[string]any{"state": "opened", "reviews": float64(1)}})
	due, st, _ := s.Due("gitlab", "h/g/r!1")
	if len(due) != 1 || due[0].ID != w1.ID || st.State["reviews"] != float64(1) {
		t.Fatalf("due %+v", due)
	}
	if changes != 4 {
		t.Fatalf("changes %d", changes)
	}
	_ = s.Flush()
	s2, _ := Open(root)
	if l := s2.List(""); len(l) != 2 || l[0].ID != w2.ID {
		t.Fatalf("after reopening: %+v", l)
	}
	if st, ok := s2.StateOf("gitlab", "h/g/r!1"); !ok || st.State["state"] != "opened" {
		t.Fatalf("state after reopening: %+v", st)
	}
	s2.DropSession("s2")
	s2.Forget(func(source, key string) bool { return false })
	if _, ok := s2.StateOf("gitlab", "h/g/r!1"); ok || len(s2.List("")) != 1 {
		t.Fatal("forget / drop")
	}
}
