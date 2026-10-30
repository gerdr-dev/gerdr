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
	sessions, _ := paneSpaces(raw)
	return sessions
}

// TermSpaces maps every terminal shown in a pane (its id, which the shell gets as GERDR_TERM) to the space holding it.
func TermSpaces(raw json.RawMessage) map[string]string {
	_, terms := paneSpaces(raw)
	return terms
}

func paneSpaces(raw json.RawMessage) (sessions, terms map[string]string) {
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
	sessions, terms = map[string]string{}, map[string]string{}
	var walk func(n json.RawMessage, space string)
	walk = func(n json.RawMessage, space string) {
		var x struct {
			Type      string `json:"type"`
			SessionID string `json:"sessionId"`
			Term      *struct {
				ID string `json:"id"`
			} `json:"term"`
			A json.RawMessage `json:"a"`
			B json.RawMessage `json:"b"`
		}
		_ = json.Unmarshal(n, &x)
		if x.SessionID != "" {
			if _, seen := sessions[x.SessionID]; !seen { // shown twice: the first pane wins, like the UI's locateSession
				sessions[x.SessionID] = space
			}
		}
		if x.Term != nil && x.Term.ID != "" {
			terms[x.Term.ID] = space
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
	return sessions, terms
}

// Caller is who runs a `gerdr` command, as the CLI tells it: an explicit --project, the agent's own gerdr session (and that
// session's cwd), the terminal it's typed in (GERDR_TERM).
type Caller struct {
	Project    string `json:"project,omitempty"`
	Session    string `json:"from,omitempty"`
	SessionCwd string `json:"-"`
	Term       string `json:"term,omitempty"`
}

// ResolveSpace is the project (space) a caller is in, "" when it can't be told (a shell outside gerdr): then nothing is
// scoped. Where you are is looked up now, never fixed at start-up, because workspaces move between projects:
//
//  1. --project, when given;
//  2. the project of the pane showing the caller's session (an agent in Code);
//  3. the project whose Work directory the session runs in (the Work agent: it's in no pane);
//  4. the project of the pane showing the caller's terminal.
//
// workProject maps a directory to the project whose Work directory holds it (work.Runner.WorkProject); nil skips step 3.
func ResolveSpace(raw json.RawMessage, c Caller, workProject func(string) (string, bool)) string {
	if c.Project != "" {
		return c.Project
	}
	sessions, terms := paneSpaces(raw)
	if c.Session != "" {
		if sp, ok := sessions[c.Session]; ok {
			return sp
		}
		if workProject != nil {
			if sp, ok := workProject(c.SessionCwd); ok {
				return sp
			}
		}
	}
	if c.Term != "" {
		if sp, ok := terms[c.Term]; ok {
			return sp
		}
	}
	return ""
}

// crossSpace refuses an agent in one space acting on a session open in another. A Work agent counts as in its project
// (sessionSpace); either end being nowhere (a closed session, a caller outside gerdr) is fine.
func (s *Server) crossSpace(from, to string) error {
	if from == "" || to == "" || from == to {
		return nil
	}
	sp := SessionSpaces(s.Layout.Get())
	a, okA := s.sessionSpace(sp, from)
	b, okB := s.sessionSpace(sp, to)
	if okA && okB && a != b {
		return fmt.Errorf("它在另一个项目里：不同项目里的 agent 不能互相调用")
	}
	return nil
}

// sessionSpace: the space of the pane showing the session, else of the Work directory it runs in.
func (s *Server) sessionSpace(panes map[string]string, id string) (string, bool) {
	if sp, ok := panes[id]; ok {
		return sp, true
	}
	if s.Work == nil || s.Hub == nil {
		return "", false
	}
	if sess, err := s.Hub.Session(id); err == nil {
		return s.Work.WorkProject(sess.Summary().Cwd)
	}
	return "", false
}

// workProject is ResolveSpace's step 3 (nil when this server has no Work).
func (s *Server) workProject() func(string) (string, bool) {
	if s.Work == nil {
		return nil
	}
	return s.Work.WorkProject
}
