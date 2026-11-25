package work

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
)

// The order of a project's panels on the rail is the person's, set by dragging a button (or `gerdr panel move`), like
// the order of projects. It is kept next to the panels, in <panels dir>/.order.json: a list of panel names, "" for the
// project's own panel. A dot-file can't be a panel (validProject refuses '.'), and it goes with the project's panels.
// Panels not in the list (added since, or never moved) keep the default place after the listed ones: the project's own
// first, then by name. A removed panel's name stays in the list, harmless, and puts it back where it was if restored.
//
// Code (the project's agents, always there) has its place in the same list as CodeName: it is dragged like any panel
// (asked for: "no special case for Code"). Lists saved before it was movable don't name it; it is then first, where
// it always was.

// CodeName stands for Code in the rail's order: "." can't be a panel's name (validProject), so it never clashes.
const CodeName = "."

const orderFile = ".order.json"

func (r *Runner) orderPath(project string) (string, error) {
	d, err := r.panelsDir(project)
	if err != nil {
		return "", err
	}
	return filepath.Join(d, orderFile), nil
}

func (r *Runner) savedOrder(project string) []string {
	p, err := r.orderPath(project)
	if err != nil {
		return nil
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var names []string
	_ = json.Unmarshal(b, &names)
	if !slices.Contains(names, CodeName) {
		names = append([]string{CodeName}, names...)
	}
	return names
}

// OrderPanels sorts a project's panel names ("" = its own) the way the person arranged them.
func (r *Runner) OrderPanels(project string, names []string) []string {
	saved := r.savedOrder(project)
	rank := func(n string) int {
		if i := slices.Index(saved, n); i >= 0 {
			return i
		}
		return len(saved)
	}
	out := slices.Clone(names)
	slices.SortStableFunc(out, func(a, b string) int { return rank(a) - rank(b) })
	return out
}

// PanelNames: a project's panels as the rail shows them ("" = its own, unless removed), in the person's order.
func (r *Runner) PanelNames(project string) []string {
	var names []string
	if !r.WorkRemoved(project) {
		if _, err := r.Dir(project); err == nil {
			names = append(names, "")
		}
	}
	names = append(names, r.Panels(project)...)
	return r.OrderPanels(project, names)
}

// RailOrder: everything in the project's rail group, in the person's order: its panels and Code (CodeName).
func (r *Runner) RailOrder(project string) []string {
	return r.OrderPanels(project, append([]string{CodeName}, r.PanelNames(project)...))
}

// MovePanel puts a panel (or Code: key "<project>/.") at position `to` (0-based, clamped) among its project's rail
// group, Code included.
func (r *Runner) MovePanel(key string, to int) error {
	key = r.Canon(key) // an old name of a renamed panel (rename.go)
	project, panel := SplitKey(key)
	names := r.RailOrder(project)
	i := slices.Index(names, panel)
	if i < 0 {
		return fmt.Errorf("no panel %q", key)
	}
	names = slices.Delete(names, i, i+1)
	to = max(0, min(to, len(names)))
	names = slices.Insert(names, to, panel)
	// Keep removed panels' places too, after the live ones, so a restore puts them back near where they were
	for _, n := range r.savedOrder(project) {
		if !slices.Contains(names, n) {
			names = append(names, n)
		}
	}
	p, err := r.orderPath(project)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, _ := json.Marshal(names)
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}
