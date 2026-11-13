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

// seenSession: what was last told about a conversation, to tell only changes.
type seenSession struct{ status, title string }

func (s *Server) watchPanelSessions() {
	last := map[string]seenSession{}
	for _, sum := range s.Hub.List() {
		last[sum.ID] = seenSession{sum.Status, sum.Title}
	}
	for {
		sub := s.Hub.Subscribe()
		// Dropped as a slow subscriber (hub Sub.send): what changed meanwhile is caught up from the list
		for _, sum := range s.Hub.List() {
			if prev, ok := last[sum.ID]; ok {
				s.panelSessionChanged(sum, prev)
			}
			last[sum.ID] = seenSession{sum.Status, sum.Title}
		}
		for m := range sub.C() {
			switch m.T {
			case "session":
				if m.Session == nil {
					continue
				}
				prev, seen := last[m.Session.ID]
				last[m.Session.ID] = seenSession{m.Session.Status, m.Session.Title}
				if !seen {
					prev = seenSession{title: m.Session.Title}
				}
				s.panelSessionChanged(*m.Session, prev)
			case "session_removed":
				delete(last, m.ID)
			}
		}
		s.Hub.Unsubscribe(sub)
		time.Sleep(time.Second)
	}
}

// panelSessionChanged tells the conversation's panel what changed since prev: its status ("session.<status>"), or
// else its title ("session.renamed"). Renamed is for a board whose card follows its worker's auto title (the
// Superagent's tasks added without one): the auto title lands seconds after the message that prompted it, usually
// while the worker runs, and waiting for the next status change left the card on a stopgap name for a whole turn.
func (s *Server) panelSessionChanged(sum hub.Summary, prev seenSession) {
	kind := ""
	switch {
	case prev.status != sum.Status:
		kind = "session." + sum.Status
	case prev.title != sum.Title:
		kind = "session.renamed"
	default:
		return
	}
	panel := s.panelOf(sum)
	if panel == "" {
		return
	}
	data, _ := json.Marshal(map[string]any{"id": sum.ID, "status": sum.Status, "prev": prev.status, "title": sum.Title, "prevTitle": prev.title, "waiting": sum.Waiting})
	now := time.Now().UnixMilli()
	s.Work.DispatchSession(panel, ev.Event{
		ID:     fmt.Sprintf("session:%s:%d", sum.ID, now),
		Source: work.SessionSource,
		Kind:   kind,
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
