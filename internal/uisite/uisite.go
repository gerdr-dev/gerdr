// Package uisite 渲染 gerdr 的界面：直接读界面目录（默认 ~/.gerdr/ui，GERDR_UI 可指到别处）里的源码，
// 没有打包环节。改完保存就生效 —— 界面本身就是 agent 能改的东西（「加一个每周额度的显示」「这个弹窗换个样子」
// 直接让 agent 改，不用另外开发、编译、发版）。
//
// 做法照 shuttle 的 internal/localsite：每个文件用 esbuild 单独编成 ESM（modules.go，按内容缓存），浏览器按 URL 加载；
// 文件一变只重编那一个，经 SSE 推给页面（events.go），页面里的运行时用 React Refresh 就地替换（runtime.go）。
// 页面由 react-router 按 src/pages/ 切换（routes.go）；第三方包按 importMap 从本机的 esm.sh 代理加载（importmap.go、esm.go）；
// 样式用 Tailwind browser 在浏览器里生成。
package uisite

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Renderer struct {
	dir     string
	base    string // URL prefix the whole site lives under ("" = the root, gerdr's own UI; "/work" = the Work project)
	log     *slog.Logger
	modules *moduleCache
	events  hub
	esm     *ESM // where third-party packages come from; nil = per-file import map only (vendor.go)

	mu        sync.Mutex
	cur, prev *build // prev 留着：还在用上一版的页面也拿得到它的模块
	waitingOn *vendorBuild

	errMu     sync.Mutex
	clientErr []ClientError   // 页面报上来的运行时错误（只留当前这一版构建的）
	logged    map[string]bool // build+source+message already written to the log, so a render loop doesn't flood it
}

// ClientError：页面里冒出来的错误（渲染出错、点按钮抛异常……），按构建号归档。
type ClientError struct {
	Build   string `json:"build"`
	Source  string `json:"source"`
	Message string `json:"message"`
	Detail  string `json:"detail,omitempty"`
	At      int64  `json:"at"`
}

// ReportClientErrors：页面经 POST /api/ui/errors 报上来的。只认当前这一版（旧版的错，改完已经不相干了），最多留 30 条。
// Every distinct error also goes to gerdr.log, whatever its build: the list here lives in memory and is gone after
// "ignore", a rollback or a restart, so without the log nobody can tell afterwards what the page showed before the
// user reached for the rescue panel.
func (r *Renderer) ReportClientErrors(build string, items []errorItem) {
	r.mu.Lock()
	cur := ""
	if r.cur != nil {
		cur = r.cur.id
	}
	r.mu.Unlock()
	r.errMu.Lock()
	defer r.errMu.Unlock()
	for _, it := range items {
		key := build + "\x00" + it.Source + "\x00" + it.Message
		if r.logged[key] { // once per build: a render loop reports the same error over and over
			continue
		}
		if r.logged == nil || len(r.logged) > 500 {
			r.logged = map[string]bool{}
		}
		r.logged[key] = true
		r.log.Warn("ui client error", "dir", r.dir, "build", build, "current", build == cur, "source", it.Source, "message", it.Message, "detail", firstLines(it.Detail, 8))
	}
	if build != cur {
		return
	}
	for _, it := range items {
		r.clientErr = append(r.clientErr, ClientError{Build: build, Source: it.Source, Message: it.Message, Detail: it.Detail, At: time.Now().UnixMilli()})
	}
	if n := len(r.clientErr); n > 30 {
		r.clientErr = r.clientErr[n-30:]
	}
}

// ClearClientErrors：人在急救面板里点了「忽略」（临时的错、已经知道了的错），清掉，免得一直挂着红框。
func (r *Renderer) ClearClientErrors() {
	r.errMu.Lock()
	r.clientErr = nil
	r.errMu.Unlock()
}

// ClientErrors：当前这一版构建下页面报过的错。
func (r *Renderer) ClientErrors() []ClientError {
	b := r.ensure()
	r.errMu.Lock()
	defer r.errMu.Unlock()
	var out []ClientError
	for _, e := range r.clientErr {
		if e.Build == b.id {
			out = append(out, e)
		}
	}
	return out
}

