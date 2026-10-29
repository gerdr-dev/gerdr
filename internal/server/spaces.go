package server

import (
	"encoding/json"
	"fmt"
)

// DefaultSpace is the session a workspace without a "space" field belongs to (ui/src/lib/layout.ts DEFAULT_SPACE).
const DefaultSpace = "default"

// SessionSpaces maps every gerdr session shown in a pane of the layout to the herdr-style session ("space" in the layout,
// see ui/src/lib/layout.ts Space) whose workspace holds it. Sessions with no pane (closed ones) aren't in the map: they belong
// to no space until someone opens them again, and then they belong to wherever they were opened.
//
// Agents in different spaces must not reach each other: the `gerdr session` CLI only lists / resolves sessions in the caller's
// space, and the handlers that know both ends (send's from, open's near, close's from) refuse to cross. Like herdr's sessions
// this keeps well-behaved agents apart; it isn't a security boundary (an agent holding the token can call the API directly).
//
// raw is layout.json as stored, or wrapped in {"data": …} as some responses carry it.
func SessionSpaces(raw json.RawMessage) map[string]string {
	type layout struct {
		Workspaces []struct {
			Space string `json:"space"`
			Tabs  []struct {
				Root json.RawMessage `json:"root"`
			} `json:"tabs"`
		} `json:"workspaces"`
	}
	var l struct {
		layout
		Data layout `json:"data"`
	}
	_ = json.Unmarshal(raw, &l)
	out := map[string]string{}
	var walk func(n json.RawMessage, space string)
	walk = func(n json.RawMessage, space string) {
		var x struct {
			Type      string          `json:"type"`
			SessionID string          `json:"sessionId"`
			A         json.RawMessage `json:"a"`
			B         json.RawMessage `json:"b"`
		}
		_ = json.Unmarshal(n, &x)
		if x.SessionID != "" {
			if _, seen := out[x.SessionID]; !seen { // shown twice: the first pane wins, like the UI's locateSession
				out[x.SessionID] = space
			}
		}
		if x.Type == "split" {
			walk(x.A, space)
			walk(x.B, space)
		}
	}
	for _, w := range append(l.Data.Workspaces, l.Workspaces...) {
		sp := w.Space
		if sp == "" {
			sp = DefaultSpace
		}
		for _, t := range w.Tabs {
			walk(t.Root, sp)
		}
	}
	return out
}

// crossSpace refuses an agent in one space acting on a session open in another. Either end not being in the layout
// (a closed session, a caller outside gerdr) is fine.
func (s *Server) crossSpace(from, to string) error {
	if from == "" || to == "" || from == to {
		return nil
	}
	sp := SessionSpaces(s.Layout.Get())
	a, okA := sp[from]
	b, okB := sp[to]
	if okA && okB && a != b {
		return fmt.Errorf("它在另一个 session 里：不同 session 里的 agent 不能互相调用")
	}
	return nil
}
