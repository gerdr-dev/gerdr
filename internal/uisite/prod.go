package uisite

import (
	"net/http"
	"time"
)

// Production React, the default; development React per device (Settings → Labs, a cookie). The development build of
// React does a lot of checking on every render: on a phone's main thread a session summary pushed while a turn streams cost
// tens of milliseconds, a good part of it React's dev-only work (jsxDEV, prop validation, console.createTask for
// owner stacks), and swiping / scrolling the overview stuttered.
//
// Only the page's import map differs. The UI's own modules are compiled once, as always (JSXDev, so component stacks
// in development name the file): their jsxDEV calls go to a small shim over the production jsx runtime. Every
// third-party package is switched to esm.sh's production build too, bundled as a vendor bundle of its own (vendor.go;
// the entries' URLs differ, so its key does). React Refresh has no production build, so it is left out and the
// runtime (runtime.go) takes its no-Refresh path: an edit to the UI re-renders the whole page from its entry
// (component state such as scroll positions and open menus is lost; the store keeps sessions, drafts, the layout).
// Build ids are the development build's: errors these pages report, hot updates and reloads all work unchanged.

// Which React a device asked for: "development" (Settings → Labs, for debugging the UI: component stacks name files,
// React Refresh keeps state over an edit), "production" (asked for explicitly: waits for its packages), or no cookie:
// production when its packages are ready, without waiting. Production became the default after a tab switch measured
// 4-5x slower on development React (73-82ms vs 37-52ms with the CPU 4x slowed, after the re-render fixes;
// tools/scrolltest/tabswitch.mjs), and every other render pays the same.
const prodCookie = "gerdr_react"

// jsxShim: what "react/jsx-dev-runtime" points at on a production page.
const jsxShim = "@jsx-dev-prod.js"

const jsxShimJS = `import { jsx, jsxs, Fragment } from "react/jsx-runtime";
export { Fragment };
export function jsxDEV(type, props, key, isStatic) {
  return isStatic ? jsxs(type, props, key) : jsx(type, props, key);
}
`

type prodPage struct {
	importMap map[string]string
	preload   []string
}

// reactChoice: "development", "production", or "" (nothing asked: production if ready).
func reactChoice(req *http.Request) string {
	c, err := req.Cookie(prodCookie)
	if err != nil || (c.Value != "development" && c.Value != "production") {
		return ""
	}
	return c.Value
}

// prodFor: b's import map and preload list for production React, built once per build. While its vendor bundle is
// still building it waits up to `wait` (the first time takes seconds to tens of seconds: every package is
// downloaded again in its production build), then gives up with "preparing": that page loads the development build.
// "unavailable": no vendor bundles here (per-file esm.sh only) or the build is broken; also the development one.
func (r *Renderer) prodFor(b *build, wait time.Duration) (*prodPage, string) {
	if r.esm == nil || b.fatal != "" || b.deps == nil {
		return nil, "unavailable"
	}
	r.mu.Lock()
	p := b.prod
	r.mu.Unlock()
	if p != nil {
		return p, ""
	}
	im, missing := buildImportMap(b.deps, b.bare, true)
	if len(missing) > 0 {
		return nil, "unavailable"
	}
	entries := map[string]string{}
	for spec, u := range im {
		if spec[len(spec)-1] != '/' {
			entries[spec] = u
		}
	}
	vb := r.esm.vendor(entries, b.deps, true)
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
	im["react/jsx-dev-runtime"] = moduleURL(r.base, jsxShim, shortHash(jsxShimJS))
	p = &prodPage{importMap: im, preload: append([]string(nil), b.ownPreload...)}
	seen := map[string]bool{}
	for _, spec := range b.staticBare {
		u, ok := vb.imports[spec]
		if !ok {
			continue
		}
		for _, x := range append([]string{u}, vb.closure[u]...) {
			if !seen[x] {
				seen[x] = true
				p.preload = append(p.preload, x)
			}
		}
	}
	r.mu.Lock()
	b.prod = p
	r.mu.Unlock()
	return p, ""
}

// PrepareProd builds the production packages ahead of the switch (Settings → Labs waits on this with its own
// "switching" state, then reloads): the reload itself then has nothing left to wait for. "" = ready.
func (r *Renderer) PrepareProd(wait time.Duration) string {
	_, st := r.prodFor(r.ensure(), wait)
	return st
}