type build struct {
	id        string
	sig       string
	title     string
	favicon   string
	importMap map[string]string
	css       string
	routes    []route
	layout    string
	graph     *graph
	entry     string
	manifest  map[string]moduleInfo
	served    map[string]served
	inputs    map[string]bool
	fatal     string
	issues    []errorItem
	// The vendor bundle for this import map is still being built: the page is a "preparing" page that reloads
	// when it is done (html.go), instead of loading hundreds of files from esm.sh in the meantime.
	waiting bool
	// Everything the first render loads statically, for <link rel="modulepreload"> (html.go): the browser fetches it
	// all at once instead of discovering it one import level at a time.
	preload []string
}

type moduleInfo struct {
	URL  string `json:"url"`
	Lazy bool   `json:"lazy,omitempty"`
}

type served struct {
	ver, code, sourcemap string
}

type errorItem struct {
	Level   string `json:"level"`
	Source  string `json:"source"`
	Message string `json:"message"`
	Detail  string `json:"detail,omitempty"`
}

// New：dir 是界面目录，cacheDir 放编译缓存。
func New(dir, cacheDir string, log *slog.Logger) *Renderer {
	if log == nil {
		log = slog.Default()
	}
	return &Renderer{dir: dir, log: log, modules: &moduleCache{dir: filepath.Join(cacheDir, "modules")}}
}

func (r *Renderer) Dir() string { return r.dir }

// UseESM bundles the third-party packages through this proxy (vendor.go) instead of mapping each to esm.sh's files.
// Call before Watch.
func (r *Renderer) UseESM(e *ESM) { r.esm = e }

// NewAt is New for a site mounted under a URL prefix (e.g. "/work"): module, event and public URLs and
// the router's basename all carry it. Used for the Work view's project, a second, user-owned UI that
// gerdr shows in a frame next to the Work agent.
func NewAt(dir, cacheDir, base string, log *slog.Logger) *Renderer {
	r := New(dir, cacheDir, log)
	r.base = strings.TrimRight(base, "/")
	return r
}

func (r *Renderer) Base() string { return r.base }

func shortHash(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}

// ErrorItem 是构建时的问题（给 server 包解 POST /api/ui/errors 用）。
type ErrorItem = errorItem

// Status：现在的构建出没出错（给 agent 看：改完界面查一下有没有改坏）。
func (r *Renderer) Status() (fatal string, issues []errorItem) {
	b := r.ensure()
	return b.fatal, b.issues
}

// ensure 返回当前源码的构建结果，源码没变就用上一次的。建了新的就把变化推给打开着的页面。
func (r *Renderer) ensure() *build {
	r.mu.Lock()
	var inputs map[string]bool
	if r.cur != nil {
		inputs = r.cur.inputs
	}
	files, sig := r.scan(inputs)
	if r.cur != nil && r.cur.sig == sig {
		b := r.cur
		r.mu.Unlock()
		return b
	}
	start := time.Now()
	b := r.build(files, sig)
	if _, sig2 := r.scan(inputs); sig2 != sig {
		b.sig = ""
	}
	msg := diff(r.cur, b)
	r.prev, r.cur = r.cur, b
	r.mu.Unlock()
	if b.fatal != "" {
		r.log.Warn("ui build failed", "dir", r.dir, "err", firstLine(b.fatal))
	} else {
		r.log.Info("ui built", "modules", len(b.served), "took", time.Since(start).Round(time.Millisecond))
	}
	if msg != nil {
		r.events.broadcast(msg)
	}
	return b
}

type uiConfig struct {
	Title   string `json:"title"`
	Favicon string `json:"favicon"`
}

