package addons

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// The state file (<root>/addons-state.json): which add-ons are switched off, which came from a store, and the stores.
// Kept outside the add-on directory so writing it doesn't count as editing an add-on (though the watcher looks at it:
// switching one off reloads it).

// DefaultStore is the store every gerdr starts with: the add-ons that used to ship inside gerdr live there.
const DefaultStore = "gerdr-dev/gerdr-addon"

// Installed: an add-on that came from a store.
type Installed struct {
	Store   string `json:"store"`
	Version string `json:"version,omitempty"`
	Hash    string `json:"hash"` // hashDir of what was installed
	At      int64  `json:"at"`   // unix ms
}

type state struct {
	Disabled  []string             `json:"disabled,omitempty"`
	Installed map[string]Installed `json:"installed,omitempty"`
	// Stores: nil = never touched (the default store); [] = all removed.
	Stores *[]string `json:"stores,omitempty"`
	// Settings: add-on id → key → value, what was set in Settings → Add-ons. Kept here, not in the browser, so every
	// window and device reads the same; and not in the add-on's folder, which an update replaces.
	Settings map[string]map[string]any `json:"settings,omitempty"`
}

func (s *state) disabled(id string) bool { return slices.Contains(s.Disabled, id) }

func (s *state) stores() []string {
	if s.Stores == nil {
		return []string{DefaultStore}
	}
	return *s.Stores
}

func (m *Manager) statePath() string { return filepath.Join(m.root, "addons-state.json") }

func (m *Manager) readState() *state {
	st := &state{}
	if b, err := os.ReadFile(m.statePath()); err == nil {
		_ = json.Unmarshal(b, st)
	}
	if st.Installed == nil {
		st.Installed = map[string]Installed{}
	}
	return st
}

// updateState: read, change, write, under one lock.
func (m *Manager) updateState(f func(st *state) error) error {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	st := m.readState()
	if err := f(st); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(st, "", "  ")
	tmp := m.statePath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, m.statePath())
}

// SetEnabled switches an add-on on or off: off unloads its UI half in every window and stops its service.
func (m *Manager) SetEnabled(id string, on bool) error {
	return m.updateState(func(st *state) error {
		st.Disabled = slices.DeleteFunc(st.Disabled, func(x string) bool { return x == id })
		if !on {
			st.Disabled = append(st.Disabled, id)
		}
		return nil
	})
}

// SettingSpec is one entry of addon.json "settings".
type SettingSpec struct {
	Key         string   `json:"key"`
	Title       string   `json:"title,omitempty"`
	Description string   `json:"description,omitempty"`
	Type        string   `json:"type"` // number / string / boolean / select
	Default     any      `json:"default,omitempty"`
	Min         *float64 `json:"min,omitempty"`
	Max         *float64 `json:"max,omitempty"`
	Unit        string   `json:"unit,omitempty"` // shown after a number ("分钟")
	Options     []struct {
		Value any    `json:"value"`
		Label string `json:"label,omitempty"`
	} `json:"options,omitempty"`
}

// SetSettings stores an add-on's settings (keys it declares only); a null value goes back to the default. The state
// file's change reaches every window through the watcher, and g.settings reads the new values.
func (m *Manager) SetSettings(id string, values map[string]any) error {
	if !validID(id) {
		return errors.New("bad add-on id")
	}
	mf, err := readManifest(filepath.Join(m.dir, id))
	if err != nil {
		return err
	}
	known := map[string]SettingSpec{}
	if mf != nil {
		for _, s := range mf.Settings {
			known[s.Key] = s
		}
	}
	for k, v := range values {
		s, ok := known[k]
		if !ok {
			return fmt.Errorf("%s has no setting %q", id, k)
		}
		if n, ok := v.(float64); ok && s.Type == "number" && (s.Min != nil && n < *s.Min || s.Max != nil && n > *s.Max) {
			return fmt.Errorf("%s: %v is out of range", k, n)
		}
	}
	return m.updateState(func(st *state) error {
		if st.Settings == nil {
			st.Settings = map[string]map[string]any{}
		}
		cur := st.Settings[id]
		if cur == nil {
			cur = map[string]any{}
		}
		for k, v := range values {
			if v == nil {
				delete(cur, k)
			} else {
				cur[k] = v
			}
		}
		if len(cur) == 0 {
			delete(st.Settings, id)
		} else {
			st.Settings[id] = cur
		}
		return nil
	})
}

func (m *Manager) recordInstall(id string, rec Installed) error {
	rec.At = time.Now().UnixMilli()
	return m.updateState(func(st *state) error {
		st.Installed[id] = rec
		return nil
	})
}
