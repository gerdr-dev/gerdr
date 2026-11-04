package server

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/gerdr-dev/gerdr/internal/work"
)

// Panels' items in the inbox (ctx.inbox.set, work/inbox.go): a board's task waiting on the user, say. Kept here, not
// in each page: every device's inbox shows the same, and the phone App's background notices (appwatch.go) get a new
// one even with no page open. On disk (<dir>/inbox.json) so a restart neither loses them nor notifies them again.
//
// To windows: GET /api/inbox, and the whole list as an "inbox" message whenever it changes.

type panelInbox struct {
	mu     sync.Mutex
	loaded bool
	items  map[string][]work.InboxItem // by panel key
}

func (s *Server) inboxPath() string { return filepath.Join(s.Dir, "inbox.json") }

func (s *Server) inboxLoadLocked() {
	b := &s.inbox
	if b.loaded {
		return
	}
	b.loaded = true
	b.items = map[string][]work.InboxItem{}
	if s.Dir == "" {
		return
	}
	if data, err := os.ReadFile(s.inboxPath()); err == nil {
		_ = json.Unmarshal(data, &b.items)
		if b.items == nil {
			b.items = map[string][]work.InboxItem{}
		}
	}
}

func (s *Server) inboxSaveLocked() error {
	if s.Dir == "" {
		return nil
	}
	data, err := json.Marshal(s.inbox.items)
	if err != nil {
		return err
	}
	tmp := s.inboxPath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.inboxPath())
}

// InboxItems: every panel's items, newest first.
func (s *Server) InboxItems() []work.InboxItem {
	s.inbox.mu.Lock()
	defer s.inbox.mu.Unlock()
	s.inboxLoadLocked()
	return s.inboxAllLocked()
}

func (s *Server) inboxAllLocked() []work.InboxItem {
	out := []work.InboxItem{}
	for _, its := range s.inbox.items {
		out = append(out, its...)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At > out[j].At })
	return out
}

// SetInbox (work.Inbox): the panel's whole list now. Items it had before keep their time; new ones are told to the
// phone App; gone ones leave every inbox.
func (s *Server) SetInbox(panel string, items []work.InboxItem) error {
	if panel == "" {
		return errors.New("no panel")
	}
	s.inbox.mu.Lock()
	s.inboxLoadLocked()
	old := map[string]work.InboxItem{}
	for _, it := range s.inbox.items[panel] {
		old[it.ID] = it
	}
	now := time.Now().UnixMilli()
	var fresh []work.InboxItem
	next := make([]work.InboxItem, 0, len(items))
	for _, it := range items {
		it.Panel = panel
		if o, ok := old[it.ID]; ok {
			it.At = o.At
		} else {
			it.At = now
			fresh = append(fresh, it)
		}
		next = append(next, it)
	}
	if slices.Equal(next, s.inbox.items[panel]) {
		s.inbox.mu.Unlock()
		return nil
	}
	if len(next) == 0 {
		delete(s.inbox.items, panel)
	} else {
		s.inbox.items[panel] = next
	}
	err := s.inboxSaveLocked()
	all := s.inboxAllLocked()
	s.inbox.mu.Unlock()
	if s.Hub != nil {
		s.Hub.Broadcast("inbox", all)
	}
	for _, it := range fresh {
		s.noticePanelItem(it)
	}
	return err
}

// noticePanelItem: a new panel item as a "waiting" notice for the phone App, like a session that turned to waiting.
// Not when the conversation it's about is in a muted (do-not-disturb) group.
func (s *Server) noticePanelItem(it work.InboxItem) {
	if s.Hub == nil {
		return // no sessions to watch, no App to tell (tests)
	}
	if it.Session != "" && s.Layout != nil && MutedSessions(s.Layout.Get())[it.Session] {
		return
	}
	w := s.watch()
	w.mu.Lock()
	defer w.mu.Unlock()
	w.seq++
	w.notices = append(w.notices, appNotice{Seq: w.seq, Session: it.Session, Status: "waiting", Title: it.Title, Text: it.Body, At: it.At})
	if len(w.notices) > noticeKeep {
		w.notices = w.notices[len(w.notices)-noticeKeep:]
	}
	w.ringLocked()
}
