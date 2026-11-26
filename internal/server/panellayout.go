package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/gerdr-dev/gerdr/internal/config"
	"github.com/gerdr-dev/gerdr/internal/work"
)

// A panel's own panes (work/panellayout.go): each such panel keeps a layout of its own in its directory, apart from
// Code mode's layout.json. The UI shows one or the other (ui/src/lib/hostView.ts). Whatever on the server asks "which
// panes are there" (which project a session is in, do-not-disturb, Browser panes still shown, `gerdr pane …`) reads
// all of them through LayoutAll. docs/dev/HOSTED-CODE.md

var panelLayoutMu sync.Mutex

func (s *Server) panelLayoutStore(key string) (*config.Store, error) {
	p, err := s.Work.PanelLayoutPath(key)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return nil, err
	}
	return config.NewStore(p, `null`), nil
}

// PanelLayouts: every panel's own layout, by key.
func (s *Server) PanelLayouts() map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	if s.Work == nil {
		return out
	}
	for key, p := range s.Work.PanelLayouts() {
		if b, err := os.ReadFile(p); err == nil && json.Valid(b) {
			out[key] = b
		}
	}
	return out
}

// LayoutAll is Code's layout with every panel's workspaces added (each marked with its panel in "host", and its
// project in "space"), for what reads the panes rather than shows them.
func (s *Server) LayoutAll() json.RawMessage {
	if s.Layout == nil {
		return json.RawMessage(`null`)
	}
	raw := s.Layout.Get()
	panels := s.PanelLayouts()
	if len(panels) == 0 {
		return raw
	}
	var l map[string]any
	if json.Unmarshal(raw, &l) != nil || l == nil {
		l = map[string]any{"version": 1}
	}
	wss, _ := l["workspaces"].([]any)
	for key, praw := range panels {
		var pl map[string]any
		if json.Unmarshal(praw, &pl) != nil || pl == nil {
			continue
		}
		project, _ := work.SplitKey(key)
		for _, w := range nodes(pl["workspaces"]) {
			w["host"] = key
			if project != work.DefaultProject {
				w["space"] = project
			} else {
				delete(w, "space")
			}
			wss = append(wss, w)
		}
	}
	l["workspaces"] = wss
	out, _ := json.Marshal(l)
	return out
}

// setLayoutAll stores a LayoutAll that was changed (a `gerdr pane …` call): each workspace back where it came from,
// Code's in layout.json and a panel's in its own file. Only the layouts that changed are written.
func (s *Server) setLayoutAll(all json.RawMessage) error {
	var l map[string]any
	if err := json.Unmarshal(all, &l); err != nil {
		return err
	}
	byHost := map[string][]any{}
	var code []any
	for _, w := range nodes(l["workspaces"]) {
		if h, _ := w["host"].(string); h != "" {
			delete(w, "host")
			byHost[h] = append(byHost[h], w)
		} else {
			code = append(code, w)
		}
	}
	// Code's: as it was, but its own workspaces
	l["workspaces"] = code
	if cb, err := json.Marshal(l); err == nil && !sameJSON(cb, s.Layout.Get()) {
		if err := s.Layout.Set(cb); err != nil {
			return err
		}
		s.Hub.Broadcast("layout", map[string]any{"data": s.Layout.Get(), "client": "gerdr-cli"})
	}
	panels := s.PanelLayouts()
	for key, wss := range byHost {
		var pl map[string]any
		_ = json.Unmarshal(panels[key], &pl)
		if pl == nil {
			pl = map[string]any{"version": 1}
		}
		pl["workspaces"] = wss
		if !hasWs(wss, pl["active"]) {
			pl["active"] = wss[0].(map[string]any)["id"]
		}
		b, _ := json.Marshal(pl)
		if sameJSON(b, panels[key]) {
			continue
		}
		if err := s.putPanelLayout(key, b, ""); err != nil {
			return err
		}
	}
	return nil
}

func hasWs(wss []any, id any) bool {
	for _, w := range wss {
		if m, ok := w.(map[string]any); ok && m["id"] == id {
			return true
		}
	}
	return false
}

