package addons

// Stores: where add-ons are installed from. A store is a GitHub repository (or, for writing one, a local folder) whose
// folders holding an addon.json are add-ons:
//
//	<repo>/duplicate-tab/addon.json, index.ts …
//	<repo>/addons/band/addon.json …          (one level deeper works too)
//
// No index file to keep in sync: refreshing downloads the repository's tarball (one request to codeload, which has no
// API rate limit) and unpacks it into the cache, so browsing the store, its icons and READMEs, works offline and costs
// nothing until the next manual refresh. Installing copies a folder from that cache into ~/.gerdr/addons.
//
// Borrowed from Claude Code's plugin marketplaces (owner/repo as the address, a folder per plugin), without their
// index file: a folder is listed by having an addon.json.

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

// ErrConflict: installing would overwrite an add-on that didn't come from this store, or one changed locally.
var ErrConflict = errors.New("conflict")

// StoreAddon is one add-on a store offers.
type StoreAddon struct {
	ID          string `json:"id"`
	Path        string `json:"path"` // its folder in the store, slash-separated
	Name        string `json:"name"`
	Version     string `json:"version,omitempty"`
	Description string `json:"description,omitempty"`
	Icon        string `json:"icon,omitempty"`
	Readme      string `json:"readme,omitempty"` // README.md of its folder
	HasService  bool   `json:"hasService,omitempty"`
	Hash        string `json:"hash"`
}

// StoreView is a store as Settings → Add-ons → Store shows it.
type StoreView struct {
	Source    string       `json:"source"`
	Local     bool         `json:"local,omitempty"` // a folder on this machine: read as it is, no download
	Default   bool         `json:"default,omitempty"`
	FetchedAt int64        `json:"fetchedAt,omitempty"` // unix ms; 0 = never downloaded
	Error     string       `json:"error,omitempty"`     // why the last refresh failed (what was fetched before stays)
	Addons    []StoreAddon `json:"addons"`
}

var ghRE = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9-]*)/([A-Za-z0-9._-]+?)(?:\.git)?(?:#([A-Za-z0-9._/-]+))?$`)

// NormalizeSource: "owner/repo", "owner/repo#ref", a github.com URL, or an absolute (or ~) folder path.
func NormalizeSource(s string) (string, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			s = filepath.Join(home, s[2:])
		}
	}
	if filepath.IsAbs(s) {
		if st, err := os.Stat(s); err != nil || !st.IsDir() {
			return "", fmt.Errorf("%s is not a folder", s)
		}
		return filepath.Clean(s), nil
	}
	for _, p := range []string{"https://github.com/", "http://github.com/", "github.com/"} {
		s = strings.TrimPrefix(s, p)
	}
	s = strings.TrimSuffix(s, "/")
	g := ghRE.FindStringSubmatch(s)
	if g == nil {
		return "", fmt.Errorf("%q: give a GitHub repository as owner/repo", s)
	}
	s = g[1] + "/" + g[2]
	if g[3] != "" {
		s += "#" + g[3]
	}
	return s, nil
}

func isLocal(source string) bool { return filepath.IsAbs(source) }

func storeKey(source string) string {
	h := sha256.Sum256([]byte(source))
	clean := regexp.MustCompile(`[^A-Za-z0-9._-]+`).ReplaceAllString(source, "_")
	if len(clean) > 40 {
		clean = clean[len(clean)-40:]
	}
	return strings.Trim(clean, "_") + "-" + hex.EncodeToString(h[:4])
}

func (m *Manager) storeCache(source string) string {
	return filepath.Join(m.root, "cache", "addon-stores", storeKey(source))
}

// storeRoot: where a store's files are on disk (the folder itself for a local store).
func (m *Manager) storeRoot(source string) string {
	if isLocal(source) {
		return source
	}
	return filepath.Join(m.storeCache(source), "repo")
}

type storeMeta struct {
	FetchedAt int64  `json:"fetchedAt,omitempty"`
	Error     string `json:"error,omitempty"`
}

func (m *Manager) readMeta(source string) storeMeta {
	var mt storeMeta
	if b, err := os.ReadFile(filepath.Join(m.storeCache(source), "meta.json")); err == nil {
		_ = json.Unmarshal(b, &mt)
	}
	return mt
}

func (m *Manager) writeMeta(source string, mt storeMeta) {
	_ = os.MkdirAll(m.storeCache(source), 0o755)
	b, _ := json.Marshal(mt)
	_ = os.WriteFile(filepath.Join(m.storeCache(source), "meta.json"), b, 0o644)
}