func (r *Renderer) build(files []string, sig string) *build {
	b := &build{sig: sig, served: map[string]served{}, manifest: map[string]moduleInfo{}, inputs: map[string]bool{}, title: "Gerdr"}
	var fatal []string
	var cfg uiConfig
	if raw, err := os.ReadFile(filepath.Join(r.dir, "ui.json")); err == nil {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			fatal = append(fatal, "ui.json 写错了："+err.Error())
		}
	}
	if cfg.Title != "" {
		b.title = cfg.Title
	}
	b.favicon = cfg.Favicon
	deps, err := readPackageJSON(r.dir)
	if err != nil {
		fatal = append(fatal, err.Error())
		deps = map[string]string{}
	}
	cssRaw, _ := os.ReadFile(filepath.Join(r.dir, "src", "index.css"))
	// Workspace packages bring their own CSS (src/style.css): the app's index.css can't @import it (Tailwind browser)
	cssRaw = append(cssRaw, packagesCSS(r.dir)...)
	css, dropped := browserCSS(string(cssRaw))
	b.css = css
	for _, d := range dropped {
		b.issues = append(b.issues, errorItem{Level: "warn", Source: "css", Message: fmt.Sprintf("src/index.css 里的 %s 用不了（Tailwind browser 不支持），已经跳过", d)})
	}

	b.routes = buildRoutes(files)
	if len(b.routes) == 0 {
		fatal = append(fatal, "src/pages/ 下面一个页面都没有（至少要有 src/pages/index.tsx）")
	}
	var roots []string
	for _, rt := range b.routes {
		roots = append(roots, rt.File)
	}
	for _, f := range files {
		if f == layoutFile {
			b.layout = f
			roots = append(roots, f)
		}
	}
	g := r.modules.buildGraph(r.dir, roots)
	g.base = r.base
	b.graph = g
	fatal = append(fatal, g.errs...)

	static := map[string]bool{}
	var walk func(string)
	walk = func(p string) {
		if static[p] || g.mods[p] == nil {
			return
		}
		static[p] = true
		for _, d := range g.mods[p].Deps {
			walk(d)
		}
	}
	for _, rt := range roots {
		walk(rt)
	}
	var bare, staticBare []string
	for p, m := range g.mods {
		b.inputs[p] = true
		if m.Error != "" {
			continue
		}
		bare = append(bare, m.Bare...)
		if static[p] {
			staticBare = append(staticBare, m.Bare...)
			b.preload = append(b.preload, moduleURL(r.base, p, g.version[p]))
		}
		b.served[p] = served{ver: g.version[p], code: g.link(p), sourcemap: m.Map}
		b.manifest[p] = moduleInfo{URL: moduleURL(r.base, p, g.version[p]), Lazy: !static[p]}
	}
	im, missing := buildImportMap(deps, uniq(bare))
	b.importMap = im
	if len(missing) > 0 {
		fatal = append(fatal, "代码里用到了这些包，但 package.json 的 dependencies 里没写版本："+strings.Join(missing, ", "))
	} else if r.esm != nil {
		r.useVendor(b, deps, append(staticBare, runtimeBare...))
	}
	sort.Strings(b.preload)
	entry := entrySource(b.routes, b.layout, r.base, func(f string) string { return moduleURL(r.base, f, g.version[f]) })
	ev := shortHash(entry)
	b.served["@entry.js"] = served{ver: ev, code: entry}
	b.entry = moduleURL(r.base, "@entry.js", ev)
	b.fatal = strings.Join(fatal, "\n\n")
	imj, _ := json.Marshal(b.importMap)
	b.id = shortHash(ev, b.css, string(imj), b.fatal, b.title, b.favicon, fmt.Sprint(b.waiting))
	return b
}

// useVendor points the import map at the vendor bundle, or marks the build as waiting for it. Prefix entries
// ("react/") stay on esm.sh: they are only a fallback for subpaths nobody imports directly. Called with r.mu held.
func (r *Renderer) useVendor(b *build, deps map[string]string, staticBare []string) {
	entries := map[string]string{}
	for spec, u := range b.importMap {
		if !strings.HasSuffix(spec, "/") {
			entries[spec] = u
		}
	}
	vb := r.esm.vendor(entries, deps)
	select {
	case <-vb.done:
		if vb.err != nil {
			b.issues = append(b.issues, errorItem{Level: "warn", Source: "vendor", Message: "第三方包没能打成 vendor 包，退回逐个从 esm.sh 加载（首次打开会慢）：" + firstLine(vb.err.Error()), Detail: vb.err.Error()})
			return
		}
		for spec, u := range vb.imports {
			b.importMap[spec] = u
		}
		seen := map[string]bool{}
		for _, spec := range staticBare {
			u, ok := vb.imports[spec]
			if !ok {
				continue
			}
			for _, x := range append([]string{u}, vb.closure[u]...) {
				if !seen[x] {
					seen[x] = true
					b.preload = append(b.preload, x)
				}
			}
		}
	default:
		b.waiting = true
		if r.waitingOn == vb {
			return
		}
		r.waitingOn = vb
		go func() {
			<-vb.done
			// Force a rebuild (the sources didn't change, so ensure would keep the waiting build): its import map
			// now differs, so diff sends open pages a reload.
			r.mu.Lock()
			if r.waitingOn == vb {
				r.waitingOn = nil
			}
			if r.cur != nil {
				r.cur.sig = ""
			}
			r.mu.Unlock()
			r.ensure()
		}()
	}
}

