package work

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Renaming a panel's directory changes its key ("default/fusion" → "default/superagent"), and the key is written down
// all over: session tags, the inbox, config, artifacts owners, the schedules' state, log directories, and — out of
// reach — the agents already running for it: their MCP server was started with `--project <old key>`, their system
// prompt names files under the old directory, commands a function started carry GERDR_PANEL=<old key>. Restarting
// every one of those would cut turns off, so the old name keeps working instead:
//
//   - <panels dir>/.renamed.json maps old names to new ones, and Canon turns an old key into the new one wherever a key
//     comes in (Dir, WorkKey, the Runner's entry points). An old key never comes back to life with state of its own.
//   - <panels dir>/<old> is left as a symlink to the new directory, so old absolute paths still read the same files.
//     Panels skips symlinks (DirEntry.IsDir is false for one), so it isn't a second panel on the rail.
//
// What Gerdr keeps outside the Runner (sessions, inbox, config, layout, artifacts, the UI's mounts) the server moves
// over (server/workapi.go renamePanel).

const renamedFile = ".renamed.json"

func (r *Runner) renamedPath(project string) (string, error) {
	d, err := r.panelsDir(project)
	if err != nil {
		return "", err
	}
	return filepath.Join(d, renamedFile), nil
}

func (r *Runner) renamed(project string) map[string]string {
	p, err := r.renamedPath(project)
	if err != nil {
		return nil
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var m map[string]string
	_ = json.Unmarshal(b, &m)
	return m
}

// Renamed: a project's renamed panels, old name → current name.
func (r *Runner) Renamed(project string) map[string]string {
	m := r.renamed(project)
	out := map[string]string{}
	for old := range m {
		if k := r.Canon(PanelKey(project, old)); k != PanelKey(project, old) {
			_, out[old] = SplitKey(k)
		}
	}
	return out
}

// RenamedKeys: every project's renamed panels, old key → current key.
func (r *Runner) RenamedKeys() map[string]string {
	out := map[string]string{}
	projects := []string{DefaultProject}
	ents, _ := os.ReadDir(filepath.Join(r.Root, "projects"))
	for _, e := range ents {
		if e.IsDir() && validProject(e.Name()) && e.Name() != DefaultProject {
			projects = append(projects, e.Name())
		}
	}
	for _, p := range projects {
		for old, now := range r.Renamed(p) {
			out[PanelKey(p, old)] = PanelKey(p, now)
		}
	}
	return out
}

// Canon is the key a panel goes by now: an old name of a renamed panel becomes its current one. Any other key is
// returned as it is.
func (r *Runner) Canon(key string) string {
	project, panel := SplitKey(key)
	if panel == "" {
		return key
	}
	orig, m := panel, r.renamed(project)
	for range 8 { // a chain of renames (a → b → c); bounded, a loop in a hand-edited file mustn't hang
		next, ok := m[panel]
		if !ok || next == panel {
			break
		}
		panel = next
	}
	if panel == orig {
		return key
	}
	return PanelKey(project, panel)
}

// RenamePanel moves a panel's directory to a new name in the same project and returns its new key. Its functions
// must not be running (they hold the old key and directory until they end); schedules, triggers and streams pick the
// new key up from the directory on their next pass. The project's own panel (key without a name) has no directory
// name to change.
func (r *Runner) RenamePanel(key, name string) (string, error) {
	key = r.Canon(key)
	project, old := SplitKey(key)
	if old == "" {
		return "", fmt.Errorf("a project's own panel has no directory name to change")
	}
	name = strings.TrimSpace(name)
	if !validProject(name) {
		return "", fmt.Errorf("bad panel name %q: no / \\ . or control characters", name)
	}
	if name == old {
		return key, nil
	}
	dir, err := r.Dir(key)
	if err != nil {
		return "", err
	}
	if fi, err := os.Lstat(dir); err != nil || !fi.IsDir() {
		return "", fmt.Errorf("no panel %q in %s", old, project)
	}
	nkey := PanelKey(project, name)
	ndir := filepath.Join(filepath.Dir(dir), name)
	if fi, err := os.Lstat(ndir); err == nil {
		// The new name may be this panel's own leftover link from an earlier rename the other way: reuse its place
		if t, _ := os.Readlink(ndir); fi.Mode()&os.ModeSymlink == 0 || t != old {
			return "", fmt.Errorf("there is already a panel %q in %s", name, project)
		}
		if err := os.Remove(ndir); err != nil {
			return "", err
		}
	}
	r.mu.Lock()
	for _, rn := range r.runs {
		if r.Canon(rn.Project) == key && !rn.ended() {
			r.mu.Unlock()
			return "", fmt.Errorf("%s is running in this panel; try again when it ends", rn.Fn)
		}
	}
	r.mu.Unlock()
	r.sched_.mu.Lock()
	if p := r.sched_.projects[key]; p != nil && len(p.running) > 0 {
		r.sched_.mu.Unlock()
		return "", fmt.Errorf("a schedule of this panel is running; try again when it ends")
	}
	if err := os.Rename(dir, ndir); err != nil {
		r.sched_.mu.Unlock()
		return "", err
	}
	// The schedules' state (last runs, paused) and the run logs live outside the directory, under the key
	delete(r.sched_.projects, key)
	if err := os.MkdirAll(filepath.Dir(r.stateFile(nkey)), 0o755); err == nil {
		_ = os.Rename(r.stateFile(key), r.stateFile(nkey))
	}
	r.sched_.mu.Unlock()
	_ = os.Rename(r.logDir(key), r.logDir(nkey))
	r.trig_.mu.Lock()
	delete(r.trig_.projects, key)
	r.trig_.mu.Unlock()
	r.mu.Lock()
	delete(r.installed, dir)
	delete(r.installed, ndir)
	r.mu.Unlock()

	_ = os.Symlink(name, dir) // relative: it follows the panels directory if that moves
	m := r.renamed(project)
	if m == nil {
		m = map[string]string{}
	}
	delete(m, name) // the new name is a panel again, not an alias
	m[old] = name
	if err := r.saveRenamed(project, m); err != nil {
		return nkey, err
	}
	r.renameInOrder(project, old, name)
	_, err = r.Ensure(nkey)
	return nkey, err
}

func (r *Runner) saveRenamed(project string, m map[string]string) error {
	p, err := r.renamedPath(project)
	if err != nil {
		return err
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// renameInOrder keeps the panel's place on the rail under its new name.
func (r *Runner) renameInOrder(project, old, name string) {
	p, err := r.orderPath(project)
	if err != nil {
		return
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return
	}
	var names []string
	if json.Unmarshal(b, &names) != nil {
		return
	}
	i := slices.Index(names, old)
	if i < 0 {
		return
	}
	names[i] = name
	b, _ = json.Marshal(names)
	tmp := p + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil {
		_ = os.Rename(tmp, p)
	}
}
