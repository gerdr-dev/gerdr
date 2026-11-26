package work

import (
	"os"
	"path/filepath"
)

// A panel showing Code with a list of its own (the UI's CodeView list="replace") has panes of its own: tabs, splits,
// terminals, the conversations it started. They are the panel's data like its tasks are, so they live in its own
// directory (user/layout.json, the same shape as layout.json), apart from Code mode's layout.json: renaming, removing
// and restoring the panel take them along, and nothing of one side can end up in the other's. docs/dev/HOSTED-CODE.md

// PanelLayoutPath is where panel `key` keeps its own panes.
func (r *Runner) PanelLayoutPath(key string) (string, error) {
	d, err := r.Dir(key)
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "user", "layout.json"), nil
}

// PanelLayouts lists the panels that have panes of their own: key → its layout file.
func (r *Runner) PanelLayouts() map[string]string {
	out := map[string]string{}
	add := func(key string) {
		if p, err := r.PanelLayoutPath(key); err == nil {
			if _, err := os.Stat(p); err == nil {
				out[r.Canon(key)] = p
			}
		}
	}
	for _, project := range r.Projects() {
		add(PanelKey(project, ""))
		for _, panel := range r.Panels(project) {
			add(PanelKey(project, panel))
		}
	}
	return out
}
