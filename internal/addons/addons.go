// Package addons manages the user's add-ons in ~/.gerdr/addons: one folder per add-on.
//
// Why "add-on" and not "plugin": Claude Code and Codex both have plugins already (skills, MCP, hooks of the agent
// itself), shown in the same settings; a third thing called plugin would be confused with them. Add-ons extend gerdr,
// the shell around the agents: tab names, pane titles, commands and keys, a process that keeps running.
//
// Why folders: an add-on may need more than one file (several modules, an image, a sound, a service binary), and a
// store installs and updates a folder as a unit.
//
//	~/.gerdr/addons/<id>/addon.json   optional: name, version, description, icon, main, service
//	~/.gerdr/addons/<id>/index.ts     the UI half (or "main"), bundled with everything it imports
//
// The TS half **runs in the UI** (it changes UI things), so all this package does for it is turn TS into JS with
// esbuild (the Go one, no node needed); what an add-on can touch is the API object the UI hands it (gerdr.d.ts).
// A service (addon.json "service") is a process the backend keeps running (service.go). Stores: store.go.
package addons

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/evanw/esbuild/pkg/api"
)

//go:embed builtin/*
var builtin embed.FS

// ManifestFile is the add-on's manifest, in its folder. Optional for an add-on that is just index.ts.
const ManifestFile = "addon.json"

// Manifest is addon.json.
type Manifest struct {
	Name        string `json:"name,omitempty"` // shown in Settings; the folder name when empty
	Version     string `json:"version,omitempty"`
	Description string `json:"description,omitempty"`
	Icon        string `json:"icon,omitempty"` // a file of the add-on (png / svg …), relative to its folder
	// Screenshots: files of the add-on, shown in the store before installing (a picture says what it does)
	Screenshots []string `json:"screenshots,omitempty"`
	// Settings: what the person can set in Settings → Add-ons, read with g.settings.get(key)
	Settings []SettingSpec `json:"settings,omitempty"`
	Main     string        `json:"main,omitempty"` // the UI half's entry; index.ts / index.tsx / index.js when empty
	Service  *ServiceSpec  `json:"service,omitempty"`
}

func readManifest(dir string) (*Manifest, error) {
	b, err := os.ReadFile(filepath.Join(dir, ManifestFile))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", ManifestFile, err)
	}
	if m.Service != nil && strings.TrimSpace(m.Service.Command) == "" {
		return nil, fmt.Errorf("%s: service.command is empty", ManifestFile)
	}
	if m.Main != "" {
		if _, err := within(dir, m.Main); err != nil {
			return nil, fmt.Errorf("%s: main: %w", ManifestFile, err)
		}
	}
	return &m, nil
}

// entry is the UI half's entry file in dir, "" when there is none.
func entry(dir string, mf *Manifest) string {
	if mf != nil && mf.Main != "" {
		return filepath.Join(dir, filepath.FromSlash(mf.Main))
	}
	for _, idx := range []string{"index.ts", "index.tsx", "index.js"} {
		if _, err := os.Stat(filepath.Join(dir, idx)); err == nil {
			return filepath.Join(dir, idx)
		}
	}
	return ""
}

// Addon is one installed add-on as the UI gets it.
type Addon struct {
	ID          string `json:"id"`
	Dir         string `json:"dir"`
	Path        string `json:"path,omitempty"` // the UI half's entry; empty for a service-only add-on
	Name        string `json:"name"`
	Version     string `json:"version,omitempty"`
	Description string `json:"description,omitempty"`
	Icon        string `json:"icon,omitempty"`
	HasService  bool   `json:"hasService,omitempty"`
	// Settings the add-on declares, and what was set (only keys that were changed from the default)
	Settings      []SettingSpec  `json:"settings,omitempty"`
	SettingValues map[string]any `json:"settingValues,omitempty"`
	Disabled      bool           `json:"disabled,omitempty"`
	// Source: installed from a store; Modified: its files changed since (an update would overwrite that).
	Source   *Installed `json:"source,omitempty"`
	Modified bool       `json:"modified,omitempty"`
	Code     string     `json:"code,omitempty"`
	Error    string     `json:"error,omitempty"`
	Hash     string     `json:"hash"` // changes when Code / Error / Disabled do: the UI reloads the add-on then
	Mtime    int64      `json:"mtime"`

	manifest *Manifest
}

type Manager struct {
	root string // gerdr's data directory
	dir  string // root/addons
	log  *slog.Logger

	mu    sync.Mutex
	cache map[string]*Addon
	sig   string

	stateMu sync.Mutex // the state file: read-modify-write
	fetchMu sync.Mutex // one store download at a time
}

