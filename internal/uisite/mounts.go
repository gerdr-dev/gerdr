package uisite

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gerdr-dev/gerdr/internal/i18n"
)

// Mounts: other UI directories compiled into this one's page, each drawn as a second React root in the same
// document (a project's Work UI next to Code). Not a frame:
//
//   - one module graph: a mounted file is the graph path "@m/<name>/<path>", served by this Renderer like the host's
//     own; its bare imports go through the page's one import map (the host's package.json wins on a version
//     conflict, the mount's own fills in what the host doesn't have), so there is one React and a mount can use the
//     host's components and state directly: `import { SessionChat } from "@gerdr/core"` is the host's src/core, the
//     same module instances the host runs (modules.go coreSpec), and the host's workspace packages (@gerdr/chat)
//     resolve too;
//   - one hot reload stream: an edit in a mount hot-replaces its components through the same React Refresh, and when
//     it can't, only that mount's root is rendered again (update.Remount), not the host;
//   - its own failure: a mount's compile errors, missing packages and render errors stay in its own area (mountInfo
//     .Error, its own route error counter); the host page never shows the full-page overlay for them;
//   - its own styles: its src/index.css goes into the page's Tailwind stylesheet with every plain rule inside
//     @scope ([data-gerdr-mount="<name>"]) (scopeCSS): its tokens and base styles reach its own area only.
//     Tailwind's own at-rules (@theme, @custom-variant, @utility…) stay global: there is one Tailwind per page.
//
// A mount's router is a memory router (the address bar is the host's); the page that mounts it keeps the path.

const mountPrefix = "@m/"

// MountAttr is the attribute on the element a mount renders into; its scoped CSS matches it.
const MountAttr = "data-gerdr-mount"

type mountInfo struct {
	// Entry: the mount's entry module (createApp({ path, onNavigate })), "" while it has an error.
	Entry string `json:"entry,omitempty"`
	// Cards: its src/cards.tsx when it has one, loaded by the host at start (cards drawn in any conversation).
	Cards  string         `json:"cards,omitempty"`
	Error  string         `json:"error,omitempty"`
	Config map[string]any `json:"config,omitempty"` // its ui.json
}

// Mount compiles dir's UI into this page as name (a project id); dir "" takes it out. The page gets the change as a
// hot update (a new mount is a lazily loaded module set: nothing reloads).
func (r *Renderer) Mount(name, dir string) {
	r.mu.Lock()
	if dir == "" {
		if _, ok := r.mounts[name]; !ok {
			r.mu.Unlock()
			return
		}
		delete(r.mounts, name)
	} else {
		if r.mounts[name] == dir {
			r.mu.Unlock()
			return
		}
		if r.mounts == nil {
			r.mounts = map[string]string{}
		}
		r.mounts[name] = dir
	}
	if r.cur != nil {
		r.cur.sig = ""
	}
	r.mu.Unlock()
	r.ensure()
}

// MountStatus is a mount's build error and the runtime errors its area reported (`gerdr panel status`).
func (r *Renderer) MountStatus(name string) (fatal string, runtime []ClientError, ok bool) {
	b := r.ensure()
	mi, ok := b.mounts[name]
	if !ok {
		return "", nil, false
	}
	for _, e := range r.ClientErrors() {
		if strings.HasPrefix(e.Source, mountPrefix+name+"/") || strings.HasPrefix(e.Detail, mountPrefix+name+"/") || strings.Contains(e.Source, "@"+name) {
			runtime = append(runtime, e)
		}
	}
	return mi.Error, runtime, true
}

