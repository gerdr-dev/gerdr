package waits

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// State is what a source last said a thing is like: its fields (what conditions read) and a text for whoever is
// woken (the latest comments, a failed job's log), with when.
type State struct {
	Source string         `json:"source"`
	Key    string         `json:"key"`
	State  map[string]any `json:"state"`
	Detail string         `json:"detail,omitempty"`
	URL    string         `json:"url,omitempty"`
	At     int64          `json:"at"` // unix ms
}

// Wait is a session waiting until a condition on one thing's state holds. Woken once, then gone: the waiter says
// its next expectation by waiting again (the wake message shows the values to start from).
type Wait struct {
	ID      string `json:"id"`
	Source  string `json:"source"`
	Key     string `json:"key"`
	Until   string `json:"until"`
	Session string `json:"session"`
	// Note: why it waits, sent along when it is woken
	Note    string `json:"note,omitempty"`
	Created int64  `json:"created"`
}

const (
	maxKey    = 500
	maxDetail = 20_000
	maxState  = 64 << 10
)

// Store keeps the sources' latest states (<root>/state/source-states.json) and the sessions' waits
// (<root>/state/waits.json). This machine's, like event watches.
type Store struct {
	statesPath, waitsPath string

	mu     sync.Mutex
	states map[string]State // by source + "\x00" + key
	waits  []Wait

	lmu       sync.Mutex
	onChange  []func() // waits added / removed: the subscription list changed
	lastSaved time.Time
}

func sk(source, key string) string { return source + "\x00" + key }