// New: root is gerdr's data directory; add-ons live in root/addons.
func New(root string, log *slog.Logger) *Manager {
	return &Manager{root: root, dir: filepath.Join(root, "addons"), log: log, cache: map[string]*Addon{}}
}

func (m *Manager) Dir() string { return m.dir }

// Install puts the API declarations and the guide (gerdr.d.ts, tsconfig.json, GUIDE.md) into the add-on directory,
// refreshed on every start: they describe this version's API. README.md only when missing (someone may have edited it).
func (m *Manager) Install() error {
	if err := os.MkdirAll(m.dir, 0o755); err != nil {
		return err
	}
	return fs.WalkDir(builtin, "builtin", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		name := strings.TrimPrefix(p, "builtin/")
		dst := filepath.Join(m.dir, name)
		if name == "README.md" {
			if _, err := os.Stat(dst); err == nil {
				return nil
			}
		}
		b, _ := builtin.ReadFile(p)
		if old, err := os.ReadFile(dst); err == nil && string(old) == string(b) {
			return nil // unchanged: don't touch the mtime (the watcher would reload every window)
		}
		return os.WriteFile(dst, b, 0o644)
	})
}

// ids: the add-on folders. A folder counts when it has addon.json or an index.(ts|tsx|js). Names starting with . or
// _ are skipped (_x is how one is switched off by hand; the UI's switch is the state file instead).
func (m *Manager) ids() []string {
	var out []string
	ents, err := os.ReadDir(m.dir)
	if err != nil {
		return nil
	}
	for _, e := range ents {
		name := e.Name()
		if !e.IsDir() || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || name == "node_modules" {
			continue
		}
		full := filepath.Join(m.dir, name)
		for _, f := range []string{ManifestFile, "index.ts", "index.tsx", "index.js"} {
			if _, err := os.Stat(filepath.Join(full, f)); err == nil {
				out = append(out, name)
				break
			}
		}
	}
	return out
}

// signature: every file's mtime + size, and the state file (switching one off reloads it). The directory is small;
// scanning it every second costs nothing.
func (m *Manager) signature() string {
	var parts []string
	_ = filepath.WalkDir(m.dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && (d.Name() == "node_modules" || d.Name() == ".git") {
			return filepath.SkipDir
		}
		if d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			parts = append(parts, fmt.Sprintf("%s:%d:%d", p, info.ModTime().UnixNano(), info.Size()))
		}
		return nil
	})
	if info, err := os.Stat(m.statePath()); err == nil {
		parts = append(parts, fmt.Sprintf("state:%d:%d", info.ModTime().UnixNano(), info.Size()))
	}
	sort.Strings(parts)
	h := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(h[:8])
}

