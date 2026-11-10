package work

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// A panel's name and its button on the rail ("title", "icon", "color") come from two files:
//
//	ui/ui.json     the panel's own, written by its author (or their agent) and shared through git
//	user/ui.json   this person's rename / icon pick, over it; never shared
//
// The person's choice used to be written into ui/ui.json. In an installed panel that is a change to a file git
// tracks, so every update had to stash it and put it back, and once the panel's own ui.json changed too the
// choice stayed behind in the stash: the panel went back to its published name with nothing said. In user/ an update
// never sees it.
const uiFile = "ui.json"

// UIPrefKeys: what a person may set for a panel on top of its ui.json.
var UIPrefKeys = []string{"title", "icon", "color"}

func isUIPref(k string) bool {
	for _, p := range UIPrefKeys {
		if p == k {
			return true
		}
	}
	return false
}

func readJSONMap(p string) (map[string]any, error) {
	m := map[string]any{}
	raw, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func writeJSONMap(p string, m map[string]any) error {
	out, _ := json.MarshalIndent(m, "", "  ")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, append(out, '\n'), 0o644)
}

// UIConfig is the panel's ui.json with this person's choices over it: what the rail shows. A broken user/ui.json is
// skipped (the panel's own still shows); a broken ui/ui.json is the error.
func (r *Runner) UIConfig(key string) (map[string]any, error) {
	dir, err := r.Dir(key)
	if err != nil {
		return nil, err
	}
	cfg, err := readJSONMap(filepath.Join(dir, "ui", uiFile))
	if err != nil {
		cfg = map[string]any{}
	}
	if mine, err2 := readJSONMap(filepath.Join(dir, UserDir, uiFile)); err2 == nil {
		for k, v := range mine {
			if isUIPref(k) {
				cfg[k] = v
			}
		}
	}
	return cfg, err
}

// SetUIPrefs records this person's name / icon / color for the panel in user/ui.json. An empty value is kept as ""
// (not dropped): clearing a color has to hide the panel's own color, not fall back to it.
func (r *Runner) SetUIPrefs(key string, vals map[string]string) error {
	dir, err := r.Dir(key)
	if err != nil {
		return err
	}
	ensureUserDir(dir)
	p := filepath.Join(dir, UserDir, uiFile)
	mine, err := readJSONMap(p)
	if err != nil {
		mine = map[string]any{} // broken: what is being set now replaces it
	}
	for k, v := range vals {
		if !isUIPref(k) {
			continue
		}
		if v = strings.TrimSpace(v); len(v) > 64 {
			v = ""
		}
		mine[k] = v
	}
	return writeJSONMap(p, mine)
}

// liftUIPrefs moves a person's choices that older Gerdr wrote into an installed panel's ui/ui.json over to
// user/ui.json and gives ui/ui.json back its committed content, so updates stop stashing it. Only for a panel
// installed from somewhere (a git origin) whose ui/ui.json differs from HEAD in title / icon / color alone: any other
// change there is someone working on the panel, left as it is. Best effort.
func liftUIPrefs(dir string) {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return
	}
	if remote, _ := git(dir, "remote", "get-url", "origin"); remote == "" {
		return
	}
	p := filepath.Join(dir, "ui", uiFile)
	cur, err := os.ReadFile(p)
	if err != nil {
		return
	}
	head, err := exec.Command("git", "-C", dir, "show", "HEAD:ui/"+uiFile).Output()
	if err != nil || bytes.Equal(cur, head) {
		return
	}
	var a, b map[string]any
	if json.Unmarshal(cur, &a) != nil || json.Unmarshal(head, &b) != nil {
		return
	}
	lifted := map[string]any{}
	for _, m := range []map[string]any{a, b} {
		for k := range m {
			ja, _ := json.Marshal(a[k])
			jb, _ := json.Marshal(b[k])
			if bytes.Equal(ja, jb) {
				continue
			}
			if !isUIPref(k) {
				return
			}
			if v, ok := a[k]; ok {
				lifted[k] = v
			} else {
				lifted[k] = ""
			}
		}
	}
	ensureUserDir(dir)
	up := filepath.Join(dir, UserDir, uiFile)
	mine, err := readJSONMap(up)
	if err != nil {
		return
	}
	for k, v := range lifted {
		if _, set := mine[k]; !set {
			mine[k] = v
		}
	}
	if writeJSONMap(up, mine) != nil {
		return
	}
	_ = os.WriteFile(p, head, 0o644)
}
