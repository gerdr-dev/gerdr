package server

import (
	"encoding/json"
	"path/filepath"
	"strings"
)

// renamePanelDir renames a panel's directory (work.RenamePanel: the directory, its schedules' state and logs, the
// rail order, the old name kept as a symlink and an alias) and moves over what Gerdr keeps under its key or path
// outside it: the page's mount, the sessions (tags.panel, cwd, skill dirs, system prompt file), the inbox, badges,
// config.json workSessions, the layout's paths and the artifacts owners. Agents already running for the panel aren't
// restarted: their old key and paths still lead to it (work/rename.go).
func (s *Server) renamePanelDir(key, name string) (map[string]any, error) {
	key = s.Work.Canon(key)
	oldDir, err := s.Work.Dir(key)
	if err != nil {
		return nil, err
	}
	nkey, err := s.Work.RenamePanel(key, name)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"key": nkey, "from": key}
	if nkey == key {
		return out, nil
	}
	newDir, _ := s.Work.Dir(nkey)
	out["dir"] = newDir
	if s.UI != nil {
		s.UI.Mount(mountName(key), "")
	}
	if _, err := s.mountWork(nkey); err != nil {
		s.Log.Warn("work: renamed panel not mounted", "key", nkey, "err", err)
	}
	if s.Hub != nil {
		// Before the inbox moves: the page moves what it keeps by the key first, so the items aren't new to it
		s.Hub.Broadcast("panel_renamed", map[string]string{"from": key, "to": nkey})
		// Sessions recorded either path (a real directory, or the one with symlinks resolved): both are moved
		n := s.Hub.MoveDir(oldDir, newDir, map[string][2]string{PanelTag: {key, nkey}})
		if real, err := filepath.EvalSymlinks(filepath.Dir(oldDir)); err == nil && real != filepath.Dir(oldDir) {
			n += s.Hub.MoveDir(filepath.Join(real, filepath.Base(oldDir)), newDir, nil)
		}
		out["sessions"] = n
	}
	s.inbox.mu.Lock()
	s.inboxLoadLocked()
	if its, ok := s.inbox.items[key]; ok {
		for i := range its {
			its[i].Panel = nkey
		}
		s.inbox.items[nkey] = append(s.inbox.items[nkey], its...)
		delete(s.inbox.items, key)
		_ = s.inboxSaveLocked()
		all := s.inboxAllLocked()
		s.inbox.mu.Unlock()
		if s.Hub != nil {
			s.Hub.Broadcast("inbox", all)
		}
	} else {
		s.inbox.mu.Unlock()
	}
	s.badges.mu.Lock()
	s.badgesLoadLocked()
	if n, ok := s.badges.n[key]; ok {
		s.badges.n[nkey] = n
		delete(s.badges.n, key)
		_ = s.badgesSaveLocked()
		all := map[string]int{}
		for k, v := range s.badges.n {
			all[k] = v
		}
		s.badges.mu.Unlock()
		if s.Hub != nil {
			s.Hub.Broadcast("badges", all)
		}
	} else {
		s.badges.mu.Unlock()
	}
	s.renameInConfig(key, nkey)
	s.moveInLayout(oldDir, newDir)
	if s.Artifacts != nil {
		out["artifacts"] = s.Artifacts.RenamePanel(key, nkey)
	}
	s.Log.Info("work: panel renamed", "from", key, "to", nkey)
	return out, nil
}

// renameInConfig: config.json workSessions (the panel's agent conversation, kept by the UI) goes by the key.
func (s *Server) renameInConfig(key, nkey string) {
	if s.Config == nil {
		return
	}
	var c map[string]any
	if json.Unmarshal(s.Config.Get(), &c) != nil {
		return
	}
	ws, _ := c["workSessions"].(map[string]any)
	v, ok := ws[key]
	if !ok {
		return
	}
	ws[nkey] = v
	delete(ws, key)
	b, err := json.Marshal(c)
	if err != nil || s.Config.Set(b) != nil {
		return
	}
	if s.Hub != nil {
		s.Hub.Broadcast("config", map[string]any{"data": s.Config.Get(), "client": "gerdr-work"})
	}
}

// moveInLayout: Code workspaces, drafts and terminals opened in the panel's directory follow it.
func (s *Server) moveInLayout(from, to string) {
	if s.Layout == nil {
		return
	}
	layoutMu.Lock()
	defer layoutMu.Unlock()
	var l any
	if json.Unmarshal(s.Layout.Get(), &l) != nil {
		return
	}
	changed := false
	var walk func(v any) any
	walk = func(v any) any {
		switch x := v.(type) {
		case map[string]any:
			for k, e := range x {
				x[k] = walk(e)
			}
		case []any:
			for i, e := range x {
				x[i] = walk(e)
			}
		case string:
			if x == from || strings.HasPrefix(x, from+"/") {
				changed = true
				return to + x[len(from):]
			}
		}
		return v
	}
	l = walk(l)
	if !changed {
		return
	}
	b, err := json.Marshal(l)
	if err != nil {
		return
	}
	out := json.RawMessage(b)
	if s.Layout.Set(out) != nil {
		return
	}
	if s.Hub != nil {
		s.Hub.Broadcast("layout", map[string]any{"data": out, "client": "gerdr-work"})
	}
}
