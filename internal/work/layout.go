package work

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
)

// A project's Code side lives in its project directory too: <GERDR_DIR>/projects/<name>/layout.json
// holds the project's workspaces (tabs, splits, which conversation each pane shows) and which one was
// active. It's kept current from layout.json — the live layout of the open projects, which the UI,
// other windows / devices and `gerdr workspace` all work on — every time that's saved (SaveLayouts).
// So closing a project and creating one with the same name later brings back its Code as well as its
// Work (the UI reads Layout when it creates the project).

// ProjectDir is a project's own directory. The default project's Work stays in <GERDR_DIR>/work (where it
// lived before projects), but its layout copy is under projects/ like everyone's.
func (r *Runner) ProjectDir(project string) (string, error) {
	if project == "" {
		project = DefaultProject
	}
	if !validProject(project) {
		return "", errBadProject(project)
	}
	return filepath.Join(r.Root, "projects", project), nil
}

// ProjectLayout is what layout.json keeps per project.
type ProjectLayout struct {
	Workspaces []json.RawMessage `json:"workspaces"`
	Active     string            `json:"active,omitempty"`
}

// SaveLayouts splits a saved layout.json by project and writes each open project's part into its
// directory. Projects not in it (closed ones) keep the copy they had; a project with no workspaces left
// isn't written either, so that copy is what comes back.
func (r *Runner) SaveLayouts(raw json.RawMessage) {
	var l struct {
		Workspaces []json.RawMessage `json:"workspaces"`
		Spaces     []struct {
			ID     string `json:"id"`
			Active string `json:"active"`
		} `json:"spaces"`
		Active string `json:"active"`
	}
	if json.Unmarshal(raw, &l) != nil {
		return
	}
	by := map[string]*ProjectLayout{}
	for _, w := range l.Workspaces {
		var h struct {
			ID    string `json:"id"`
			Space string `json:"space"`
		}
		if json.Unmarshal(w, &h) != nil {
			continue
		}
		sp := h.Space
		if sp == "" {
			sp = DefaultProject
		}
		p := by[sp]
		if p == nil {
			p = &ProjectLayout{}
			by[sp] = p
		}
		p.Workspaces = append(p.Workspaces, w)
		if h.ID == l.Active {
			p.Active = h.ID // the project on screen: its active workspace is the layout's
		}
	}
	for _, s := range l.Spaces {
		if p := by[s.ID]; p != nil && p.Active == "" {
			p.Active = s.Active // other projects remember theirs in spaces[].active
		}
	}
	for sp, p := range by {
		dir, err := r.ProjectDir(sp)
		if err != nil {
			continue
		}
		b, _ := json.MarshalIndent(p, "", "  ")
		path := filepath.Join(dir, "layout.json")
		if cur, err := os.ReadFile(path); err == nil && bytes.Equal(cur, b) {
			continue
		}
		if os.MkdirAll(dir, 0o755) != nil {
			continue
		}
		tmp := path + ".tmp"
		if os.WriteFile(tmp, b, 0o600) == nil {
			_ = os.Rename(tmp, path)
		}
	}
}

// Layout is the project's saved Code layout; nil when it has none.
func (r *Runner) Layout(project string) (*ProjectLayout, error) {
	dir, err := r.ProjectDir(project)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(filepath.Join(dir, "layout.json"))
	if err != nil {
		return nil, nil
	}
	var p ProjectLayout
	if json.Unmarshal(b, &p) != nil || len(p.Workspaces) == 0 {
		return nil, nil
	}
	return &p, nil
}
