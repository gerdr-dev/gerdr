package server

import (
	"errors"
	"log/slog"
	"testing"

	"github.com/gerdr-dev/gerdr/internal/chromeext"
)

// Each project's agents see and drive only their project's pages (asked for: separate browsers).
func TestScopedTab(t *testing.T) {
	b := chromeext.New(slog.Default())
	s := &Server{}
	lb1 := b.Adopt(chromeext.Page{Project: "lb", URL: "https://a"}).ID()
	def := b.Adopt(chromeext.Page{Project: "default", URL: "https://b"}).ID()
	lb2 := b.Adopt(chromeext.Page{Project: "lb", URL: "https://c"}).ID()
	last := b.Adopt(chromeext.Page{Project: "default", URL: "https://d"}).ID()

	// no number: the last page of the caller's project, not the last one anywhere
	if got, err := s.scopedTab(b, "lb", "", 0); err != nil || got != lb2 {
		t.Errorf("lb, last: %d %v, want %d", got, err, lb2)
	}
	if got, _ := s.scopedTab(b, "default", "", 0); got != last {
		t.Errorf("default, last: %d, want %d", got, last)
	}
	// another project's page: refused as if it didn't exist
	if _, err := s.scopedTab(b, "lb", "", def); !errors.Is(err, errNoPageHere) {
		t.Errorf("lb driving default's page: %v, want refused", err)
	}
	if got, err := s.scopedTab(b, "lb", "", lb1); err != nil || got != lb1 {
		t.Errorf("lb, own page: %d %v", got, err)
	}
	// a project with no page
	if _, err := s.scopedTab(b, "other", "", 0); !errors.Is(err, errNoPageHere) {
		t.Errorf("empty project: %v, want refused", err)
	}
	// the UI (no scope): every page, numbers as given
	if got, _ := s.scopedTab(b, "", "", def); got != def {
		t.Errorf("unscoped: %d", got)
	}
	if n := len(s.scopedTabs(b, "lb")); n != 2 {
		t.Errorf("lb sees %d pages, want 2", n)
	}
	// no number, from a conversation: the last page it opened, not another conversation's opened after it
	b.SetSession(lb1, "s1")
	if got, _ := s.scopedTab(b, "lb", "s1", 0); got != lb1 {
		t.Errorf("s1, own last: %d, want %d", got, lb1)
	}
	// a conversation with no page of its own: the project's last, as before
	if got, _ := s.scopedTab(b, "lb", "s2", 0); got != lb2 {
		t.Errorf("s2, project's last: %d, want %d", got, lb2)
	}
}

// A page of the user's Chrome belongs to the project of the agent that opened it, and so do the pages it opened; one
// nobody known opened is everyone's.
func TestTabScopeChrome(t *testing.T) {
	s := &Server{}
	s.noteChromeScope(1, "lb")
	all := []chromeext.Tab{{ID: 1}, {ID: 2, Opener: 1}, {ID: 3}}
	for _, c := range []struct {
		tab  chromeext.Tab
		want string
	}{{all[0], "lb"}, {all[1], "lb"}, {all[2], ""}} {
		if got := s.tabScope(c.tab, all); got != c.want {
			t.Errorf("tab %d: %q, want %q", c.tab.ID, got, c.want)
		}
	}
}