// update 是推给页面的一次变化（runtime.go 的 apply）
type update struct {
	Type       string                `json:"type"` // update / reload / error
	Build      string                `json:"build"`
	Error      string                `json:"error,omitempty"`
	Entry      string                `json:"entry,omitempty"`
	Modules    map[string]moduleInfo `json:"modules,omitempty"`
	Boundaries []string              `json:"boundaries"` // 要热替换的模块；null：换不了，重新渲染入口
	CSS        *string               `json:"css,omitempty"`
}

// diff 比较两次构建，决定页面怎么更新（照 shuttle localsite.diff）：
//   - 构建出错 → error；importMap / 标题变了 → reload（importMap 在页面里改不了）；
//   - 路由变了 → 重新渲染入口；
//   - 否则从改了的文件沿「谁 import 了它」往上找最近的 React Refresh 边界，一路都找得到就热替换这些边界，
//     有一条路走到了入口就重新渲染入口；
//   - index.css 变了 → 带上新的 css，只换样式。
func diff(prev, next *build) *update {
	if prev == nil || prev.id == next.id {
		return nil
	}
	if next.fatal != "" {
		return &update{Type: "error", Build: next.id, Error: next.fatal}
	}
	same := func(a, b any) bool { x, _ := json.Marshal(a); y, _ := json.Marshal(b); return string(x) == string(y) }
	if !same(prev.importMap, next.importMap) || prev.title != next.title || prev.favicon != next.favicon {
		return &update{Type: "reload", Build: next.id}
	}
	u := &update{Type: "update", Build: next.id, Entry: next.entry, Modules: next.manifest}
	if next.css != prev.css {
		u.CSS = &next.css
	}
	if prev.fatal != "" || prev.graph == nil || !same(prev.routes, next.routes) || prev.layout != next.layout {
		return u
	}
	isRoot := map[string]bool{}
	for _, rt := range next.routes {
		isRoot[rt.File] = true
	}
	if next.layout != "" {
		isRoot[next.layout] = true
	}
	var changed []string
	for p, m := range next.graph.mods {
		if old := prev.graph.mods[p]; old == nil || old.Code != m.Code {
			changed = append(changed, p)
		}
	}
	sort.Strings(changed)
	u.Boundaries = []string{}
	seen := map[string]bool{}
	queue := changed
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		if seen[p] {
			continue
		}
		seen[p] = true
		m := next.graph.mods[p]
		if m != nil && m.boundary(p) {
			u.Boundaries = append(u.Boundaries, p)
			continue
		}
		importers := next.graph.importers[p]
		if isRoot[p] || len(importers) == 0 {
			u.Boundaries = nil
			return u
		}
		queue = append(queue, importers...)
	}
	sort.Strings(u.Boundaries)
	return u
}

// Watch 盯着界面目录：源码一变就重新构建（ensure 里把变化推给页面）。
func (r *Renderer) Watch(stop <-chan struct{}) {
	t := time.NewTicker(250 * time.Millisecond)
	defer t.Stop()
	r.ensure()
	pending := ""
	for {
		select {
		case <-stop:
			return
		case <-t.C:
		}
		r.mu.Lock()
		var inputs map[string]bool
		cur := ""
		if r.cur != nil {
			inputs, cur = r.cur.inputs, r.cur.sig
		}
		_, sig := r.scan(inputs)
		r.mu.Unlock()
		if sig == cur {
			pending = ""
			continue
		}
		// 等一轮没有新改动再建：agent 一次改好几个文件时别每个文件推一次
		if sig != pending {
			pending = sig
			continue
		}
		pending = ""
		r.ensure()
	}
}

