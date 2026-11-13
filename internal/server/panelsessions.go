package server

import (
	"encoding/json"
	"fmt"
	"time"

	ev "github.com/gerdr-dev/gerdr/internal/events"
	"github.com/gerdr-dev/gerdr/internal/hub"
	"github.com/gerdr-dev/gerdr/internal/work"
)

// Panels' conversations changing status, for their triggers on "gerdr/session.*" (work.DispatchSession). A board
// (the Superagent) used to learn that a worker stopped, asked or failed only from a schedule polling every minute
// when no page was open, so its inbox items and badge came up to a minute late; the hub already says the moment it
// happens. The conversation's panel is the one that started it (PanelTag), or, for one a worker handed work to, the
// panel of the conversation it reports to.

const ownerDepth = 4 // reportTo hops followed to find a panel: worker → helper → helper's helper…

func (s *Server) watchPanelSessions() {
	last := map[string]string{}
	for _, sum := range s.Hub.List() {
		last[sum.ID] = sum.Status
	}
	for {
		sub := s.Hub.Subscribe()
		// Dropped as a slow subscriber (hub Sub.send): what changed meanwhile is caught up from the list
		for _, sum := range s.Hub.List() {
			if prev, ok := last[sum.ID]; ok && prev != sum.Status {
				s.panelSessionChanged(sum, prev)
			}
			last[sum.ID] = sum.Status
		}
		for m := range sub.C() {
			switch m.T {
			case "session":
				if m.Session == nil {
					continue
				}
				prev, seen := last[m.Session.ID]
				last[m.Session.ID] = m.Session.Status
				if !seen || prev != m.Session.Status {
					s.panelSessionChanged(*m.Session, prev)
				}
			case "session_removed":
				delete(last, m.ID)
			}
		}
		s.Hub.Unsubscribe(sub)
		time.Sleep(time.Second)
	}
}

func (s *Server) panelSessionChanged(sum hub.Summary, prev string) {
	panel := s.panelOf(sum)
	if panel == "" {
		return
	}
	data, _ := json.Marshal(map[string]any{"id": sum.ID, "status": sum.Status, "prev": prev, "title": sum.Title, "waiting": sum.Waiting})
	now := time.Now().UnixMilli()
	s.Work.DispatchSession(panel, ev.Event{
		ID:     fmt.Sprintf("session:%s:%d", sum.ID, now),
		Source: work.SessionSource,
		Kind:   "session." + sum.Status,
		Key:    sum.ID,
		Title:  sum.Title,
		Data:   data,
		At:     now,
	})
}

// panelOf: the panel a conversation works for, "" for none.
func (s *Server) panelOf(sum hub.Summary) string {
	for range ownerDepth {
		if p := sum.Tags[PanelTag]; p != "" {
			return p
		}
		if sum.ReportTo == "" {
			return ""
		}
		up, err := s.Hub.Session(sum.ReportTo)
		if err != nil {
			return ""
		}
		sum = up.Summary()
	}
	return ""
}