// mountNames: sorted. Called with r.mu held.
func (r *Renderer) mountNames() []string {
	names := make([]string, 0, len(r.mounts))
	for n := range r.mounts {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// siteOf: which site a graph path belongs to. Called with r.mu held (reads r.mounts).
func (r *Renderer) siteOf(local map[string]string) func(rel string) site {
	mounts := map[string]string{}
	for k, v := range r.mounts {
		mounts[k] = v
	}
	return func(rel string) site {
		if name, ok := mountOf(rel); ok {
			return site{dir: mounts[name], prefix: mountPrefix + name + "/", local: local, host: r.dir}
		}
		return site{dir: r.dir, local: local, host: r.dir}
	}
}

// mountOf: the mount a graph path is in.
func mountOf(rel string) (string, bool) {
	if !strings.HasPrefix(rel, mountPrefix) {
		return "", false
	}
	name, _, ok := strings.Cut(strings.TrimPrefix(rel, mountPrefix), "/")
	return name, ok
}

// mountSpec is what one mount contributes to a build, before the graph is compiled.
type mountSpec struct {
	name, dir string
	prefix    string
	routes    []route
	layout    string
	cards     string
	roots     []string
	deps      map[string]string
	config    map[string]any
	errs      []string
	css       string
}

// prepareMount reads a mount's ui.json, package.json, routes and stylesheet. files: the graph paths scan found.
func (r *Renderer) prepareMount(name, dir string, files []string) *mountSpec {
	m := &mountSpec{name: name, dir: dir, prefix: mountPrefix + name + "/", deps: map[string]string{}}
	if raw, err := os.ReadFile(filepath.Join(dir, "ui.json")); err == nil {
		if err := json.Unmarshal(raw, &m.config); err != nil {
			m.errs = append(m.errs, i18n.S("ui.json 写错了：", "ui.json is invalid: ")+err.Error())
		}
	}
	if deps, err := readPackageJSON(dir); err == nil {
		m.deps = deps
	} else {
		m.errs = append(m.errs, err.Error())
	}
	var own []string
	for _, f := range files {
		if strings.HasPrefix(f, m.prefix) {
			own = append(own, strings.TrimPrefix(f, m.prefix))
		}
	}
	for _, rt := range buildRoutes(own) {
		m.routes = append(m.routes, route{File: m.prefix + rt.File, Path: rt.Path})
		m.roots = append(m.roots, m.prefix+rt.File)
	}
	if len(m.routes) == 0 {
		m.errs = append(m.errs, i18n.S("src/pages/ 下面一个页面都没有（至少要有 src/pages/index.tsx）", "No pages under src/pages/ (at least src/pages/index.tsx is needed)"))
	}
	for _, f := range own {
		switch f {
		case layoutFile:
			m.layout = m.prefix + f
			m.roots = append(m.roots, m.layout)
		case "src/cards.tsx", "src/cards.ts":
			m.cards = m.prefix + f
			m.roots = append(m.roots, m.cards)
		}
	}
	indexCSS := filepath.Join(dir, "src", "index.css")
	raw, _ := os.ReadFile(indexCSS)
	text, _ := inlineCSSImports(filepath.Join(dir, "src"), indexCSS, string(raw))
	css, _ := browserCSS(text)
	m.css = "\n/* mount " + name + " */\n" + scopeCSS(css, "["+MountAttr+"=\""+name+"\"]")
	return m
}

// finishMount: the mount's errors from the compiled graph and missing package versions, and its entry module.
func (r *Renderer) finishMount(b *build, m *mountSpec, g *graph, missing []string) {
	errs := append([]string{}, m.errs...)
	var paths []string
	for p := range g.mods {
		if strings.HasPrefix(p, m.prefix) {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	for _, p := range paths {
		if mod := g.mods[p]; mod != nil && mod.Error != "" {
			errs = append(errs, mod.Error)
		}
	}
	if len(missing) > 0 {
		errs = append(errs, i18n.S("代码里用到了这些包，但 package.json 的 dependencies 里没写版本：", "These packages are used in code but have no version in package.json dependencies: ")+strings.Join(missing, ", "))
	}
	mi := mountInfo{Config: m.config}
	if len(errs) > 0 {
		mi.Error = strings.Join(errs, "\n\n")
	} else {
		url := func(f string) string { return moduleURL(r.base, f, g.version[f]) }
		src := entrySource(m.routes, m.layout, "", url, true)
		ev := shortHash(src)
		key := m.prefix + "@entry.js"
		b.served[key] = served{ver: ev, code: src}
		mi.Entry = moduleURL(r.base, key, ev)
		if m.cards != "" {
			mi.Cards = url(m.cards)
		}
	}
	b.mounts[m.name] = mi
}

// scopeCSS puts every plain rule of css inside @scope (sel), inside the @media / @supports / @layer blocks it's
// in; Tailwind's at-rules and other global ones (@theme, @keyframes, @font-face…) stay as they are. A selector that
// starts at the document (:root, html, body) starts at the scope root instead, and .dark becomes "the scope root in
// dark mode", so a stylesheet written for a whole page works unchanged for one area of it.
func scopeCSS(css, sel string) string {
	var out strings.Builder
	var plain strings.Builder
	flush := func() {
		if strings.TrimSpace(plain.String()) != "" {
			out.WriteString("@scope (" + sel + ") {\n" + plain.String() + "}\n")
		}
		plain.Reset()
	}
	for _, st := range cssStatements(css) {
		if st.block == nil {
			if strings.HasPrefix(st.prelude, "@") {
				flush()
				out.WriteString(st.prelude + ";\n")
			}
			continue
		}
		if strings.HasPrefix(st.prelude, "@") {
			name := strings.ToLower(strings.Fields(st.prelude + " ")[0])
			switch name {
			case "@media", "@supports", "@layer", "@container":
				flush()
				out.WriteString(st.prelude + " {\n" + scopeCSS(*st.block, sel) + "}\n")
			default: // @theme, @custom-variant, @utility, @variant, @keyframes, @font-face, @property…: global
				flush()
				out.WriteString(st.prelude + " {" + *st.block + "}\n")
			}
			continue
		}
		plain.WriteString(scopeSelectors(st.prelude) + " {" + *st.block + "}\n")
	}
	flush()
	return out.String()
}

// scopeSelectors rewrites a selector list's document-level starts (see scopeCSS).
func scopeSelectors(list string) string {
	parts := splitTop(list, ',')
	for i, p := range parts {
		p = strings.TrimSpace(p)
		head, rest := p, ""
		if j := strings.IndexAny(p, " >+~\n\t"); j >= 0 {
			head, rest = p[:j], p[j:]
		}
		switch head {
		case ":root", "html", "body":
			p = ":scope" + rest
		case ".dark", "html.dark", ":root.dark":
			p = ":where(.dark) :scope" + rest
		}
		parts[i] = p
	}
	return strings.Join(parts, ", ")
}

// splitTop splits s at sep where it isn't inside (), [] or quotes.
func splitTop(s string, sep byte) []string {
	var out []string
	depth, start := 0, 0
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '(' || c == '[':
			depth++
		case c == ')' || c == ']':
			depth--
		case c == sep && depth == 0:
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

type cssStatement struct {
	prelude string
	block   *string // nil: a statement ending in ";"
}

// cssStatements splits a stylesheet into its top-level statements (comments dropped).
func cssStatements(css string) []cssStatement {
	var out []cssStatement
	var buf strings.Builder
	i := 0
	for i < len(css) {
		c := css[i]
		switch {
		case c == '/' && i+1 < len(css) && css[i+1] == '*':
			end := strings.Index(css[i+2:], "*/")
			if end < 0 {
				i = len(css)
				continue
			}
			i += end + 4
			continue
		case c == '"' || c == '\'':
			j := i + 1
			for j < len(css) && css[j] != c {
				if css[j] == '\\' {
					j++
				}
				j++
			}
			buf.WriteString(css[i:min(j+1, len(css))])
			i = j + 1
			continue
		case c == ';':
			if p := strings.TrimSpace(buf.String()); p != "" {
				out = append(out, cssStatement{prelude: p})
			}
			buf.Reset()
		case c == '{':
			depth, j := 1, i+1
			var quote byte
			for j < len(css) && depth > 0 {
				d := css[j]
				switch {
				case quote != 0:
					if d == '\\' {
						j++
					} else if d == quote {
						quote = 0
					}
				case d == '"' || d == '\'':
					quote = d
				case d == '/' && j+1 < len(css) && css[j+1] == '*':
					if end := strings.Index(css[j+2:], "*/"); end >= 0 {
						j += end + 3
					}
				case d == '{':
					depth++
				case d == '}':
					depth--
				}
				j++
			}
			body := css[i+1 : max(i+1, j-1)]
			out = append(out, cssStatement{prelude: strings.TrimSpace(buf.String()), block: &body})
			buf.Reset()
			i = j
			continue
		default:
			buf.WriteByte(c)
		}
		i++
	}
	if p := strings.TrimSpace(buf.String()); p != "" && strings.HasPrefix(p, "@") {
		out = append(out, cssStatement{prelude: p})
	}
	return out
}
