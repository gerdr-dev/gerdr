package uisite

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/evanw/esbuild/pkg/api"
)

// Vendor bundle: every third-party specifier the UI imports, bundled into a handful of files.
//
// Why: loading esm.sh's output as is costs one request per file it is split into, and esm.sh splits a lot (every
// micromark-util-* is its own module, every package starts with a stub that only re-exports the real file). Measured
// on gerdr's own UI: 414 /_esm/ requests on a cold load, chained 10+ imports deep because the browser only finds the
// next one after parsing the last. With nothing cached that was 67 s; with gerdr's disk cache warm but 150 ms per
// request (a phone through the relay) still 17 s. Bundled, it is a few dozen requests at most two levels deep.
//
// How: esbuild (already in the binary) bundles the same esm.sh URLs the import map used to point at, fetched through
// the same proxy and disk cache (ESM.get), so there is still no npm and no node_modules. One entry point per
// specifier with code splitting: the import map maps each specifier to its entry chunk, shared code lands in shared
// chunks, and because it is all one build every package gets the same single copy of React (the external=react
// query keeps esm.sh from inlining its own; the bare "react" it leaves behind resolves to our react entry).
//
// It only replaces where the import map points: the UI's own modules and hot reload are untouched. Adding a package
// changes the import map, which already meant a full reload; now the reload lands on a short "preparing" page while
// the new bundle builds (Renderer.build). If the bundle can't be built (a package esm.sh can't serve), the import
// map falls back to the per-file esm.sh URLs, which is how it always worked and where the browser reports which
// package is broken.
//
// Dynamic import() inside a package (shiki's languages and themes) is left out of the bundle and keeps loading per
// file from /_esm/: bundling it would pull hundreds of lazily used files at build time for nothing.

// Bump when the bundling options below change: old bundles get rebuilt.
const vendorVersion = "v2"

const vendorPrefix = "/~vendor/" // under /_esm/; "~" can't start an npm name, so it can't clash with a package

type vendorBuild struct {
	done    chan struct{}
	imports map[string]string // specifier → /_esm/~vendor/<key>/<file>
	// URL → every bundle file it statically pulls in (transitively), for <link rel="modulepreload">: without it the
	// browser only finds a shared chunk after it has parsed the entry that imports it.
	closure map[string][]string
	err     error
	at      time.Time
}

type vendorSet struct {
	mu     sync.Mutex
	builds map[string]*vendorBuild
}