// List returns every add-on, built (rebuilt when a file changed).
func (m *Manager) List() []*Addon {
	m.mu.Lock()
	defer m.mu.Unlock()
	sig := m.signature()
	if sig != m.sig {
		m.rebuild()
		m.sig = sig
	}
	var out []*Addon
	for _, a := range m.cache {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (m *Manager) rebuild() {
	st := m.readState()
	m.cache = map[string]*Addon{}
	for _, id := range m.ids() {
		dir := filepath.Join(m.dir, id)
		a := &Addon{ID: id, Dir: dir, Name: id, Disabled: st.disabled(id)}
		if rec, ok := st.Installed[id]; ok {
			r := rec
			a.Source = &r
			a.Modified = hashDir(dir) != rec.Hash
		}
		mf, err := readManifest(dir)
		if mf != nil {
			a.manifest = mf
			if mf.Name != "" {
				a.Name = mf.Name
			}
			a.Version, a.Description, a.Icon, a.HasService = mf.Version, mf.Description, mf.Icon, mf.Service != nil
			a.Settings = mf.Settings
			a.SettingValues = st.Settings[id]
		}
		a.Path = entry(dir, mf)
		if p := a.Path; p != "" {
			if info, err := os.Stat(p); err == nil {
				a.Mtime = info.ModTime().UnixMilli()
			}
		}
		switch {
		case err != nil:
			a.Error = err.Error() // a broken addon.json: say so instead of loading half an add-on
		case a.Disabled || a.Path == "":
			// off, or a service only: nothing to build
		default:
			a.Code, a.Error = m.build(id, a.Path)
		}
		h := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%v\x00%s", a.Code, a.Error, a.Disabled, a.Path)))
		a.Hash = hex.EncodeToString(h[:8])
		m.cache[id] = a
	}
}

func (m *Manager) build(id, path string) (code, errText string) {
	res := api.Build(api.BuildOptions{
		EntryPoints:   []string{path},
		Bundle:        true,
		Write:         false,
		Format:        api.FormatESModule,
		Platform:      api.PlatformBrowser,
		Target:        api.ES2022,
		Sourcemap:     api.SourceMapInline,
		SourceRoot:    "gerdr-addon://" + id + "/",
		JSX:           api.JSXTransform,
		JSXFactory:    "__gerdrH", // set by ui/src/lib/addons.ts (React.createElement)
		JSXFragment:   "__gerdrFragment",
		LogLevel:      api.LogLevelSilent,
		AbsWorkingDir: filepath.Dir(path),
		TsconfigRaw:   `{"compilerOptions":{"target":"es2022","useDefineForClassFields":true}}`,
	})
	if len(res.Errors) > 0 {
		var msgs []string
		for _, e := range res.Errors {
			loc := ""
			if e.Location != nil {
				loc = fmt.Sprintf("%s:%d:%d: ", e.Location.File, e.Location.Line, e.Location.Column)
			}
			msgs = append(msgs, loc+e.Text)
		}
		return "", strings.Join(msgs, "\n")
	}
	if len(res.OutputFiles) > 0 {
		return string(res.OutputFiles[0].Contents), ""
	}
	return "", ""
}

// Watch looks at the add-on directory every second and calls onChange when it changed (the UI then fetches the
// add-ons again; services are synced).
func (m *Manager) Watch(onChange func()) {
	go func() {
		m.mu.Lock()
		last := m.signature()
		m.mu.Unlock()
		for {
			time.Sleep(time.Second)
			m.mu.Lock()
			sig := m.signature()
			m.mu.Unlock()
			if sig != last {
				last = sig
				m.log.Info("addons changed")
				onChange()
			}
		}
	}()
}

type serviceEntry struct {
	dir  string
	spec ServiceSpec
}

// services: the enabled add-ons whose addon.json declares a service (a broken addon.json declares none; List says why).
func (m *Manager) services() map[string]serviceEntry {
	st := m.readState()
	out := map[string]serviceEntry{}
	for _, id := range m.ids() {
		if st.disabled(id) {
			continue
		}
		dir := filepath.Join(m.dir, id)
		if mf, err := readManifest(dir); err == nil && mf != nil && mf.Service != nil {
			out[id] = serviceEntry{dir: dir, spec: *mf.Service}
		}
	}
	return out
}

// File resolves a file of an installed add-on (its icon, an image its UI shows), refusing anything outside its folder.
func (m *Manager) File(id, rel string) (string, error) {
	if !validID(id) {
		return "", errors.New("bad add-on id")
	}
	return within(filepath.Join(m.dir, id), rel)
}

// within joins rel to dir, refusing a path that leaves dir (.., absolute, a symlink pointing out).
func within(dir, rel string) (string, error) {
	if rel == "" || filepath.IsAbs(rel) || strings.HasPrefix(rel, "/") {
		return "", errors.New("bad path")
	}
	p := filepath.Join(dir, filepath.FromSlash(rel))
	r, err := filepath.Rel(dir, p)
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", errors.New("path outside the add-on")
	}
	if real, err := filepath.EvalSymlinks(p); err == nil {
		if rd, err := filepath.EvalSymlinks(dir); err == nil {
			if r, err := filepath.Rel(rd, real); err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
				return "", errors.New("path outside the add-on")
			}
		}
	}
	return p, nil
}

// validID: a folder name we are willing to create or delete under the add-on directory.
func validID(id string) bool {
	if id == "" || len(id) > 64 || strings.HasPrefix(id, ".") || strings.HasPrefix(id, "_") {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return true
}

// hashDir: the content of a folder (relative paths and bytes, .git and node_modules left out). Tells whether an
// installed add-on still is what the store gave, and whether the store has something newer.
func hashDir(dir string) string {
	h := sha256.New()
	var files []string
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "node_modules") {
			return filepath.SkipDir
		}
		if d.Type().IsRegular() {
			files = append(files, p)
		}
		return nil
	})
	sort.Strings(files)
	for _, p := range files {
		rel, _ := filepath.Rel(dir, p)
		fmt.Fprintf(h, "%s\x00", filepath.ToSlash(rel))
		if f, err := os.Open(p); err == nil {
			_, _ = io.Copy(h, f)
			f.Close()
		}
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)[:12])
}
