package server

import (
	"encoding/json"
	"net/http"
	"sync"
)

// Unread: the sessions that finished (or failed) while nobody was looking at them, one set for every device. It used to
// live in each browser's localStorage, so a turn read on the phone stayed "unread" on the desktop. Which device decides
// is unchanged: whichever device isn't showing a session when its turn ends marks it (ui App.tsx useAttention), and one
// that is showing it, in a window in front, clears it again (Panes / OverviewTile). That is why the API sets or clears
// one id rather than taking the whole set: two devices writing their own copy of the set would undo each other.
//
// Kept in unread.json (Server.Unread) so a restart doesn't forget it; ids of deleted sessions are dropped on each write.

var unreadMu sync.Mutex

// unreadSet reads the stored set (id → true). Missing store (tests) or a broken file: empty.
func (s *Server) unreadSet() map[string]bool {
	out := map[string]bool{}
	if s.Unread == nil {
		return out
	}
	_ = json.Unmarshal(s.Unread.Get(), &out)
	for k, v := range out {
		if !v {
			delete(out, k)
		}
	}
	return out
}

// POST /api/unread {id, unread, client}: mark one session unread or read, then tell every client the whole set.
func (s *Server) postUnread(w http.ResponseWriter, r *http.Request) {
	var b struct {
		ID     string `json:"id"`
		Unread bool   `json:"unread"`
		Client string `json:"client"`
	}
	if err := decode(r, &b); err != nil || b.ID == "" {
		httpError(w, 400, "need id")
		return
	}
	if s.Unread == nil {
		httpError(w, 503, "no unread store")
		return
	}
	unreadMu.Lock()
	set := s.unreadSet()
	if set[b.ID] == b.Unread {
		unreadMu.Unlock()
		writeJSON(w, map[string]any{"ok": true})
		return
	}
	if b.Unread {
		set[b.ID] = true
	} else {
		delete(set, b.ID)
	}
	if s.Hub != nil {
		live := map[string]bool{}
		for _, x := range s.Hub.List() {
			live[x.ID] = true
		}
		for id := range set {
			if !live[id] {
				delete(set, id)
			}
		}
	}
	data, _ := json.Marshal(set)
	err := s.Unread.Set(data)
	// Broadcast under the lock: clients apply every frame (their own included), so frames must go out in the order the
	// changes were made, or an older set could land last
	if err == nil && s.Hub != nil {
		s.Hub.Broadcast("unread", map[string]any{"data": set, "client": b.Client})
	}
	unreadMu.Unlock()
	if err != nil {
		httpError(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}