// Stores lists the stores and what each offers, from the cache (no network).
func (m *Manager) Stores() []StoreView {
	out := []StoreView{}
	for _, src := range m.readState().stores() {
		v := StoreView{Source: src, Local: isLocal(src), Default: src == DefaultStore, Addons: []StoreAddon{}}
		mt := m.readMeta(src)
		v.FetchedAt, v.Error = mt.FetchedAt, mt.Error
		if v.Local {
			v.FetchedAt = time.Now().UnixMilli()
		}
		v.Addons = scanStore(m.storeRoot(src))
		out = append(out, v)
	}
	return out
}

// scanStore: the folders (one or two levels down) that hold an addon.json. The first folder of a name wins.
func scanStore(root string) []StoreAddon {
	out := []StoreAddon{}
	seen := map[string]bool{}
	var walk func(dir, rel string, depth int)
	walk = func(dir, rel string, depth int) {
		ents, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range ents {
			name := e.Name()
			if !e.IsDir() || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || name == "node_modules" {
				continue
			}
			full := filepath.Join(dir, name)
			r := name
			if rel != "" {
				r = rel + "/" + name
			}
			mf, err := readManifest(full)
			if err != nil || mf == nil {
				if depth < 2 {
					walk(full, r, depth+1)
				}
				continue
			}
			if seen[name] || !validID(name) {
				continue
			}
			seen[name] = true
			a := StoreAddon{ID: name, Path: r, Name: name, Version: mf.Version, Description: mf.Description, Icon: mf.Icon,
				HasService: mf.Service != nil, Hash: hashDir(full)}
			if mf.Name != "" {
				a.Name = mf.Name
			}
			if b, err := os.ReadFile(filepath.Join(full, "README.md")); err == nil && len(b) < 64<<10 {
				a.Readme = string(b)
			}
			out = append(out, a)
		}
	}
	walk(root, "", 1)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// AddStore adds a store and downloads it.
func (m *Manager) AddStore(ctx context.Context, source string) (string, error) {
	src, err := NormalizeSource(source)
	if err != nil {
		return "", err
	}
	if err := m.updateState(func(st *state) error {
		list := st.stores()
		if !slices.Contains(list, src) {
			list = append(slices.Clone(list), src)
		}
		st.Stores = &list
		return nil
	}); err != nil {
		return "", err
	}
	return src, m.RefreshStore(ctx, src)
}

// RemoveStore forgets a store and its cache. What was installed from it stays installed.
func (m *Manager) RemoveStore(source string) error {
	if err := m.updateState(func(st *state) error {
		list := slices.DeleteFunc(slices.Clone(st.stores()), func(s string) bool { return s == source })
		st.Stores = &list
		return nil
	}); err != nil {
		return err
	}
	if !isLocal(source) {
		_ = os.RemoveAll(m.storeCache(source))
	}
	return nil
}

func (m *Manager) hasStore(source string) bool { return slices.Contains(m.readState().stores(), source) }

// RefreshStore downloads a store again. A failed download keeps what was there and records why.
func (m *Manager) RefreshStore(ctx context.Context, source string) error {
	if !m.hasStore(source) {
		return fmt.Errorf("no store %q", source)
	}
	if isLocal(source) {
		return nil
	}
	m.fetchMu.Lock()
	defer m.fetchMu.Unlock()
	mt := m.readMeta(source)
	err := m.download(ctx, source)
	if err != nil {
		mt.Error = err.Error()
	} else {
		mt.Error, mt.FetchedAt = "", time.Now().UnixMilli()
	}
	m.writeMeta(source, mt)
	return err
}

// Limits for a store's tarball: it is a handful of small folders; anything far bigger is a mistake.
const (
	maxStoreBytes = 100 << 20
	maxStoreFile  = 20 << 20
)

var storeClient = &http.Client{Timeout: 2 * time.Minute}

func (m *Manager) download(ctx context.Context, source string) error {
	g := ghRE.FindStringSubmatch(source)
	if g == nil {
		return fmt.Errorf("bad store %q", source)
	}
	ref := g[3]
	if ref == "" {
		ref = "HEAD"
	}
	url := fmt.Sprintf("https://codeload.github.com/%s/%s/tar.gz/%s", g[1], g[2], ref)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	resp, err := storeClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return fmt.Errorf("%s/%s not found (a private repository can't be used as a store yet)", g[1], g[2])
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("download %s: %s", url, resp.Status)
	}
	cache := m.storeCache(source)
	if err := os.MkdirAll(cache, 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(cache, ".dl-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if err := untar(resp.Body, tmp); err != nil {
		return err
	}
	old := filepath.Join(cache, "repo.old")
	_ = os.RemoveAll(old)
	dst := filepath.Join(cache, "repo")
	if _, err := os.Stat(dst); err == nil {
		if err := os.Rename(dst, old); err != nil {
			return err
		}
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Rename(old, dst)
		return err
	}
	_ = os.RemoveAll(old)
	return nil
}

// untar unpacks a GitHub tarball into dst, dropping its top folder (<repo>-<sha>/). Regular files and folders only:
// no links, nothing outside dst.
func untar(r io.Reader, dst string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	var total int64
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name := filepath.ToSlash(h.Name)
		if i := strings.IndexByte(name, '/'); i >= 0 {
			name = name[i+1:]
		} else {
			continue
		}
		if name == "" {
			continue
		}
		p, err := within(dst, name)
		if err != nil {
			continue
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(p, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if h.Size > maxStoreFile {
				continue
			}
			total += h.Size
			if total > maxStoreBytes {
				return errors.New("store is too big (over 100MB)")
			}
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return err
			}
			mode := os.FileMode(0o644)
			if h.Mode&0o111 != 0 {
				mode = 0o755 // a service's script or binary stays runnable
			}
			f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				return err
			}
			_, err = io.Copy(f, io.LimitReader(tr, h.Size))
			f.Close()
			if err != nil {
				return err
			}
		}
	}
}

