package server

import (
	"encoding/json"
	"sync"
)

// A Browser pane closed closes its page too (asked for): left open, pages piled up out of sight, each a whole
// renderer, and a video in one played on with no pane to stop it from. Watched on every layout save (layout.json's
// OnSet, cmd/gerdr/main.go), whatever closed the pane (⌘W, a closed tab, another device, an agent's layout call), not
// at each way to close one. Kept open:
//   - a page another pane still shows (moved, or two panes on one page);
//   - a page whose pane only switched to another page (the pane is still there): it stays in the page list;
//   - a page an agent opened (gerdr browser open) while that agent's session is still there: it may be driving it.
type browserPanes struct {
	mu     sync.Mutex
	pages  map[string]int // Browser pane id → the page it shows; nil until the first layout is seen
	opener map[int]string // page → the agent session that opened it
	chrome map[int]string // a page of the user's Chrome → the project of the agent that opened it (browserscope.go)
}

// notePageOpener: session opened page (POST /api/browser/open).
func (s *Server) notePageOpener(page int, session string) {
	if session == "" {
		return
	}
	s.bpanes.mu.Lock()
	if s.bpanes.opener == nil {
		s.bpanes.opener = map[int]string{}
	}
	s.bpanes.opener[page] = session
	s.bpanes.mu.Unlock()
}

// LayoutSaved closes the pages of the Browser panes that are gone from the layout (see browserPanes).
func (s *Server) LayoutSaved(raw json.RawMessage) {
	gone := s.bpanes.seen(raw, func(sess string) bool {
		if s.Hub == nil {
			return false
		}
		_, err := s.Hub.Session(sess)
		return err == nil
	})
	if s.Browser == nil {
		return
	}
	for _, page := range gone {
		go func() {
			if err := s.Browser.Close(page, false); err == nil && s.Log != nil {
				s.Log.Info("browser page closed with its pane", "tab", page)
			}
		}()
	}
}

// seen takes a saved layout and returns the pages to close: their pane is gone, no pane shows them, and no live
// agent session (alive) opened them.
func (bp *browserPanes) seen(raw json.RawMessage, alive func(session string) bool) []int {
	var l node
	if json.Unmarshal(raw, &l) != nil {
		return nil
	}
	now := map[string]int{}
	shown := map[int]bool{}
	for _, w := range nodes(l["workspaces"]) {
		for _, t := range tabsOf(w) {
			for _, p := range leaves(asNode(t["root"])) {
				if b := asNode(p["browser"]); b != nil {
					page, _ := b["tab"].(float64)
					now[str(p, "id")] = int(page)
					shown[int(page)] = true
				}
			}
		}
	}
	bp.mu.Lock()
	defer bp.mu.Unlock()
	prev := bp.pages
	bp.pages = now
	var gone []int
	for pane, page := range prev {
		if _, still := now[pane]; still || page == 0 || shown[page] {
			continue
		}
		if sess := bp.opener[page]; sess != "" && alive(sess) {
			continue // its agent may be driving it
		}
		delete(bp.opener, page)
		gone = append(gone, page)
	}
	return gone
}
