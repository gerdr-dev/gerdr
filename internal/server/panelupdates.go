package server

import (
	"context"
	"strings"
	"sync"
	"time"
)

// Installed panels come from git repositories that keep moving. Gerdr tells when one has moved on (the person asked
// for a reminder, 2026-11-25, and not for updates applied by themselves: an update can change how a panel works
// under them). Every few hours each panel with a remote is fetched — only fetched, the checkout stays — and
// work.Source.Behind then counts what's new. The UI asks GET /api/work/panels/updates.

const panelCheckEvery = 6 * time.Hour

func (s *Server) checkPanelUpdates() {
	// Not in the start's rush: the network may not be up yet, and nothing is urgent
	time.Sleep(2 * time.Minute)
	for {
		before := len(s.panelUpdates())
		s.fetchPanels(context.Background(), "")
		if n := len(s.panelUpdates()); n != before {
			s.Hub.Broadcast("panel_updates", map[string]int{"count": n})
		}
		time.Sleep(panelCheckEvery)
	}
}

// fetchPanels fetches the remote of every panel of project ("" = every project), a few at a time. Their errors by
// panel key (an unreachable or private repository): the rest still get checked.
func (s *Server) fetchPanels(ctx context.Context, project string) map[string]string {
	var (
		mu   sync.Mutex
		errs = map[string]string{}
		wg   sync.WaitGroup
		gate = make(chan struct{}, 4)
	)
	for _, key := range s.Work.Projects() {
		if project != "" && strings.SplitN(key, "/", 2)[0] != project {
			continue
		}
		wg.Add(1)
		go func(key string) {
			defer wg.Done()
			gate <- struct{}{}
			defer func() { <-gate }()
			if err := s.Work.FetchUpdates(ctx, key); err != nil {
				s.Log.Info("panel: checking for updates failed", "key", key, "err", err)
				mu.Lock()
				errs[key] = err.Error()
				mu.Unlock()
			}
		}(key)
	}
	wg.Wait()
	return errs
}

type panelUpdate struct {
	Key    string `json:"key"`
	Behind int    `json:"behind"`
}

// panelUpdates: the panels, of every project, with commits waiting on their remote.
func (s *Server) panelUpdates() []panelUpdate {
	out := []panelUpdate{}
	for _, key := range s.Work.Projects() {
		if src, err := s.Work.Source(key); err == nil && src.Behind > 0 {
			out = append(out, panelUpdate{key, src.Behind})
		}
	}
	return out
}
