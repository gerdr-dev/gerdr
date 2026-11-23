package uisite

import (
	"net/http"
	"sort"
	"strings"
	"time"
)

// Production React is the default and primary build (uisite.go: buildImportMap(..., true) and useVendor(..., true));
// development React is opt-in per device (Settings → Labs, cookie gerdr_react=development). The development build of
// React does a lot of checking on every render: on a phone's main thread a session summary pushed while a turn streams
// cost tens of milliseconds, a good part of it React's dev-only work (jsxDEV, prop validation, console.createTask for
// owner stacks), and swiping / scrolling the overview stuttered.
//
// Only the page's import map differs. The UI's own modules are compiled once, as always (JSXDev, so component stacks
// in development name the file): on the default production pages their jsxDEV calls go to a small shim over the
// production jsx runtime (@jsx-dev-prod.js), and React Refresh is left out (it has no production build, so runtime.go
// re-renders the whole page from its entry on an edit; component state such as scroll positions and open menus is lost,
// while the store keeps sessions, drafts and the layout). When a device opts into development React, devFor switches
// its page to esm.sh's ?dev builds (with React Refresh and real jsx-dev-runtime) in a separate vendor bundle built on
// demand. Build ids are the primary build's: errors these pages report, hot updates and reloads all work unchanged.

// Which React a device asked for: "development" (Settings → Labs off, for debugging the UI: component stacks name
// files, React Refresh keeps state over an edit), "production" (legacy explicit opt-in), or no cookie: both default
// to the primary production build. Production became the default after a tab switch measured 4-5x slower on development
// React (73-82ms vs 37-52ms with the CPU 4x slowed, after the re-render fixes; tools/scrolltest/tabswitch.mjs), and
// every other render pays the same.
const prodCookie = "gerdr_react"

// jsxShim: what "react/jsx-dev-runtime" points at on a production page.
const jsxShim = "@jsx-dev-prod.js"

const jsxShimJS = `import { jsx, jsxs, Fragment } from "react/jsx-runtime";
export { Fragment };
export function jsxDEV(type, props, key, isStatic) {
  return isStatic ? jsxs(type, props, key) : jsx(type, props, key);
}
`

type devPage struct {
	importMap map[string]string
	preload   []string
}

// reactChoice: "development", "production", or "" (default: production).
func reactChoice(req *http.Request) string {
	c, err := req.Cookie(prodCookie)
	if err != nil || (c.Value != "development" && c.Value != "production") {
		return ""
	}
	return c.Value
}

// devFor: b's import map and preload list for development React, built once per build on demand. While its vendor
// bundle is still building it waits up to `wait` (the first time downloads every package's development build), then
// gives up with "preparing": that page stays on the primary production build. "unavailable": the build is broken or
// the development vendor bundle failed; also stays on production.
func (r *Renderer) devFor(b *build, wait time.Duration) (*devPage, string) {
	if b.fatal != "" || b.deps == nil {
		return nil, "unavailable"
	}
	r.mu.Lock()
	p := b.dev
	r.mu.Unlock()
	if p != nil {
		return p, ""
	}
	im, missing := buildImportMap(b.deps, b.bare, false)
	if len(missing) > 0 {
		return nil, "unavailable"
	}
	preload := append([]string(nil), b.ownPreload...)
	if r.esm != nil {
		entries := map[string]string{}
		for spec, u := range im {
			if !strings.HasSuffix(spec, "/") && strings.HasPrefix(u, "/_esm/") {
				entries[spec] = u
			}
		}
		vb := r.esm.vendor(entries, b.deps, false)
		if wait <= 0 {
			// Not waiting: look only (time.After(0) would race a closed done and lose half the time)
			select {
			case <-vb.done:
			default:
				return nil, "preparing"
			}
		} else {
			select {
			case <-vb.done:
			case <-time.After(wait):
				return nil, "preparing"
			}
		}
		if vb.err != nil {
			return nil, "unavailable"
		}
		for spec, u := range vb.imports {
			im[spec] = u
		}
		seen := map[string]bool{}
		for _, spec := range b.staticBare {
			u, ok := vb.imports[spec]
			if !ok {
				continue
			}
			for _, x := range append([]string{u}, vb.closure[u]...) {
				if !seen[x] {
					seen[x] = true
					preload = append(preload, x)
				}
			}
		}
	}
	sort.Strings(preload)
	p = &devPage{importMap: im, preload: preload}
	r.mu.Lock()
	b.dev = p
	r.mu.Unlock()
	return p, ""
}

// PrepareDev builds the development packages ahead of switching off Production React in Settings → Labs (which shows
// its own "switching" state before setting the cookie and reloading). "" = ready.
func (r *Renderer) PrepareDev(wait time.Duration) string {
	_, st := r.devFor(r.ensure(), wait)
	return st
}

// PrepareProd is kept for older UI tabs calling POST /api/ui/prod-react; production is already the primary build.
func (r *Renderer) PrepareProd(_ time.Duration) string {
	b := r.ensure()
	if b.fatal != "" {
		return "unavailable"
	}
	if b.waiting {
		return "preparing"
	}
	return ""
}
