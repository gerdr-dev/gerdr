package hub

import (
	"errors"

	"github.com/gerdr-dev/gerdr/internal/i18n"
)

// Calling off a message that is still on its way (the composer's stop button while it shows "Sending…").
//
// A send can wait a long time before it does anything: for a stop still rolling back (waitStopping) or for the agent
// to start or resume (ensureAttached, up to the 2-minute request timeout). The UI's rule is that everything answers at
// once, so the person can call it off during that wait. Calling off doesn't interrupt the wait itself: an abandoned
// ResumeSession would leave the agent resumed with nobody knowing, and the next send would resume it again. The wait
// finishes in the background (so the next message finds the agent ready) and the message is dropped at commitSend,
// the point before anything reaches the agent or the log. Past that point CancelSend says no and the UI falls back
// to stopping the turn, which hands an untouched message back (Stop).

// ErrSendCancelled: the person called the message off before it went in.
var ErrSendCancelled = errors.New(i18n.S("已取消发送", "Sending was cancelled"))

// CancelSend calls off the message with this client id. false: too late, it is in (or going in right now).
// A message the server hasn't seen yet (the request is still uploading, or being retried over a bad network) is
// remembered, so its send is refused when it arrives.
func (s *Session) CancelSend(clientID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if done, ok := s.sendState[clientID]; ok {
		return !done
	}
	for _, d := range s.deferred {
		if d.id == clientID {
			return false
		}
	}
	s.loadLocked()
	for _, e := range s.events {
		if e.Kind == EvUser && e.MessageID == clientID {
			return false
		}
	}
	if s.sendState == nil {
		s.sendState = map[string]bool{}
	}
	// Ids called off for a send that never arrives would pile up; a few hundred is far more than can be in flight
	if len(s.sendState) > 200 {
		for id, done := range s.sendState {
			if !done {
				delete(s.sendState, id)
			}
		}
	}
	s.sendState[clientID] = false
	return true
}

// calledOff: the person called this message off (it stays called off).
func (s *Session) calledOff(clientID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	called, ok := s.sendState[clientID]
	return ok && !called
}

// commitSend marks the message as past the point of no return; false if it was called off first (the mark is
// cleared, the send gives up). SendWith clears the mark when it returns.
func (s *Session) commitSend(clientID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if called, ok := s.sendState[clientID]; ok && !called {
		delete(s.sendState, clientID)
		return false
	}
	if s.sendState == nil {
		s.sendState = map[string]bool{}
	}
	s.sendState[clientID] = true
	return true
}