func sameJSON(a, b json.RawMessage) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	xa, _ := json.Marshal(x)
	ya, _ := json.Marshal(y)
	return string(xa) == string(ya)
}

func (s *Server) putPanelLayout(key string, data json.RawMessage, client string) error {
	panelLayoutMu.Lock()
	st, err := s.panelLayoutStore(key)
	if err == nil {
		err = st.Set(data)
	}
	panelLayoutMu.Unlock()
	if err != nil {
		return err
	}
	key = s.Work.Canon(key)
	s.Hub.Broadcast("panel_layout", map[string]any{"key": key, "data": st.Get(), "client": client})
	s.LayoutSaved(s.LayoutAll())
	return nil
}

func (s *Server) getPanelLayouts(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.PanelLayouts())
}

func (s *Server) putPanelLayoutAPI(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Key    string          `json:"key"`
		Data   json.RawMessage `json:"data"`
		Client string          `json:"client"`
	}
	if err := decode(r, &b); err != nil {
		httpError(w, 400, err.Error())
		return
	}
	if s.Work == nil || b.Key == "" {
		httpError(w, 400, "no panel")
		return
	}
	if err := s.putPanelLayout(b.Key, b.Data, b.Client); err != nil {
		httpError(w, 400, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// MigrateHostWorkspaces makes Code's layout.json Code's alone (see migrateHosts and panelPanesOut). Once at start-up,
// here rather than in the UI: every device would otherwise do it at once.
func (s *Server) MigrateHostWorkspaces() {
	s.migrateHosts()
	s.panelPanesOut()
}

// panelPanesOut takes the panes of a panel's conversations (tags.panel) out of Code's layout, for a panel with panes of
// its own: before those existed (2026-11-26) a panel's worker opened as a tab in Code's workspaces, hidden from Code's
// list. One the panel's own layout doesn't show yet goes there, as a tab of its own.
func (s *Server) panelPanesOut() {
	if s.Layout == nil || s.Work == nil || s.Hub == nil {
		return
	}
	panels := s.PanelLayouts()
	if len(panels) == 0 {
		return
	}
	var l map[string]any
	if json.Unmarshal(s.Layout.Get(), &l) != nil || l == nil {
		return
	}
	owner := func(id string) string {
		sess, err := s.Hub.Session(id)
		if err != nil {
			return ""
		}
		k := sess.Summary().Tags["panel"]
		if k == "" {
			return ""
		}
		k = s.Work.Canon(k)
		if _, ok := panels[k]; ok {
			return k
		}
		return ""
	}
	moved := map[string][]node{} // panel → the panes taken out
	var keep []any
	for _, w := range nodes(l["workspaces"]) {
		var tabs []any
		for _, t := range tabsOf(w) {
			root := asNode(t["root"])
			for _, p := range leaves(root) {
				if k := owner(str(p, "sessionId")); k != "" {
					moved[k] = append(moved[k], p)
					root = removeLeaf(root, str(p, "id"))
				}
			}
			if root == nil {
				continue
			}
			t["root"] = root
			if lv := leaves(root); len(lv) > 0 && !hasLeaf(root, str(t, "focus")) {
				t["focus"] = lv[0]["id"]
			}
			if z := str(t, "zoom"); z != "" && !hasLeaf(root, z) {
				delete(t, "zoom")
			}
			tabs = append(tabs, t)
		}
		if len(tabs) == 0 {
			continue // held nothing but the panel's: it was hidden from Code's list anyway
		}
		w["tabs"] = tabs
		if !hasWs(tabs, w["activeTab"]) {
			w["activeTab"] = asNode(tabs[0])["id"]
		}
		keep = append(keep, w)
	}
	if len(moved) == 0 {
		return
	}
	for key, panes := range moved {
		var pl map[string]any
		_ = json.Unmarshal(panels[key], &pl)
		wss := nodes(pl["workspaces"])
		if len(wss) == 0 {
			continue
		}
		shown := map[string]bool{}
		for _, w := range wss {
			for _, t := range tabsOf(w) {
				for _, p := range leaves(asNode(t["root"])) {
					shown[str(p, "sessionId")] = true
				}
			}
		}
		for _, p := range panes {
			id := str(p, "sessionId")
			if shown[id] {
				continue
			}
			shown[id] = true
			w := wss[0]
			w["tabs"] = append(arr(w, "tabs"), node{"id": rid("t"), "root": p, "focus": p["id"]})
		}
		pl["workspaces"] = wss
		b, _ := json.Marshal(pl)
		if !sameJSON(b, panels[key]) {
			_ = s.putPanelLayout(key, b, "")
		}
	}
	if len(keep) == 0 {
		keep = []any{map[string]any{"id": "w1", "name": "default", "tabs": []any{map[string]any{"id": "t1", "root": map[string]any{"type": "pane", "id": "p1"}, "focus": "p1"}}, "activeTab": "t1"}}
	}
	l["workspaces"] = keep
	if !hasWs(keep, l["active"]) {
		l["active"] = asNode(keep[0])["id"]
	}
	if !hasWs(keep, l["lastActive"]) {
		delete(l, "lastActive")
	}
	b, _ := json.Marshal(l)
	_ = s.Layout.Set(b)
	s.Hub.Broadcast("layout", map[string]any{"data": s.Layout.Get(), "client": "gerdr-migrate"})
}

func hasLeaf(root node, id string) bool {
	for _, p := range leaves(root) {
		if str(p, "id") == id {
			return true
		}
	}
	return false
}

// migrateHosts moves the workspaces Code's layout.json still holds for a panel (marked "host", from when a
// panel's panes were kept inside Code's layout, 2026-11-26) into that panel's own layout.
func (s *Server) migrateHosts() {
	if s.Layout == nil || s.Work == nil {
		return
	}
	var l map[string]any
	if json.Unmarshal(s.Layout.Get(), &l) != nil || l == nil {
		return
	}
	var keep []any
	moved := map[string][]any{}
	all := nodes(l["workspaces"])
	for _, w := range all {
		h, _ := w["host"].(string)
		if h == "" {
			keep = append(keep, w)
			continue
		}
		delete(w, "host")
		if _, err := s.Work.Dir(h); err != nil {
			continue // a panel that isn't there any more: its panes go with it
		}
		moved[h] = append(moved[h], w)
	}
	if len(keep) == len(all) {
		return
	}
	panels := s.PanelLayouts()
	for key, wss := range moved {
		var pl map[string]any
		_ = json.Unmarshal(panels[s.Work.Canon(key)], &pl)
		if pl == nil {
			pl = map[string]any{"version": 1}
		}
		have, _ := pl["workspaces"].([]any)
		pl["workspaces"] = append(have, wss...)
		if !hasWs(pl["workspaces"].([]any), pl["active"]) {
			pl["active"] = wss[0].(map[string]any)["id"]
		}
		b, _ := json.Marshal(pl)
		if err := s.putPanelLayout(key, b, ""); err != nil && s.Log != nil {
			s.Log.Warn("panel layout migration", "panel", key, "err", err)
		}
	}
	if len(keep) == 0 {
		keep = []any{map[string]any{"id": "w1", "name": "default", "tabs": []any{map[string]any{"id": "t1", "root": map[string]any{"type": "pane", "id": "p1"}, "focus": "p1"}}, "activeTab": "t1"}}
	}
	l["workspaces"] = keep
	if !hasWs(keep, l["active"]) {
		l["active"] = keep[0].(map[string]any)["id"]
	}
	if !hasWs(keep, l["lastActive"]) {
		delete(l, "lastActive")
	}
	b, _ := json.Marshal(l)
	_ = s.Layout.Set(b)
}

// ownerPanel: the panel whose own layout session id's panes go in ("" = Code's): one a panel started (tags.panel)
// when that panel has panes of its own.
func (s *Server) ownerPanel(id string) string {
	if s.Hub == nil || s.Work == nil {
		return ""
	}
	sess, err := s.Hub.Session(id)
	if err != nil {
		return ""
	}
	k := sess.Summary().Tags["panel"]
	if k == "" {
		return ""
	}
	k = s.Work.Canon(k)
	if _, ok := s.Work.PanelLayouts()[k]; ok {
		return k
	}
	return ""
}