func (m *Manager) storeAddon(source, id string) (StoreAddon, error) {
	if !m.hasStore(source) {
		return StoreAddon{}, fmt.Errorf("no store %q", source)
	}
	for _, a := range scanStore(m.storeRoot(source)) {
		if a.ID == id {
			return a, nil
		}
	}
	return StoreAddon{}, fmt.Errorf("store %s has no add-on %q (refresh it?)", source, id)
}

// StoreFile resolves a file of an add-on in a store (its icon in the store list).
func (m *Manager) StoreFile(source, id, rel string) (string, error) {
	a, err := m.storeAddon(source, id)
	if err != nil {
		return "", err
	}
	return within(filepath.Join(m.storeRoot(source), filepath.FromSlash(a.Path)), rel)
}

// InstallFromStore copies an add-on from a store's cache into the add-on directory, replacing the installed copy
// when it came from a store and wasn't changed since. Otherwise (a folder of that name someone made, or one edited
// after installing) it returns ErrConflict unless force.
func (m *Manager) InstallFromStore(source, id string, force bool) error {
	a, err := m.storeAddon(source, id)
	if err != nil {
		return err
	}
	dst := filepath.Join(m.dir, id)
	if _, err := os.Stat(dst); err == nil && !force {
		rec, ok := m.readState().Installed[id]
		switch {
		case !ok:
			return fmt.Errorf("%w: %s already exists and didn't come from a store", ErrConflict, dst)
		case hashDir(dst) != rec.Hash:
			return fmt.Errorf("%w: %s was changed after it was installed", ErrConflict, dst)
		}
	}
	if err := os.MkdirAll(m.dir, 0o755); err != nil {
		return err
	}
	// Copy next to it under a dot name (skipped by the scanner), then swap: a window never sees half an add-on.
	tmp, err := os.MkdirTemp(m.dir, ".install-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if err := copyTree(filepath.Join(m.storeRoot(source), filepath.FromSlash(a.Path)), tmp); err != nil {
		return err
	}
	hash := hashDir(tmp)
	old := filepath.Join(m.dir, ".old-"+id)
	_ = os.RemoveAll(old)
	if _, err := os.Stat(dst); err == nil {
		if err := os.Rename(dst, old); err != nil {
			return err
		}
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Rename(old, dst)
		return err
	}
	_ = os.RemoveAll(old)
	return m.recordInstall(id, Installed{Store: source, Version: a.Version, Hash: hash})
}

// Uninstall deletes an add-on's folder (its data in addon-data stays: installing it again picks it up).
func (m *Manager) Uninstall(id string) error {
	if !validID(id) {
		return errors.New("bad add-on id")
	}
	dir := filepath.Join(m.dir, id)
	if _, err := os.Stat(dir); err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	return m.updateState(func(st *state) error {
		delete(st.Installed, id)
		st.Disabled = slices.DeleteFunc(st.Disabled, func(x string) bool { return x == id })
		return nil
	})
}

// copyTree copies regular files and folders (no links, no .git, no node_modules).
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "node_modules") {
			return filepath.SkipDir
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
		if err != nil {
			return err
		}
		_, err = io.Copy(out, in)
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		return err
	})
}