// Open reads both files (missing = empty).
func Open(root string) (*Store, error) {
	s := &Store{statesPath: filepath.Join(root, "state", "source-states.json"), waitsPath: filepath.Join(root, "state", "waits.json"),
		states: map[string]State{}}
	var states []State
	if err := readJSON(s.statesPath, &states); err != nil {
		return nil, err
	}
	for _, x := range states {
		s.states[sk(x.Source, x.Key)] = x
	}
	if err := readJSON(s.waitsPath, &s.waits); err != nil {
		return nil, err
	}
	return s, nil
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(v, "", "  ")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// CheckKey: a wait's key names one thing.
func CheckKey(source, key string) error {
	if source == "" || strings.ContainsAny(source, "/ ") || len(source) > 64 {
		return fmt.Errorf("source %q: a name like gitlab", source)
	}
	if key == "" || strings.Contains(key, "*") || len(key) > maxKey {
		return fmt.Errorf("key %q: one thing (no *), as the source names it", key)
	}
	return nil
}

// SetState records what a source says now. Kept in memory at once; on disk at most every few seconds (a poller
// reporting 30 MRs a minute shouldn't mean 30 writes), and always for a key seen for the first time.
func (s *Store) SetState(st State) error {
	if err := CheckKey(st.Source, st.Key); err != nil {
		return err
	}
	if st.State == nil {
		return errors.New("state: no fields")
	}
	if b, _ := json.Marshal(st.State); len(b) > maxState {
		return fmt.Errorf("state over %d KB", maxState>>10)
	}
	if len(st.Detail) > maxDetail {
		st.Detail = st.Detail[:maxDetail]
	}
	if st.At == 0 {
		st.At = time.Now().UnixMilli()
	}
	s.mu.Lock()
	_, known := s.states[sk(st.Source, st.Key)]
	s.states[sk(st.Source, st.Key)] = st
	save := !known || time.Since(s.lastSaved) > 5*time.Second
	var list []State
	if save {
		s.lastSaved = time.Now()
		list = s.stateListLocked()
	}
	s.mu.Unlock()
	if save {
		return writeJSON(s.statesPath, list)
	}
	return nil
}

func (s *Store) stateListLocked() []State {
	out := make([]State, 0, len(s.states))
	for _, x := range s.states {
		out = append(out, x)
	}
	sort.Slice(out, func(i, j int) bool { return sk(out[i].Source, out[i].Key) < sk(out[j].Source, out[j].Key) })
	return out
}

// Flush writes the states now (on exit).
func (s *Store) Flush() error {
	s.mu.Lock()
	list := s.stateListLocked()
	s.mu.Unlock()
	return writeJSON(s.statesPath, list)
}

// StateOf: the latest state of one thing.
func (s *Store) StateOf(source, key string) (State, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.states[sk(source, key)]
	return st, ok
}

// Forget drops states nobody waits on any more (keep says which are still wanted), so the file doesn't grow forever.
func (s *Store) Forget(keep func(source, key string) bool) {
	s.mu.Lock()
	changed := false
	for k, st := range s.states {
		if !keep(st.Source, st.Key) {
			delete(s.states, k)
			changed = true
		}
	}
	var list []State
	if changed {
		list = s.stateListLocked()
	}
	s.mu.Unlock()
	if changed {
		_ = writeJSON(s.statesPath, list)
	}
}

// Add records a session's wait (its condition must parse).
func (s *Store) Add(w Wait) (Wait, error) { return s.AddWith(w, nil) }

// AddWith is Add, calling before(id) once the wait has its id and before anyone can see it.
func (s *Store) AddWith(w Wait, before func(id string)) (Wait, error) {
	if w.Session == "" {
		return w, errors.New("wait: which session")
	}
	if err := CheckKey(w.Source, w.Key); err != nil {
		return w, err
	}
	if _, err := Parse(w.Until); err != nil {
		return w, err
	}
	w.ID = "u-" + randID()
	w.Created = time.Now().UnixMilli()
	if before != nil {
		before(w.ID)
	}
	s.mu.Lock()
	s.waits = append(s.waits, w)
	err := writeJSON(s.waitsPath, s.waits)
	s.mu.Unlock()
	s.changed()
	return w, err
}

// Remove drops one by id; false when there's none.
func (s *Store) Remove(id string) (bool, error) {
	s.mu.Lock()
	for i, w := range s.waits {
		if w.ID == id {
			s.waits = append(s.waits[:i], s.waits[i+1:]...)
			err := writeJSON(s.waitsPath, s.waits)
			s.mu.Unlock()
			s.changed()
			return true, err
		}
	}
	s.mu.Unlock()
	return false, nil
}

// List: every wait, or one session's, oldest first.
func (s *Store) List(session string) []Wait {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Wait{}
	for _, w := range s.waits {
		if session == "" || w.Session == session {
			out = append(out, w)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created < out[j].Created })
	return out
}

// Due takes out the waits on this thing whose condition holds for its latest state, and returns them with the
// state. A condition that can't be evaluated (a field the source doesn't have) stays, with its error in errs.
func (s *Store) Due(source, key string) (due []Wait, st State, errs map[string]error) {
	s.mu.Lock()
	st, ok := s.states[sk(source, key)]
	if !ok {
		s.mu.Unlock()
		return nil, st, nil
	}
	var keep []Wait
	for _, w := range s.waits {
		if w.Source != source || w.Key != key {
			keep = append(keep, w)
			continue
		}
		c, err := Parse(w.Until)
		var hit bool
		if err == nil {
			hit, err = c.Eval(st.State)
		}
		if err != nil {
			if errs == nil {
				errs = map[string]error{}
			}
			errs[w.ID] = err
		}
		if hit {
			due = append(due, w)
			continue
		}
		keep = append(keep, w)
	}
	if len(due) > 0 {
		s.waits = keep
		_ = writeJSON(s.waitsPath, s.waits)
	}
	s.mu.Unlock()
	if len(due) > 0 {
		s.changed()
	}
	return due, st, errs
}

// DropSession removes a session's waits (the session is gone).
func (s *Store) DropSession(session string) {
	s.mu.Lock()
	var keep []Wait
	for _, w := range s.waits {
		if w.Session != session {
			keep = append(keep, w)
		}
	}
	changed := len(keep) != len(s.waits)
	if changed {
		s.waits = keep
		_ = writeJSON(s.waitsPath, s.waits)
	}
	s.mu.Unlock()
	if changed {
		s.changed()
	}
}

// OnChange registers fn for every wait added or removed (the list sources poll by changed).
func (s *Store) OnChange(fn func()) {
	s.lmu.Lock()
	s.onChange = append(s.onChange, fn)
	s.lmu.Unlock()
}

func (s *Store) changed() {
	s.lmu.Lock()
	ls := append([]func(){}, s.onChange...)
	s.lmu.Unlock()
	for _, fn := range ls {
		fn()
	}
}

func randID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
