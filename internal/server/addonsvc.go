package server

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/gerdr-dev/gerdr/internal/hub"
	"github.com/gerdr-dev/gerdr/internal/addons"
	"github.com/gerdr-dev/gerdr/internal/version"
)

// The bridge between the hub and add-on services (addons/service.go): what the sessions are doing, sent to every
// service's stdin.
//
// Only when it changes, and only the fields worth reacting to. The hub broadcasts a session on every token of a
// running turn; a service that forwards to a device must not be woken for each one. The wristband's own poller learned
// that the hard way: a few KB re-sent every few seconds made the band reboot after a couple of hours.

// svcSession is one open session as services see it. Same shape and meaning as `gerdr session list`.
type svcSession struct {
	ID      string `json:"id"`
	Agent   string `json:"agent"`
	Cwd     string `json:"cwd"`
	Title   string `json:"title"`
	Status  string `json:"status"`            // idle / running / waiting / error / starting
	Waiting string `json:"waiting,omitempty"` // what it waits for, one line ("审批：Write x.txt")
	Error   string `json:"error,omitempty"`
	// Since: when Status last changed (unix ms). Sort by it to put what just happened first; unlike updatedAt it
	// doesn't move while a turn streams.
	Since int64 `json:"since"`
}

type addonBridge struct {
	s *Server

	mu    sync.Mutex
	since map[string]svcSince
	last  []byte // the last sessions line broadcast
}

type svcSince struct {
	status string
	at     int64
}

func (b *addonBridge) sessions() []svcSession {
	spaces := SessionSpaces(b.s.Layout.Get())
	out := []svcSession{}
	seen := map[string]bool{}
	for _, sum := range b.s.Hub.List() {
		if _, open := spaces[sum.ID]; !open {
			continue // closed (no pane shows it): not listed, like `gerdr session list`
		}
		seen[sum.ID] = true
		c, ok := b.since[sum.ID]
		if !ok || c.status != sum.Status {
			at := time.Now().UnixMilli()
			if !ok && sum.UpdatedAt > 0 {
				at = sum.UpdatedAt
			}
			c = svcSince{sum.Status, at}
			b.since[sum.ID] = c
		}
		v := svcSession{ID: sum.ID, Agent: sum.Agent, Cwd: sum.Cwd, Title: sum.Title, Status: sum.Status, Error: sum.Error, Since: c.at}
		if sum.Pending > 0 {
			v.Waiting = sum.Waiting
		}
		out = append(out, v)
	}
	for id := range b.since {
		if !seen[id] {
			delete(b.since, id)
		}
	}
	return out
}

// line: the current sessions line. Called from the greeting (service goroutines) and the pump.
func (b *addonBridge) line() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	l, _ := json.Marshal(map[string]any{"type": "sessions", "sessions": b.sessions()})
	return l
}

// changed: the current line, and whether it differs from what was broadcast last.
func (b *addonBridge) changed() ([]byte, bool) {
	l := b.line()
	b.mu.Lock()
	defer b.mu.Unlock()
	if string(l) == string(b.last) {
		return nil, false
	}
	b.last = l
	return l, true
}

// RunAddonServices wires s.AddonSvc to the hub and the UI, starts the services, and keeps their session list
// current until ctx ends. Call once the HTTP server is listening (a service may run `gerdr session …` right away).
func (s *Server) RunAddonServices(ctx context.Context) {
	ps := s.AddonSvc
	b := &addonBridge{s: s, since: map[string]svcSince{}}
	ps.Greeting = func(id string) [][]byte {
		hello, _ := json.Marshal(map[string]any{"type": "hello", "version": version.Version, "addon": id})
		return [][]byte{hello, b.line()}
	}
	ps.OnStatus = func(id string, st addons.ServiceStatus) {
		s.Hub.Broadcast("addon_service", map[string]any{"id": id, "service": st})
	}
	ps.OnMessage = func(id string, data json.RawMessage) {
		s.Hub.Broadcast("addon_message", map[string]any{"id": id, "data": data})
	}
	ps.Sync()
	go func() {
		for ctx.Err() == nil {
			sub := s.Hub.Subscribe()
			pumpSessions(ctx, sub, func() {
				if l, ok := b.changed(); ok {
					ps.Broadcast(l)
				}
			})
			s.Hub.Unsubscribe(sub)
		}
	}()
}

// pumpSessions: a session or the layout changed → flush, at most every 300ms. Returns when the subscription is
// dropped (the hub closes a subscriber that falls behind) or ctx ends.
func pumpSessions(ctx context.Context, sub *hub.Sub, flush func()) {
	var tick <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case m, ok := <-sub.C():
			if !ok {
				return
			}
			switch m.T {
			case "session", "session_removed", "layout":
				if tick == nil {
					tick = time.After(300 * time.Millisecond)
				}
			}
		case <-tick:
			tick = nil
			flush()
		}
	}
}

