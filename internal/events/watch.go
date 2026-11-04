package events

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Watch: a conversation subscribed to events (`gerdr event watch`): each matching one is sent to it as a message.
// Panels / Work subscribe with triggers/*.json in their own folder, add-ons in addon.json / their UI; a watch is for
// a session, which has no folder of its own. Kept in <root>/state/event-watches.json: this machine's, not a project's.
type Watch struct {
	ID      string `json:"id"`
	On      string `json:"on"`            // a pattern (Match)
	Key     string `json:"key,omitempty"` // a key pattern (MatchKey)
	Session string `json:"session"`
	// Once: removed after the first event it delivers ("tell me when this pipeline finishes").
	Once bool `json:"once,omitempty"`
	// Note: why it was set up, sent along with each event (what the watcher wanted to do about it).
	Note    string `json:"note,omitempty"`
	Created int64  `json:"created"`
}

type watchFile struct {
	path string
	mu   sync.Mutex
	list []Watch
}

func (w *watchFile) load() error {
	b, err := os.ReadFile(w.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(b, &w.list)
}

func (w *watchFile) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(w.path), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(w.list, "", "  ")
	tmp := w.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, w.path)
}

// AddWatch subscribes a session.
func (s *Store) AddWatch(x Watch) (Watch, error) {
	if x.Session == "" {
		return x, errors.New("watch: which session (session)")
	}
	if err := CheckPattern(x.On); err != nil {
		return x, err
	}
	x.ID = "w-" + randID()[:8]
	x.Created = time.Now().UnixMilli()
	w := s.watches
	w.mu.Lock()
	defer w.mu.Unlock()
	w.list = append(w.list, x)
	return x, w.saveLocked()
}

// RemoveWatch drops one by id; false when there's none.
func (s *Store) RemoveWatch(id string) (bool, error) {
	w := s.watches
	w.mu.Lock()
	defer w.mu.Unlock()
	for i, x := range w.list {
		if x.ID == id {
			w.list = append(w.list[:i], w.list[i+1:]...)
			return true, w.saveLocked()
		}
	}
	return false, nil
}

// Watches: every watch, or one session's.
func (s *Store) Watches(session string) []Watch {
	w := s.watches
	w.mu.Lock()
	defer w.mu.Unlock()
	out := []Watch{}
	for _, x := range w.list {
		if session == "" || x.Session == session {
			out = append(out, x)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created < out[j].Created })
	return out
}

// Matching: the watches an event goes to. Once-watches are taken out as they're returned, so a burst of matching
// events delivers one of them, not each.
func (s *Store) Matching(e Event) []Watch {
	w := s.watches
	w.mu.Lock()
	defer w.mu.Unlock()
	var out, keep []Watch
	changed := false
	for _, x := range w.list {
		if Match(x.On, e) && MatchKey(x.Key, e.Key) {
			out = append(out, x)
			if x.Once {
				changed = true
				continue
			}
		}
		keep = append(keep, x)
	}
	if changed {
		w.list = keep
		_ = w.saveLocked()
	}
	return out
}

// DropSession removes a session's watches (the session is gone).
func (s *Store) DropSession(session string) {
	w := s.watches
	w.mu.Lock()
	defer w.mu.Unlock()
	keep := w.list[:0]
	for _, x := range w.list {
		if x.Session != session {
			keep = append(keep, x)
		}
	}
	if len(keep) != len(w.list) {
		w.list = keep
		_ = w.saveLocked()
	}
}
