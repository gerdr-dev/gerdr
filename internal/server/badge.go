package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/gerdr-dev/gerdr/internal/work"
)

// Panels' badges on their rail buttons (ctx.badge, `gerdr panel badge`, work/badge.go): a dot or a number for
// "something new here". Kept here like the inbox (inbox.go) so every window shows the same and a schedule can set one
// with no page open; on disk (<dir>/badges.json) so a restart keeps what the user hasn't looked at yet.
//
// To windows: GET /api/work/badges, and the whole map as a "badges" message whenever it changes. A window clears a
// panel's badge (POST /api/work/badge, n < 0) when the user is looking at that panel.

type panelBadges struct {
	mu     sync.Mutex
	loaded bool
	n      map[string]int // by panel key: >0 a number, 0 a dot
}

func (s *Server) badgesPath() string { return filepath.Join(s.Dir, "badges.json") }

func (s *Server) badgesLoadLocked() {
	b := &s.badges
	if b.loaded {
		return
	}
	b.loaded = true
	b.n = map[string]int{}
	if s.Dir == "" {
		return
	}
	if data, err := os.ReadFile(s.badgesPath()); err == nil {
		_ = json.Unmarshal(data, &b.n)
		if b.n == nil {
			b.n = map[string]int{}
		}
	}
}

func (s *Server) badgesSaveLocked() error {
	if s.Dir == "" {
		return nil
	}
	data, err := json.Marshal(s.badges.n)
	if err != nil {
		return err
	}
	tmp := s.badgesPath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.badgesPath())
}

// Badges: every panel's badge now.
func (s *Server) Badges() map[string]int {
	s.badges.mu.Lock()
	defer s.badges.mu.Unlock()
	s.badgesLoadLocked()
	out := make(map[string]int, len(s.badges.n))
	for k, v := range s.badges.n {
		out[k] = v
	}
	return out
}

// SetBadge (work.Badges): n > 0 a number, 0 a dot, < 0 none; add: n on top of what it shows.
func (s *Server) SetBadge(panel string, n int, add bool) error {
	if panel == "" {
		return errors.New("no panel")
	}
	s.badges.mu.Lock()
	s.badgesLoadLocked()
	cur, had := s.badges.n[panel]
	next, has := n, n >= 0
	if add {
		next, has = min(cur+max(n, 0), work.BadgeMax), true
	}
	if has == had && (!has || next == cur) {
		s.badges.mu.Unlock()
		return nil
	}
	if has {
		s.badges.n[panel] = next
	} else {
		delete(s.badges.n, panel)
	}
	err := s.badgesSaveLocked()
	all := make(map[string]int, len(s.badges.n))
	for k, v := range s.badges.n {
		all[k] = v
	}
	s.badges.mu.Unlock()
	if s.Hub != nil {
		s.Hub.Broadcast("badges", all)
	}
	return err
}

func (s *Server) badgeRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/work/badges", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, s.Badges()) })
	// { key, n, add? }: n as in SetBadge (omitted = a dot); the CLI and a window looking at the panel (n: -1)
	mux.HandleFunc("POST /api/work/badge", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Key string `json:"key"`
			N   *int   `json:"n"`
			Add bool   `json:"add"`
		}
		if err := decode(r, &b); err != nil {
			httpError(w, 400, err.Error())
			return
		}
		n := 0
		if b.N != nil {
			n = min(*b.N, work.BadgeMax)
		}
		if err := s.SetBadge(b.Key, n, b.Add); err != nil {
			httpError(w, 400, err.Error())
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	})
}
