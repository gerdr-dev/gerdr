package main

// gerdr addon: manage add-ons (Settings → Add-ons) from the command line, so an agent can browse the stores,
// install / update / remove add-ons, switch them on and off and change their settings for the user.
//
// Like `gerdr mcp` / `gerdr skill`, it works on the files directly (<GERDR_DIR>/addons, addons-state.json and the
// store cache) through the same internal/addons code the HTTP API uses, so it works with gerdr stopped. A running
// gerdr's watcher sees the add-on directory and the state file within a second: every window reloads the add-on and
// services start / stop. The one thing it doesn't see is a store list change (it broadcasts that only from its own
// API); an open store tab shows it after reopening Settings.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/gerdr-dev/gerdr/internal/addons"
	"github.com/gerdr-dev/gerdr/internal/config"
)

func openAddons() *addons.Manager { return addons.New(config.FromEnv().Dir, nil) }

// ---- views (what is printed) ----

type addonSettingView struct {
	Key         string `json:"key"`
	Type        string `json:"type"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	Default     any    `json:"default,omitempty"`
	Value       any    `json:"value,omitempty"` // only when changed from the default
	Min         any    `json:"min,omitempty"`
	Max         any    `json:"max,omitempty"`
	Unit        string `json:"unit,omitempty"`
	Options     []any  `json:"options,omitempty"`
}

// viewSettings: secret values print as <hidden>, like MCP tokens: whatever is printed lands in the transcript.
func viewSettings(a *addons.Addon) []addonSettingView {
	out := []addonSettingView{}
	for _, s := range a.Settings {
		v := addonSettingView{Key: s.Key, Type: s.Type, Title: s.Title, Description: s.Description, Default: s.Default, Unit: s.Unit}
		if s.Min != nil {
			v.Min = *s.Min
		}
		if s.Max != nil {
			v.Max = *s.Max
		}
		for _, o := range s.Options {
			if o.Label != "" {
				v.Options = append(v.Options, map[string]any{"value": o.Value, "label": o.Label})
			} else {
				v.Options = append(v.Options, o.Value)
			}
		}
		if val, ok := a.SettingValues[s.Key]; ok {
			v.Value = val
			if s.Type == "secret" {
				v.Value = "<hidden>"
			}
		}
		if s.Type == "secret" && v.Default != nil {
			v.Default = "<hidden>"
		}
		out = append(out, v)
	}
	return out
}

type addonView struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Version     string `json:"version,omitempty"`
	Description string `json:"description,omitempty"`
	Enabled     bool   `json:"enabled"`
	Dir         string `json:"dir"`
	Store       string `json:"store,omitempty"`    // where it was installed from; empty = made by hand
	Modified    bool   `json:"modified,omitempty"` // edited after installing: an update would overwrite that (--force)
	Update      string `json:"update,omitempty"`   // the store has a different copy: its version (or "available")
	HasService  bool   `json:"hasService,omitempty"`
	Service     any    `json:"service,omitempty"` // status from the running gerdr
	Log         string `json:"log,omitempty"`     // the service's stderr
	Settings    int    `json:"settings,omitempty"`
	Error       string `json:"error,omitempty"`
}

// storeIndex: id → the add-on each store offers, from the cache.
func storeIndex(stores []addons.StoreView) map[string]map[string]addons.StoreAddon {
	out := map[string]map[string]addons.StoreAddon{}
	for _, s := range stores {
		for _, a := range s.Addons {
			if out[a.ID] == nil {
				out[a.ID] = map[string]addons.StoreAddon{}
			}
			out[a.ID][s.Source] = a
		}
	}
	return out
}

// updateOf: what the add-on's own store has now, when it differs from what was installed (by content, not version:
// a store may forget to bump it). Same rule as the UI.
func updateOf(a *addons.Addon, idx map[string]map[string]addons.StoreAddon) string {
	if a.Source == nil {
		return ""
	}
	sa, ok := idx[a.ID][a.Source.Store]
	if !ok || sa.Hash == a.Source.Hash {
		return ""
	}
	if sa.Version != "" && sa.Version != a.Source.Version {
		return sa.Version
	}
	return "available"
}

// liveAddons: build errors and service status from the running gerdr (nil when it isn't running).
func liveAddons() map[string]struct {
	Error   string `json:"error"`
	Service any    `json:"service"`
} {
	c, err := newAPIClient()
	if err != nil {
		return nil
	}
	c.http.Timeout = 10 * time.Second
	var list []struct {
		ID      string `json:"id"`
		Error   string `json:"error"`
		Service any    `json:"service"`
	}
	if c.do("GET", "/api/addons", nil, &list) != nil {
		return nil
	}
	out := map[string]struct {
		Error   string `json:"error"`
		Service any    `json:"service"`
	}{}
	for _, a := range list {
		out[a.ID] = struct {
			Error   string `json:"error"`
			Service any    `json:"service"`
		}{a.Error, a.Service}
	}
	return out
}

func viewAddon(a *addons.Addon, idx map[string]map[string]addons.StoreAddon) addonView {
	v := addonView{ID: a.ID, Name: a.Name, Version: a.Version, Description: a.Description, Enabled: !a.Disabled, Dir: a.Dir,
		Modified: a.Modified, Update: updateOf(a, idx), HasService: a.HasService, Settings: len(a.Settings), Error: a.Error}
	if a.Source != nil {
		v.Store = a.Source.Store
	}
	if a.HasService {
		v.Log = filepath.Join(config.FromEnv().Dir, "logs", "addons", a.ID+".log")
	}
	return v
}

func findAddon(m *addons.Manager, id string) (*addons.Addon, error) {
	for _, a := range m.Describe() {
		if a.ID == id {
			return a, nil
		}
	}
	return nil, fmt.Errorf("no add-on %q installed (gerdr addon list)", id)
}

// fetchNew downloads the stores never downloaded (the UI does the same when the store tab opens): an empty
// default store would look like there is nothing to install.
func fetchNew(m *addons.Manager) {
	for _, s := range m.Stores() {
		if !s.Local && s.FetchedAt == 0 {
			if err := m.RefreshStore(context.Background(), s.Source); err != nil {
				fmt.Fprintf(os.Stderr, "note: downloading store %s failed: %v\n", s.Source, err)
			}
		}
	}
}

// pickStore: the store to install id from. With several offering it, --store has to say which, unless one of them
// is where the installed copy came from.
func pickStore(m *addons.Manager, id, store string) (string, error) {
	if store != "" {
		src, err := addons.NormalizeSource(store)
		if err != nil {
			return "", err
		}
		return src, nil
	}
	var have []string
	for _, s := range m.Stores() {
		if slices.ContainsFunc(s.Addons, func(a addons.StoreAddon) bool { return a.ID == id }) {
			have = append(have, s.Source)
		}
	}
	switch len(have) {
	case 0:
		return "", fmt.Errorf("no store offers %q (gerdr addon store; refresh with gerdr addon store refresh)", id)
	case 1:
		return have[0], nil
	}
	if a, err := findAddon(m, id); err == nil && a.Source != nil && slices.Contains(have, a.Source.Store) {
		return a.Source.Store, nil
	}
	return "", fmt.Errorf("%q is in several stores (%s): pick one with --store", id, strings.Join(have, ", "))
}

func install(m *addons.Manager, id, store string, force bool) (map[string]any, error) {
	src, err := pickStore(m, id, store)
	if err != nil {
		return nil, err
	}
	if err := m.InstallFromStore(src, id, force); err != nil {
		if errors.Is(err, addons.ErrConflict) {
			return nil, fmt.Errorf("%w (ask the user before overwriting; then --force)", err)
		}
		return nil, err
	}
	a, err := findAddon(m, id)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"id": id, "store": src, "version": a.Version, "dir": a.Dir, "enabled": !a.Disabled}
	if len(a.Settings) > 0 {
		out["settings"] = viewSettings(a)
	}
	return out, nil
}

// ---- settings values ----

var colorRE = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// parseSetting turns the text after key= into the value addon.json's type asks for.
func parseSetting(s addons.SettingSpec, raw string) (any, error) {
	switch s.Type {
	case "number":
		n, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, fmt.Errorf("%s is a number, not %q", s.Key, raw)
		}
		return n, nil
	case "boolean":
		switch strings.ToLower(raw) {
		case "true", "on", "yes", "1":
			return true, nil
		case "false", "off", "no", "0":
			return false, nil
		}
		return nil, fmt.Errorf("%s is true or false, not %q", s.Key, raw)
	case "select":
		var vals []string
		for _, o := range s.Options {
			if fmt.Sprint(o.Value) == raw {
				return o.Value, nil
			}
			vals = append(vals, fmt.Sprint(o.Value))
		}
		return nil, fmt.Errorf("%s is one of %s, not %q", s.Key, strings.Join(vals, " / "), raw)
	case "color":
		if !colorRE.MatchString(raw) {
			return nil, fmt.Errorf("%s is a color like #3b82f6, not %q", s.Key, raw)
		}
		return strings.ToLower(raw), nil
	}
	return raw, nil // string / secret
}

// ---- commands ----

func addonCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "addon",
		Short: "Manage add-ons (Settings → Add-ons): stores, install / update / remove, on / off, settings",
		Long: `Manage add-ons (Settings → Add-ons); open windows follow within a second.

An add-on is a folder in <GERDR_DIR>/addons (UI code and / or a background service) that changes Gerdr itself:
a sidebar panel, a line under each turn, commands and keys. Stores are GitHub repositories (owner/repo) or local
folders; the default store is ` + addons.DefaultStore + `.

  gerdr addon store                    # what the stores offer (never-downloaded stores are fetched first)
  gerdr addon store refresh            # download the stores again
  gerdr addon store add owner/repo     # or an absolute folder path
  gerdr addon info cache-clock         # details and README
  gerdr addon install cache-clock      # also updates an installed one
  gerdr addon list                     # installed, with "update" when the store has a newer copy
  gerdr addon update                   # every add-on with an update
  gerdr addon config cache-clock warnMinutes=3
  gerdr addon disable cache-clock
  gerdr addon rm cache-clock

Writing your own add-on: read <GERDR_DIR>/addons/GUIDE.md.`,
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "Print the installed add-ons (on / off, store, available update, service status, errors)",
		Args:  cobra.NoArgs,
		Run: func(_ *cobra.Command, _ []string) {
			m := openAddons()
			idx := storeIndex(m.Stores())
			live := liveAddons()
			out := []addonView{}
			for _, a := range m.Describe() {
				v := viewAddon(a, idx)
				if l, ok := live[a.ID]; ok {
					v.Service = l.Service
					if v.Error == "" {
						v.Error = l.Error // build errors only the running gerdr knows
					}
				}
				out = append(out, v)
			}
			printJSON(out)
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "info <id>",
		Short: "Print one add-on: installed copy, settings, and what each store offers (with its README)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			m := openAddons()
			fetchNew(m)
			id := a[0]
			out := map[string]any{"id": id}
			stores := m.Stores()
			idx := storeIndex(stores)
			if inst, err := findAddon(m, id); err == nil {
				out["installed"] = viewAddon(inst, idx)
				out["settings"] = viewSettings(inst)
			}
			var offers []map[string]any
			for _, s := range stores {
				if sa, ok := idx[id][s.Source]; ok {
					offers = append(offers, map[string]any{"store": s.Source, "name": sa.Name, "version": sa.Version,
						"description": sa.Description, "hasService": sa.HasService, "readme": sa.Readme})
				}
			}
			if offers != nil {
				out["stores"] = offers
			}
			if len(out) == 1 {
				return fmt.Errorf("no add-on %q installed or in a store (gerdr addon store)", id)
			}
			printJSON(out)
			return nil
		},
	})

	var store string
	var force bool
	inst := &cobra.Command{
		Use:   "install <id>…",
		Short: "Install add-ons from a store, or update installed ones to the store's copy",
		Long: `Install add-ons from a store, or update installed ones to the store's copy.

Refused (conflict) when a folder of that name exists that didn't come from a store, or was edited after installing:
--force overwrites it — ask the user first, it throws their changes away. An add-on's data (addon-data/<id>) and
settings are kept across updates.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			m := openAddons()
			fetchNew(m)
			var done []map[string]any
			for _, id := range a {
				r, err := install(m, id, store, force)
				if err != nil {
					if len(done) > 0 {
						printJSON(map[string]any{"ok": false, "installed": done})
					}
					return fmt.Errorf("%s: %w", id, err)
				}
				done = append(done, r)
			}
			printJSON(map[string]any{"ok": true, "installed": done})
			return nil
		},
	}
	inst.Flags().StringVar(&store, "store", "", "the store to install from, when several offer the id")
	inst.Flags().BoolVar(&force, "force", false, "overwrite a folder that didn't come from a store or was changed locally")
	cmd.AddCommand(inst)

	var updForce bool
	upd := &cobra.Command{
		Use:   "update [id…]",
		Short: "Update add-ons whose store has a different copy (all of them without ids); download the stores first",
		RunE: func(_ *cobra.Command, a []string) error {
			m := openAddons()
			for _, s := range m.Stores() {
				if err := m.RefreshStore(context.Background(), s.Source); err != nil {
					fmt.Fprintf(os.Stderr, "note: refreshing store %s failed (using the last download): %v\n", s.Source, err)
				}
			}
			idx := storeIndex(m.Stores())
			updated, skipped := []map[string]any{}, []map[string]any{}
			for _, ad := range m.Describe() {
				if len(a) > 0 && !slices.Contains(a, ad.ID) {
					continue
				}
				if ad.Source == nil {
					if len(a) > 0 {
						skipped = append(skipped, map[string]any{"id": ad.ID, "why": "not installed from a store"})
					}
					continue
				}
				if updateOf(ad, idx) == "" {
					continue
				}
				if ad.Modified && !updForce {
					skipped = append(skipped, map[string]any{"id": ad.ID, "why": "changed locally; --force throws the changes away (ask the user)"})
					continue
				}
				from := ad.Version
				if err := m.InstallFromStore(ad.Source.Store, ad.ID, updForce); err != nil {
					skipped = append(skipped, map[string]any{"id": ad.ID, "why": err.Error()})
					continue
				}
				updated = append(updated, map[string]any{"id": ad.ID, "from": from, "to": idx[ad.ID][ad.Source.Store].Version})
			}
			for _, id := range a {
				if _, err := findAddon(m, id); err != nil {
					skipped = append(skipped, map[string]any{"id": id, "why": "not installed"})
				}
			}
			printJSON(map[string]any{"updated": updated, "skipped": skipped})
			return nil
		},
	}
	upd.Flags().BoolVar(&updForce, "force", false, "also update add-ons changed locally (their changes are lost)")
	cmd.AddCommand(upd)

	cmd.AddCommand(&cobra.Command{
		Use:     "rm <id>",
		Aliases: []string{"uninstall"},
		Short:   "Uninstall an add-on (deletes its folder; its data in addon-data/<id> stays)",
		Args:    cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			m := openAddons()
			if _, err := findAddon(m, a[0]); err != nil {
				return err
			}
			if err := m.Uninstall(a[0]); err != nil {
				return err
			}
			printJSON(map[string]any{"ok": true, "removed": a[0]})
			return nil
		},
	})

	for _, on := range []bool{true, false} {
		use := map[bool]string{true: "enable", false: "disable"}[on]
		cmd.AddCommand(&cobra.Command{
			Use:   use + " <id>",
			Short: map[bool]string{true: "Switch an add-on on", false: "Switch an add-on off (unloaded everywhere, its service stopped; files and settings stay)"}[on],
			Args:  cobra.ExactArgs(1),
			RunE: func(_ *cobra.Command, a []string) error {
				m := openAddons()
				if _, err := findAddon(m, a[0]); err != nil {
					return err
				}
				if err := m.SetEnabled(a[0], on); err != nil {
					return err
				}
				printJSON(map[string]any{"ok": true, "id": a[0], "enabled": on})
				return nil
			},
		})
	}

	var unset []string
	conf := &cobra.Command{
		Use:   "config <id> [key=value…]",
		Short: "Print an add-on's settings, or change them (key=value; --unset key goes back to the default)",
		Long: `Print an add-on's settings (the form under it in Settings → Add-ons), or change them.

Values are checked against the add-on's addon.json: number, boolean (true / false), select (one of the options'
values), color (#rrggbb), string, secret. Secrets print as <hidden>; put in only what the user hands you.
Running add-ons read the new values right away.

  gerdr addon config cache-clock
  gerdr addon config cache-clock warnMinutes=3 showTime=false
  gerdr addon config cache-clock --unset warnMinutes`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			m := openAddons()
			ad, err := findAddon(m, a[0])
			if err != nil {
				return err
			}
			if len(a) == 1 && len(unset) == 0 {
				printJSON(map[string]any{"id": ad.ID, "settings": viewSettings(ad)})
				return nil
			}
			if len(ad.Settings) == 0 {
				return fmt.Errorf("%s has no settings", ad.ID)
			}
			spec := func(k string) (addons.SettingSpec, error) {
				var keys []string
				for _, s := range ad.Settings {
					if s.Key == k {
						return s, nil
					}
					keys = append(keys, s.Key)
				}
				return addons.SettingSpec{}, fmt.Errorf("%s has no setting %q (it has %s)", ad.ID, k, strings.Join(keys, ", "))
			}
			values := map[string]any{}
			for _, kv := range a[1:] {
				k, raw, ok := strings.Cut(kv, "=")
				if !ok {
					return fmt.Errorf("give key=value, not %q", kv)
				}
				s, err := spec(k)
				if err != nil {
					return err
				}
				if values[k], err = parseSetting(s, raw); err != nil {
					return err
				}
			}
			for _, k := range unset {
				if _, err := spec(k); err != nil {
					return err
				}
				values[k] = nil
			}
			if err := m.SetSettings(ad.ID, values); err != nil {
				return err
			}
			ad, _ = findAddon(m, ad.ID)
			printJSON(map[string]any{"ok": true, "id": ad.ID, "settings": viewSettings(ad)})
			return nil
		},
	}
	conf.Flags().StringArrayVar(&unset, "unset", nil, "a key to put back to its default (repeatable)")
	cmd.AddCommand(conf)

	cmd.AddCommand(storeCmd())
	return cmd
}

