package server

import (
	"testing"

	"github.com/gerdr-dev/gerdr/internal/chromeext"
)

// An agent re-running `gerdr browser open` on its dev server after each change gets its own page back, not a new one.
func TestSameSitePage(t *testing.T) {
	tabs := []chromeext.Tab{
		{ID: 1, Project: "p", Session: "a", URL: "http://localhost:8789/?t=1"},
		{ID: 2, Project: "p", Session: "b", URL: "http://localhost:8789/"},                  // another conversation's
		{ID: 3, Project: "q", Session: "a", URL: "http://localhost:8789/"},                  // another project's
		{ID: 4, Project: "p", Session: "a", URL: "http://localhost:8790/"},                  // another port
		{ID: 5, Project: "p", Session: "a", URL: "http://localhost:8789/x", Gone: "closed"}, // closed
	}
	cases := []struct {
		session, url string
		want         int
	}{
		{"a", "http://localhost:8789/?t=2&res=1280", 1},
		{"a", "http://localhost:8790/other", 4},
		{"a", "https://localhost:8789/", 0}, // another scheme
		{"a", "http://localhost:8800/", 0},
		{"b", "http://localhost:8789/", 2},
		{"", "http://localhost:8789/", 0}, // no session: the UI's own open, always new
		{"c", "http://localhost:8789/", 0},
		{"a", "not a url", 0},
	}
	for _, c := range cases {
		if got := sameSitePage(tabs, "p", c.session, c.url); got != c.want {
			t.Errorf("sameSitePage(%q, %q) = %d, want %d", c.session, c.url, got, c.want)
		}
	}
}
