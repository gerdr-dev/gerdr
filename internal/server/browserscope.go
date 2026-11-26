package server

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gerdr-dev/gerdr/internal/chromeext"
	"github.com/gerdr-dev/gerdr/internal/i18n"
)

// Each project's browser is its own (asked for): an agent sees and drives only its project's pages, not another
// project's (their logins, what the user is doing there). An agent's calls (gerdr browser, its MCP server) say who is
// calling (Caller); the page list, an action, show and close are then limited to that project. The UI's own calls say
// nobody and see every page: it already shows each project only its own (BrowserScreen, core/SessionBrowser.tsx).
//
// The user's own Chrome (extension pages, Project "") is one browser for every project, but a page an agent opened in
// it belongs to that agent's project (chromeScope), as do the pages it opened (Opener). Pages the user opened
// themselves, or whose opener is unknown, stay open to all.

// browserScope: the project a caller is limited to, "" = not limited (the UI, a shell outside Gerdr).
func (s *Server) browserScope(c Caller) string {
	if c.Project == "" && c.Session == "" && c.Term == "" {
		return ""
	}
	return s.browserProject(c)
}

// callerOf reads a Caller from a GET's query (from / project / term, as the bodies have them).
func callerOf(r *http.Request) Caller {
	q := r.URL.Query()
	return Caller{Project: q.Get("project"), Session: q.Get("from"), Term: q.Get("term")}
}

// noteChromeScope: an agent of project opened page in the user's Chrome.
func (s *Server) noteChromeScope(page int, project string) {
	if project == "" {
		return
	}
	s.bpanes.mu.Lock()
	if s.bpanes.chrome == nil {
		s.bpanes.chrome = map[int]string{}
	}
	s.bpanes.chrome[page] = project
	s.bpanes.mu.Unlock()
}

// tabScope: the project a page belongs to, "" = every project's (a user's Chrome page no agent opened).
func (s *Server) tabScope(t chromeext.Tab, all []chromeext.Tab) string {
	s.bpanes.mu.Lock()
	defer s.bpanes.mu.Unlock()
	for range 20 { // an opener chain, bounded
		if t.Project != "" {
			return t.Project
		}
		if p := s.bpanes.chrome[t.ID]; p != "" {
			return p
		}
		found := false
		for _, o := range all {
			if t.Opener != 0 && o.ID == t.Opener {
				t, found = o, true
				break
			}
		}
		if !found {
			return ""
		}
	}
	return ""
}

// scopedTabs: the pages scope may see, oldest first (all of them for "").
func (s *Server) scopedTabs(b *chromeext.Bridge, scope string) []chromeext.Tab {
	all := b.Tabs()
	if scope == "" {
		return all
	}
	out := make([]chromeext.Tab, 0, len(all))
	for _, t := range all {
		if p := s.tabScope(t, all); p == "" || p == scope {
			out = append(out, t)
		}
	}
	return out
}

var errNoPageHere = errors.New("no page")

// scopedTab: the page id a caller of scope means: tab, or for 0 the last one session opened that is still open, else
// the last one it may see. Session first: agents of one project share its browser, and "the project's last page" was
// another conversation's whenever two worked at once (user report: a screenshot of the other task's game). An id of
// another project's page is refused as if it didn't exist.
func (s *Server) scopedTab(b *chromeext.Bridge, scope, session string, tab int) (int, error) {
	if scope == "" {
		return tab, nil
	}
	ts := s.scopedTabs(b, scope)
	if tab == 0 {
		if session != "" {
			for i := len(ts) - 1; i >= 0; i-- {
				if ts[i].Session == session && ts[i].Gone == "" {
					return ts[i].ID, nil
				}
			}
		}
		if len(ts) == 0 {
			return 0, fmt.Errorf("%w: %s", errNoPageHere, i18n.S("这个项目的浏览器还没开页面：先 open", "This project's browser has no page open: open one first"))
		}
		return ts[len(ts)-1].ID, nil
	}
	for _, t := range ts {
		if t.ID == tab {
			return tab, nil
		}
	}
	return 0, fmt.Errorf("%w: %s: %d", errNoPageHere, i18n.S("这个项目里没有这个页面（每个项目只能看到、操作自己的页面）", "No such page in this project (each project sees and drives only its own pages)"), tab)
}