// vendorKey: the bundle is a function of exactly which specifiers map to which esm.sh URLs.
func vendorKey(entries map[string]string) string {
	specs := make([]string, 0, len(entries))
	for s := range entries {
		specs = append(specs, s)
	}
	sort.Strings(specs)
	h := sha256.New()
	h.Write([]byte(vendorVersion + "\n"))
	for _, s := range specs {
		h.Write([]byte(s + "\x00" + entries[s] + "\n"))
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// vendor returns the bundle for these entries (specifier → /_esm/ URL): ready if built before (in this run or on
// disk), else building in the background. A failed build is retried after a minute (esm.sh may have been down).
// deps (package.json) resolves the bare imports esm.sh leaves in its output (react and react-dom subpaths).
func (e *ESM) vendor(entries, deps map[string]string) *vendorBuild {
	key := vendorKey(entries)
	e.vendors.mu.Lock()
	defer e.vendors.mu.Unlock()
	if e.vendors.builds == nil {
		e.vendors.builds = map[string]*vendorBuild{}
	}
	if vb := e.vendors.builds[key]; vb != nil {
		select {
		case <-vb.done:
			if vb.err == nil || time.Since(vb.at) < time.Minute {
				return vb
			}
		default:
			return vb
		}
	}
	vb := &vendorBuild{done: make(chan struct{})}
	e.vendors.builds[key] = vb
	if m, err := e.loadVendor(key); err == nil {
		vb.imports, vb.closure = m.urls(key)
		vb.at = time.Now()
		close(vb.done)
		return vb
	}
	go func() {
		start := time.Now()
		var m *vendorManifest
		if m, vb.err = e.buildVendor(key, entries, deps); vb.err == nil {
			vb.imports, vb.closure = m.urls(key)
		}
		vb.at = time.Now()
		if vb.err == nil {
			e.pruneVendors(key)
			slog.Info("ui vendor bundle built", "key", key, "entries", len(entries), "took", time.Since(start).Round(time.Millisecond))
		} else {
			slog.Warn("ui vendor bundle failed, loading packages per file", "key", key, "err", vb.err)
		}
		close(vb.done)
	}()
	return vb
}

func (e *ESM) vendorDir(key string) string { return filepath.Join(e.dir, "~vendor", key) }

type vendorManifest struct {
	Imports map[string]string   `json:"imports"` // specifier → file name in the bundle's directory
	Static  map[string][]string `json:"static"`  // file → files it imports statically
}

func vendorFileURL(key, f string) string { return "/_esm" + vendorPrefix + key + "/" + f }

func (m *vendorManifest) urls(key string) (imports map[string]string, closure map[string][]string) {
	imports = make(map[string]string, len(m.Imports))
	for spec, f := range m.Imports {
		imports[spec] = vendorFileURL(key, f)
	}
	closure = map[string][]string{}
	for _, f := range m.Imports {
		seen := map[string]bool{}
		var walk func(string)
		walk = func(x string) {
			for _, d := range m.Static[x] {
				if !seen[d] {
					seen[d] = true
					walk(d)
				}
			}
		}
		walk(f)
		var out []string
		for d := range seen {
			out = append(out, vendorFileURL(key, d))
		}
		sort.Strings(out)
		closure[vendorFileURL(key, f)] = out
	}
	return imports, closure
}

func (e *ESM) loadVendor(key string) (*vendorManifest, error) {
	b, err := os.ReadFile(filepath.Join(e.vendorDir(key), "manifest.json"))
	if err != nil {
		return nil, err
	}
	var m vendorManifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

var vendorNameRE = regexp.MustCompile(`[^A-Za-z0-9]+`)

func (e *ESM) buildVendor(key string, entries, deps map[string]string) (*vendorManifest, error) {
	var eps []api.EntryPoint
	byURL := map[string][]string{}
	for spec, u := range entries {
		if byURL[u] == nil {
			eps = append(eps, api.EntryPoint{InputPath: u, OutputPath: strings.Trim(vendorNameRE.ReplaceAllString(spec, "_"), "_")})
		}
		byURL[u] = append(byURL[u], spec)
	}
	sort.Slice(eps, func(i, j int) bool { return eps[i].InputPath < eps[j].InputPath })
	res := api.Build(api.BuildOptions{
		EntryPointsAdvanced: eps,
		Bundle:              true,
		Splitting:           true,
		Format:              api.FormatESModule,
		Platform:            api.PlatformBrowser,
		Target:              api.ES2022,
		Outdir:              "/out",
		EntryNames:          "[name]-[hash]",
		ChunkNames:          "chunk-[hash]",
		MinifyWhitespace:    true, // names and React's dev warnings stay; this is only bytes over the relay
		LegalComments:       api.LegalCommentsNone,
		Charset:             api.CharsetUTF8,
		Metafile:            true,
		Write:               false,
		LogLevel:            api.LogLevelSilent,
		Plugins:             []api.Plugin{e.vendorPlugin(entries, deps)},
	})
	if len(res.Errors) > 0 {
		var msgs []string
		for _, m := range res.Errors {
			msgs = append(msgs, m.Text)
			if len(msgs) == 5 {
				break
			}
		}
		return nil, errors.New(strings.Join(msgs, "; "))
	}
	var meta struct {
		Outputs map[string]struct {
			EntryPoint string `json:"entryPoint"`
			Imports    []struct {
				Path     string `json:"path"`
				Kind     string `json:"kind"`
				External bool   `json:"external"`
			} `json:"imports"`
		} `json:"outputs"`
	}
	if err := json.Unmarshal([]byte(res.Metafile), &meta); err != nil {
		return nil, err
	}
	files := map[string]string{}
	static := map[string][]string{}
	for out, o := range meta.Outputs {
		for _, spec := range byURL[strings.TrimPrefix(o.EntryPoint, "esm:")] {
			files[spec] = path.Base(out)
		}
		for _, im := range o.Imports {
			if im.Kind == "import-statement" && !im.External {
				static[path.Base(out)] = append(static[path.Base(out)], path.Base(im.Path))
			}
		}
	}
	if len(files) != len(entries) {
		return nil, fmt.Errorf("bundle is missing entry points (%d of %d)", len(files), len(entries))
	}
	// Write to a temp dir and rename: a half-written bundle must never look finished
	dir := e.vendorDir(key)
	tmp := dir + ".tmp"
	_ = os.RemoveAll(tmp)
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return nil, err
	}
	for _, f := range res.OutputFiles {
		if err := os.WriteFile(filepath.Join(tmp, path.Base(f.Path)), f.Contents, 0o644); err != nil {
			return nil, err
		}
	}
	m := &vendorManifest{Imports: files, Static: static}
	mb, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(tmp, "manifest.json"), mb, 0o644); err != nil {
		return nil, err
	}
	_ = os.RemoveAll(dir)
	if err := os.Rename(tmp, dir); err != nil {
		return nil, err
	}
	return m, nil
}

// vendorPlugin feeds esbuild from the esm.sh proxy. Module paths are the /_esm/ URLs themselves (namespace "esm").
func (e *ESM) vendorPlugin(entries, deps map[string]string) api.Plugin {
	return api.Plugin{Name: "esm", Setup: func(b api.PluginBuild) {
		b.OnResolve(api.OnResolveOptions{Filter: ".*"}, func(a api.OnResolveArgs) (api.OnResolveResult, error) {
			var u string
			switch {
			case a.Kind == api.ResolveEntryPoint || strings.HasPrefix(a.Path, "/_esm/"):
				u = a.Path
			case strings.HasPrefix(a.Path, "./") || strings.HasPrefix(a.Path, "../"):
				base, _, _ := strings.Cut(a.Importer, "?")
				u = path.Join(path.Dir(base), a.Path)
			case strings.HasPrefix(a.Path, "/"):
				u = "/_esm" + a.Path
			default:
				// Bare: what esm.sh leaves external (react, react-dom and their subpaths). Same mapping as the import map.
				u = entries[a.Path]
				if pkg, sub := packageName(a.Path); u == "" && deps[pkg] != "" {
					u = esmURL(pkg, deps[pkg], sub)
				}
				if u == "" {
					return api.OnResolveResult{}, fmt.Errorf("can't resolve %q (imported by %s)", a.Path, a.Importer)
				}
			}
			if a.Kind == api.ResolveJSDynamicImport {
				return api.OnResolveResult{Path: u, External: true}, nil
			}
			return api.OnResolveResult{Path: u, Namespace: "esm"}, nil
		})
		b.OnLoad(api.OnLoadOptions{Filter: ".*", Namespace: "esm"}, func(a api.OnLoadArgs) (api.OnLoadResult, error) {
			c, err := e.get(strings.TrimPrefix(a.Path, "/_esm"))
			if err != nil {
				return api.OnLoadResult{}, err
			}
			if c.Status != http.StatusOK {
				return api.OnLoadResult{}, fmt.Errorf("%s: HTTP %d", a.Path, c.Status)
			}
			s := string(c.Body)
			loader := api.LoaderJS
			switch {
			case strings.Contains(c.ContentType, "json"):
				loader = api.LoaderJSON
			case strings.Contains(c.ContentType, "css"):
				loader = api.LoaderEmpty
			}
			return api.OnLoadResult{Contents: &s, Loader: loader}, nil
		})
	}}
}

// serveVendor: /_esm/~vendor/<key>/<file>. Content-addressed by key, so cached forever.
func (e *ESM) serveVendor(w http.ResponseWriter, r *http.Request, rest string) {
	key, file, ok := strings.Cut(strings.TrimPrefix(rest, vendorPrefix), "/")
	if !ok || key == "" || file == "" || strings.ContainsAny(key+file, `/\`) || strings.HasPrefix(file, ".") {
		http.NotFound(w, r)
		return
	}
	b, err := os.ReadFile(filepath.Join(e.vendorDir(key), file))
	if err != nil || file == "manifest.json" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("content-type", "text/javascript; charset=utf-8")
	w.Header().Set("cache-control", "public, max-age=31536000, immutable")
	w.Write(b)
}

// pruneVendors keeps the newest few bundles: every package added or bumped leaves an old one behind. Not just the
// newest one, since a second UI (the Work project) or a page still on the previous build may be using another.
func (e *ESM) pruneVendors(keep string) {
	root := filepath.Join(e.dir, "~vendor")
	ents, err := os.ReadDir(root)
	if err != nil {
		return
	}
	type d struct {
		name string
		mod  time.Time
	}
	var ds []d
	for _, x := range ents {
		if info, err := x.Info(); err == nil && x.IsDir() && x.Name() != keep && !strings.HasSuffix(x.Name(), ".tmp") {
			ds = append(ds, d{x.Name(), info.ModTime()})
		}
	}
	sort.Slice(ds, func(i, j int) bool { return ds[i].mod.After(ds[j].mod) })
	for i, x := range ds {
		if i >= 4 {
			_ = os.RemoveAll(filepath.Join(root, x.name))
		}
	}
}