// ServeModule 处理 /_ui/m/<路径>?v=<版本>（和 .map）。版本对不上当前和上一次构建的就 404：页面会整页刷新。
func (r *Renderer) ServeModule(w http.ResponseWriter, req *http.Request) {
	rel := strings.TrimPrefix(req.URL.Path, r.base+"/_ui/m/")
	ver := req.URL.Query().Get("v")
	if rel == "@runtime.js" {
		w.Header().Set("content-type", "text/javascript; charset=utf-8")
		w.Header().Set("cache-control", "public, max-age=31536000, immutable")
		w.Write([]byte(runtimeJS))
		return
	}
	isMap := strings.HasSuffix(rel, ".map")
	rel = strings.TrimSuffix(rel, ".map")
	s, ok := r.lookup(rel, ver)
	if !ok {
		http.NotFound(w, req)
		return
	}
	w.Header().Set("cache-control", "public, max-age=31536000, immutable")
	if isMap {
		w.Header().Set("content-type", "application/json")
		w.Write([]byte(s.sourcemap))
		return
	}
	w.Header().Set("content-type", "text/javascript; charset=utf-8")
	w.Write([]byte(s.code))
}

func (r *Renderer) lookup(rel, ver string) (served, bool) {
	r.ensure() // 页面加载早于 Watch 的第一拍时也拿得到
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, b := range []*build{r.cur, r.prev} {
		if b == nil {
			continue
		}
		if s, ok := b.served[rel]; ok && s.ver == ver {
			return s, true
		}
	}
	return served{}, false
}

// ServePublic：界面目录 public/ 下的文件挂在根路径（图标、图片）。
func (r *Renderer) ServePublic(w http.ResponseWriter, req *http.Request) bool {
	clean := path.Clean("/" + strings.TrimPrefix(req.URL.Path, r.base))
	if clean == "/" {
		return false
	}
	p := filepath.Join(r.dir, "public", filepath.FromSlash(clean))
	if fi, err := os.Stat(p); err != nil || fi.IsDir() {
		return false
	}
	http.ServeFile(w, req, p)
	return true
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	return s
}

// firstLines keeps the top of a stack trace: enough to find the file, short enough for one log line.
func firstLines(s string, n int) string {
	lines := strings.SplitN(s, "\n", n+1)
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}

var sourceExts = map[string]bool{".ts": true, ".tsx": true, ".js": true, ".jsx": true, ".mjs": true, ".css": true, ".json": true}

// scan 列出界面目录里的源码（src/ 下 + package.json、ui.json，加上 workspace 包 packages/*/ 的 src/ 和 package.json），
// 按路径、大小、修改时间算签名：签名变了就重建。
func (r *Renderer) scan(_ map[string]bool) (files []string, sig string) {
	h := sha256.New()
	tops := []string{"package.json", "ui.json"}
	roots := []string{"src"}
	if ents, err := os.ReadDir(filepath.Join(r.dir, "packages")); err == nil {
		for _, e := range ents {
			if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
				tops = append(tops, "packages/"+e.Name()+"/package.json")
				roots = append(roots, "packages/"+e.Name()+"/src")
			}
		}
	}
	for _, top := range tops {
		if info, err := os.Stat(filepath.Join(r.dir, filepath.FromSlash(top))); err == nil {
			h.Write([]byte(top + "\x00" + strconv.FormatInt(info.Size(), 10) + "\x00" + strconv.FormatInt(info.ModTime().UnixNano(), 10) + "\n"))
		}
	}
	for _, root := range roots {
		_ = filepath.WalkDir(filepath.Join(r.dir, filepath.FromSlash(root)), func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			rel, _ := filepath.Rel(r.dir, p)
			rel = filepath.ToSlash(rel)
			if d.IsDir() {
				if strings.HasPrefix(d.Name(), ".") || d.Name() == "node_modules" {
					return filepath.SkipDir
				}
				return nil
			}
			if !sourceExts[path.Ext(rel)] {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			files = append(files, rel)
			h.Write([]byte(rel + "\x00" + strconv.FormatInt(info.Size(), 10) + "\x00" + strconv.FormatInt(info.ModTime().UnixNano(), 10) + "\n"))
			return nil
		})
	}
	sort.Strings(files)
	return files, hex.EncodeToString(h.Sum(nil))
}
