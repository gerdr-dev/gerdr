package addons

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/gerdr-dev/gerdr/internal/events"
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
	Key         string `json:"key"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	Type        string `json:"type"` // number / string / boolean / select / color ("#rrggbb") / secret (a string the form masks) / link
	// link: not a value but a button that opens URL (a page of the add-on's: its control panel, a dashboard). When the
	// address is only known at run time (a port the service picked), the service reports it in its status line's
	// "links" by this setting's key, which wins over URL. Label is the button's text ("Open" when empty).
	URL     string   `json:"url,omitempty"`
	Label   string   `json:"label,omitempty"`
	Default any      `json:"default,omitempty"`
	Min     *float64 `json:"min,omitempty"`
	Max     *float64 `json:"max,omitempty"`
	Unit    string   `json:"unit,omitempty"` // shown after a number ("分钟")
	// Options: a select's choices. With OptionsFrom they are worked out each time the add-ons are listed (resolve).
	Options     []SettingOption `json:"options,omitempty"`
	OptionsFrom *OptionsFrom    `json:"optionsFrom,omitempty"`
	// Required: a select that is never empty. No empty choice; until one is picked, or when the one picked is gone
	// (its add-on removed), the value is the first option — what the UI shows and the add-on reads alike.
	Required bool `json:"required,omitempty"`
}

type SettingOption struct {
	Value any    `json:"value"`
	Label string `json:"label,omitempty"`
}

// OptionsFrom: where a select's choices come from at run time instead of addon.json.
//
//	{"events": "*/dm", "reply": true}   the event sources the switched-on add-ons declare ("emits") whose
//	                                    source/kind matches the pattern (reply: only those that take answers);
//	                                    value = the source ("slack"), label = the add-on's name
//
// Asked for (2026-11): remote-answer picks which chat to talk in from the chat add-ons installed, so Slack today and
// Feishu tomorrow show up without either add-on knowing the other. Gerdr fills them in, not the service: the form
// works while the service is stopped, and the service gets the resolved value like any other setting.
type OptionsFrom struct {
	Events string `json:"events,omitempty"`
	Reply  bool   `json:"reply,omitempty"`
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
		specs, _ := m.resolve(mf.Settings, nil)
		for _, s := range specs {
			known[s.Key] = s
		}
	}
	for k, v := range values {
		s, ok := known[k]
		if !ok {
			return fmt.Errorf("%s has no setting %q", id, k)
		}
		if s.Type == "link" {
			return fmt.Errorf("%s: %q is a link, it has no value", id, k)
		}
		if s.Type == "select" && s.Required && v != nil &&
			!slices.ContainsFunc(s.Options, func(o SettingOption) bool { return fmt.Sprint(o.Value) == fmt.Sprint(v) }) {
			return fmt.Errorf("%s: %v is not one of %q's options", id, v, k)
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

// SettingsOf: an add-on's settings as it sees them — every declared key's default, overlaid with what was set
// (secrets in the clear, like g.settings.get). Links have no value and are left out.
func (m *Manager) SettingsOf(id string) map[string]any {
	out := map[string]any{}
	if !validID(id) {
		return out
	}
	var specs []SettingSpec
	if mf, err := readManifest(filepath.Join(m.dir, id)); err == nil && mf != nil {
		specs = mf.Settings
	}
	specs, values := m.resolve(specs, m.readState().Settings[id])
	for _, s := range specs {
		if s.Type != "link" && s.Default != nil {
			out[s.Key] = s.Default
		}
	}
	for k, v := range values {
		out[k] = v
	}
	return out
}

// resolve: the settings as they apply now — dynamic options filled in (OptionsFrom), and for a required select a
// default that is one of its options and no stored value that isn't (it falls back to that default). Specs and
// values are copies; the manifest and the state file are left as they are, so a removed choice that comes back
// (the add-on reinstalled) is picked again.
func (m *Manager) resolve(specs []SettingSpec, values map[string]any) ([]SettingSpec, map[string]any) {
	out := make([]SettingSpec, len(specs))
	var vals map[string]any // nil stays nil: "nothing set" (the UI and the listing tell the two apart)
	for k, v := range values {
		if vals == nil {
			vals = map[string]any{}
		}
		vals[k] = v
	}
	var emits []events.Source
	for i, s := range specs {
		if s.OptionsFrom != nil && s.OptionsFrom.Events != "" {
			if emits == nil {
				emits = m.Emits()
			}
			s.Options = m.eventOptions(emits, *s.OptionsFrom)
		}
		if s.Type == "select" && s.Required {
			has := func(v any) bool {
				return v != nil && slices.ContainsFunc(s.Options, func(o SettingOption) bool { return fmt.Sprint(o.Value) == fmt.Sprint(v) })
			}
			if !has(s.Default) {
				s.Default = nil
				if len(s.Options) > 0 {
					s.Default = s.Options[0].Value
				}
			}
			if v, ok := vals[s.Key]; ok && !has(v) {
				delete(vals, s.Key)
			}
		}
		out[i] = s
	}
	return out, vals
}

// eventOptions: one option per source that declares a matching event (OptionsFrom.Events).
func (m *Manager) eventOptions(emits []events.Source, f OptionsFrom) []SettingOption {
	opts := []SettingOption{}
	seen := map[string]bool{}
	for _, e := range emits {
		src, kind, _ := strings.Cut(e.On, "/")
		if seen[src] || (f.Reply && !e.CanReply) || !events.Match(f.Events, events.Event{Source: src, Kind: kind}) {
			continue
		}
		seen[src] = true
		label := e.Addon
		if mf, err := readManifest(filepath.Join(m.dir, e.Addon)); err == nil && mf != nil && mf.Name != "" {
			label = mf.Name
		}
		if src != e.Addon {
			label += " (" + src + ")"
		}
		opts = append(opts, SettingOption{Value: src, Label: label})
	}
	return opts
}
