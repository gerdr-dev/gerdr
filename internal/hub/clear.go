package hub

// /clear makes the agent start a new session (new id); the old one stays on disk and can be resumed. gerdr gives the old
// conversation a record of its own: the tab's session keeps running under the new id and its log is cut down to the
// /clear turn onwards, while everything before moves into a new, detached session that holds the old agent session id,
// its title and its log. /resume then lists it like any other session.
//
// It used to be one record per tab: the record switched to the new id and remembered the old ones in Record.Cleared.
// That side list went stale whenever an old context was resumed (it became a session of its own but stayed on the list)
// and /resume showed it twice. A conversation is one agent session; keeping them one-to-one leaves nothing to sync.

// splitOffCleared runs right after s was rekeyed by /clear. prev is s's record from before the rekey.
func (h *Hub) splitOffCleared(s *Session, prev Record) {
	s.mu.Lock()
	cut := clearCut(s.events, len(s.events), s.turnMsg)
	old := append([]*Event(nil), s.events[:cut]...)
	s.events = append([]*Event(nil), s.events[cut:]...)
	rest := append([]*Event(nil), s.events...)
	s.rec.SyncedSize = 0 // that was the old agent file's size; it goes with the old conversation
	s.mu.Unlock()
	if err := s.lf.Rewrite(rest); err != nil {
		h.log.Warn("rewrite log", "err", err)
	}
	h.broadcastEvents(s.rec.ID, rest, true)
	if o := h.archive(prev, old); o != nil {
		h.broadcastSession(o)
	}
	h.saveIndex()
	h.broadcastSession(s)
}

// clearCut: where the conversation before a /clear ends, given the index of the clear notice (or the end of the log while
// the /clear turn is still running). The /clear turn itself (the command and the notice) stays with the new conversation.
func clearCut(evs []*Event, at int, turnMsg string) int {
	for i := at - 1; i >= 0; i-- {
		e := evs[i]
		if e.Kind == EvUser && !e.Steer && (turnMsg == "" || e.MessageID == turnMsg) {
			return i
		}
		if turnMsg == "" && e.Kind == EvTurnEnd {
			break // the notice came outside a turn: cut right at it
		}
	}
	return at
}

// archive makes a detached session out of rec (its agent session id, title, mode…) holding evs. Nil when there's nothing
// to keep or the agent session already has a record.
func (h *Hub) archive(rec Record, evs []*Event) *Session {
	if len(evs) == 0 || rec.AgentSessionID == "" {
		return nil
	}
	key := rec.Agent + ":" + rec.AgentSessionID
	h.mu.Lock()
	taken := h.byAgent[key] != nil
	h.mu.Unlock()
	if taken {
		return nil
	}
	rec.ID = newID()
	rec.Cleared = nil
	rec.UpdatedAt = evs[len(evs)-1].TS
	o := newSession(h, rec)
	o.events = evs
	if err := o.lf.Rewrite(evs); err != nil {
		h.log.Warn("write session log", "id", rec.ID, "err", err)
		return nil
	}
	h.mu.Lock()
	h.sessions[rec.ID] = o
	h.byAgent[key] = o
	h.mu.Unlock()
	return o
}

// migrateCleared converts indexes from the Record.Cleared days: each tab's log still holds the conversations before its
// /clears, separated by clear notices, and Cleared has their agent session ids (oldest first, the last 20). Each piece
// whose id is known becomes its own session; pieces older than the list fold into the oldest known one. An id that's
// already a session's (resumed later, so that session replayed the whole history) or that several tabs listed (the same
// context resumed and cleared again) keeps one record: the existing session, else the latest clear.
func (h *Hub) migrateCleared() {
	type piece struct {
		s        *Session
		rec      Record
		from, to int
		at       int64
	}
	var pieces []piece
	var tabs []*Session
	for _, s := range h.sessions {
		if len(s.rec.Cleared) == 0 {
			continue
		}
		tabs = append(tabs, s)
		var cuts []int // where each pre-clear conversation ends
		for i, e := range s.events {
			if e.Kind == EvNotice && e.Level == "clear" {
				cuts = append(cuts, clearCut(s.events, i, ""))
			}
		}
		// Match from the end: the last clear is the last Cleared entry
		ctxs := s.rec.Cleared
		if len(cuts) > len(ctxs) {
			cuts = cuts[len(cuts)-len(ctxs):] // older pieces than the list remembers: they fold into the first known one
		} else {
			ctxs = ctxs[len(ctxs)-len(cuts):]
		}
		from := 0
		for i, c := range ctxs {
			rec := s.rec
			rec.AgentSessionID, rec.Title, rec.CustomTitle = c.ID, c.Title, false
			rec.TitleStale, rec.TitleKey, rec.PluginTitleFor, rec.Fork, rec.SyncedSize = false, "", "", nil, 0
			pieces = append(pieces, piece{s: s, rec: rec, from: from, to: cuts[i], at: c.At})
			from = cuts[i]
		}
	}
	if len(tabs) == 0 {
		return
	}
	latest := map[string]int{}
	for i, p := range pieces {
		k := p.rec.Agent + ":" + p.rec.AgentSessionID
		if j, ok := latest[k]; !ok || p.at > pieces[j].at {
			latest[k] = i
		}
	}
	for i, p := range pieces {
		if latest[p.rec.Agent+":"+p.rec.AgentSessionID] == i {
			h.archive(p.rec, append([]*Event(nil), p.s.events[p.from:p.to]...))
		}
	}
	for _, s := range tabs {
		end := 0
		for _, p := range pieces {
			if p.s == s && p.to > end {
				end = p.to
			}
		}
		s.events = append([]*Event(nil), s.events[end:]...)
		s.rec.Cleared = nil
		if err := s.lf.Rewrite(s.events); err != nil {
			h.log.Warn("rewrite log", "id", s.rec.ID, "err", err)
		}
	}
	h.saveIndex()
}