func storeCmd() *cobra.Command {
	type offer struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Version     string `json:"version,omitempty"`
		Description string `json:"description,omitempty"`
		HasService  bool   `json:"hasService,omitempty"`
		Installed   string `json:"installed,omitempty"` // the installed version ("yes" without one)
		Update      bool   `json:"update,omitempty"`    // installed from this store, and the store's copy differs
	}
	type view struct {
		Source    string  `json:"source"`
		Default   bool    `json:"default,omitempty"`
		Local     bool    `json:"local,omitempty"`
		FetchedAt string  `json:"fetchedAt,omitempty"`
		Error     string  `json:"error,omitempty"`
		Addons    []offer `json:"addons"`
	}
	list := func(m *addons.Manager) {
		installed := map[string]*addons.Addon{}
		for _, a := range m.Describe() {
			installed[a.ID] = a
		}
		out := []view{}
		for _, s := range m.Stores() {
			v := view{Source: s.Source, Default: s.Default, Local: s.Local, Error: s.Error, Addons: []offer{}}
			if s.FetchedAt > 0 && !s.Local {
				v.FetchedAt = time.UnixMilli(s.FetchedAt).Format(time.RFC3339)
			}
			for _, a := range s.Addons {
				o := offer{ID: a.ID, Name: a.Name, Version: a.Version, Description: a.Description, HasService: a.HasService}
				if in, ok := installed[a.ID]; ok {
					o.Installed = in.Version
					if o.Installed == "" {
						o.Installed = "yes"
					}
					o.Update = in.Source != nil && in.Source.Store == s.Source && in.Source.Hash != a.Hash
				}
				v.Addons = append(v.Addons, o)
			}
			out = append(out, v)
		}
		printJSON(out)
	}

	cmd := &cobra.Command{
		Use:   "store",
		Short: "Print what the stores offer (from the last download); subcommands add / remove / refresh stores",
		Args:  cobra.NoArgs,
		Run: func(_ *cobra.Command, _ []string) {
			m := openAddons()
			fetchNew(m)
			list(m)
		},
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "add <owner/repo[#ref] | github URL | /abs/folder>",
		Short: "Add a store and download it",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			m := openAddons()
			src, err := m.AddStore(context.Background(), a[0])
			if src == "" {
				return err
			}
			if err != nil {
				return fmt.Errorf("added %s, but downloading it failed: %w", src, err)
			}
			list(m)
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "rm <source>",
		Short: "Forget a store (what was installed from it stays installed)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			m := openAddons()
			src := a[0]
			if n, err := addons.NormalizeSource(src); err == nil {
				src = n
			}
			if !slices.ContainsFunc(m.Stores(), func(s addons.StoreView) bool { return s.Source == src }) {
				return fmt.Errorf("no store %q (gerdr addon store)", a[0])
			}
			if err := m.RemoveStore(src); err != nil {
				return err
			}
			printJSON(map[string]any{"ok": true, "removed": src})
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "refresh [source]",
		Short: "Download the stores again (all of them without a source)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			m := openAddons()
			only := ""
			if len(a) > 0 {
				only = a[0]
				if n, err := addons.NormalizeSource(only); err == nil {
					only = n
				}
				if !slices.ContainsFunc(m.Stores(), func(s addons.StoreView) bool { return s.Source == only }) {
					return fmt.Errorf("no store %q (gerdr addon store)", a[0])
				}
			}
			var failed []string
			for _, s := range m.Stores() {
				if only != "" && s.Source != only {
					continue
				}
				if err := m.RefreshStore(context.Background(), s.Source); err != nil {
					failed = append(failed, fmt.Sprintf("%s: %v", s.Source, err))
				}
			}
			list(m)
			if len(failed) > 0 {
				return fmt.Errorf("refresh failed (the last download is kept): %s", strings.Join(failed, "; "))
			}
			return nil
		},
	})
	return cmd
}
