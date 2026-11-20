package desktop

import (
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"sort"
	"sync"

	"github.com/gerdr-dev/gerdr/internal/desktop/el"
)

// Windows that show one page of a project's browser on their own (the UI's ?browser=<tab>, ui/src/core/SessionBrowser.tsx
// BrowserWindow): a page moved out of a panel's browser column. A page is in one place: while its window is open the
// column shows a card instead of it ("in its own window": show the window / put it back), opening it again brings
// that window forward instead of a second one, and closing the window puts the page back in the column (asked for:
// the window and the column both showing it, one as pictures, and a new window at each click, read as broken).
//
// The window's page says which page it shows (browserWindowTab, again when it follows the page to a popup); every
// window hears the list (the gerdr:browserwindows event) whenever it changes, and when it asks (browserWindows).
type browserWins struct {
	mu   sync.Mutex
	tabs map[string]int // window id → the page it shows
}

func (s *shell) browserWinMessage(w *el.Window, kind string, m map[string]any) {
	tab := 0
	if n, ok := m["tab"].(float64); ok {
		tab = int(n)
	}
	switch kind {
	case "browserWindowTab": // a browser window's page: the page it shows (0: none)
		if w == nil {
			return
		}
		s.bwins.mu.Lock()
		if tab > 0 {
			s.bwins.tabs[w.ID()] = tab
		} else {
			delete(s.bwins.tabs, w.ID())
		}
		s.bwins.mu.Unlock()
	case "browserWindow": // show page tab in a window of its own: the one it is in, or a new one
		if tab <= 0 {
			return
		}
		if bw := s.browserWindowOf(tab); bw != nil {
			bw.Focus()
			return
		}
		space, _ := m["space"].(string)
		if err := s.app.NewWindow(fmt.Sprintf("/%s?browser=%d", url.PathEscape(space), tab)); err != nil {
			log.Printf("browser window: %v", err)
		}
		return // the new window's page says so once it is up (browserWindowTab)
	case "browserWindowClose": // put page tab back: its window closes
		if bw := s.browserWindowOf(tab); bw != nil {
			bw.Close()
		}
		return // its close (browserWindowClosed) tells everyone
	case "browserWindows": // a window just loaded: what is out
	}
	s.tellBrowserWindows()
}

func (s *shell) browserWindowOf(tab int) *el.Window {
	s.bwins.mu.Lock()
	id := ""
	for wid, t := range s.bwins.tabs {
		if t == tab {
			id = wid
		}
	}
	s.bwins.mu.Unlock()
	if id == "" {
		return nil
	}
	for _, w := range s.app.Windows() {
		if w.ID() == id {
			return w
		}
	}
	return nil
}

// browserWindowClosed: a window is gone; if it showed a page, the page is back where it came from.
func (s *shell) browserWindowClosed(w *el.Window) {
	s.bwins.mu.Lock()
	_, was := s.bwins.tabs[w.ID()]
	delete(s.bwins.tabs, w.ID())
	s.bwins.mu.Unlock()
	if was {
		s.tellBrowserWindows()
	}
}

// tellBrowserWindows: every window hears which pages are in windows of their own.
func (s *shell) tellBrowserWindows() {
	s.bwins.mu.Lock()
	tabs := []int{}
	for _, t := range s.bwins.tabs {
		tabs = append(tabs, t)
	}
	s.bwins.mu.Unlock()
	sort.Ints(tabs)
	raw, _ := json.Marshal(tabs)
	for _, w := range s.app.Windows() {
		_ = w.Eval(`window.dispatchEvent(new CustomEvent("gerdr:browserwindows", {detail: ` + string(raw) + `}))`)
	}
}
