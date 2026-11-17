package uisite

import (
	"encoding/json"
	"html"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/gerdr-dev/gerdr/internal/i18n"
)

// 页面一开始就接管报错，记进 __GERDR_UI_ERRORS__（agent 改界面改坏了，GET /api/ui/errors 能读到）。
const errorsScript = `(function(){
  var list = window.__GERDR_UI_ERRORS__ = %s;
  // 页面里的报错也报给服务端：agent 改了界面之后用 gerdr ui status 能看到「改坏了没有」。
  var queue = [], timer = 0;
  function send(){ timer = 0; var items = queue; queue = [];
    try { fetch(((window.__GERDR_UI__ || {}).base || "") + "/api/ui/errors", { method: "POST", headers: { "Content-Type": "application/json", "X-Gerdr": "1" }, body: JSON.stringify({ build: (window.__GERDR_UI__ || {}).build, items: items }) }).catch(function(){}) } catch (_) {} }
  function push(source, message, detail){
    var item = { level: "error", source: source, build: (window.__GERDR_UI__ || {}).build, message: String(message), detail: String(detail || "").slice(0, 4000) };
    var hold = window.__GERDR_HOLD__;
    if (hold && /hook|Should have a queue/i.test(item.message + " " + item.detail)) return hold.push(item);
    list.push(item);
    queue.push(item);
    if (!timer) timer = setTimeout(send, 500);
  }
  window.__GERDR_REPORT__ = push;
  addEventListener("error", function(e){ if (/^ResizeObserver loop /.test(e.message || "")) return; if (e.error || e.message) push("window.error", e.message || e.error, e.error && e.error.stack) });
  addEventListener("unhandledrejection", function(e){ var r = e.reason; push("unhandledrejection", r && r.message ? (r.name || "Error") + ": " + r.message : r, r && r.stack) });
})()`

// sameStyleScript makes writing a <style>'s current text back into it a no-op. Tailwind's browser build rewrites its
// whole generated sheet (~250KB on gerdr's UI) whenever a class name shows up in the DOM for the first time, even when
// that class was already compiled up front (classes.go), so the text comes out identical; the browser still re-parses
// it and recalculates every element's style. Opening Settings did it twice inside the dialog's fade, dropping 2–5
// frames at 4x CPU throttling (docs/dev/TAILWIND.md). Comparing is cheap next to that: equal lengths are rare unless
// the text is the same. Runs before Tailwind loads; patching its minified source instead would break on upgrades.
// The one behaviour it changes: an identical write no longer resets rules added with insertRule, which nothing here does.
const sameStyleScript = `(function(){
  var d = Object.getOwnPropertyDescriptor(Node.prototype, "textContent");
  if (!d || !d.set) return;
  Object.defineProperty(HTMLStyleElement.prototype, "textContent", { configurable: true, enumerable: d.enumerable,
    get: function(){ return d.get.call(this) },
    set: function(v){ if (typeof v === "string" && v === d.get.call(this)) return; d.set.call(this, v) } });
})()`

// loadingHTML: the loading screen, with text in place of the line under the spinner.
// The UI's own "connecting" screen (LoadingScreen in ui/src/App.tsx) replaces it with the same spinner and line at
// the same spot, so keep the two in step or the spinner jumps when React mounts. The slow-start hint is positioned
// out of the flow for that reason: in the flow it pushed the spinner up, and the UI's screen has no such line.
// The spinner's phase follows the page clock (animation-delay set by the script after it), not its own start, so the
// UI's spinner, set the same way, picks up at the same angle instead of restarting.
func loadingHTML(text string) string {
	return `<div id="__gerdr_loading" style="position:fixed;inset:0;display:flex;flex-direction:column;align-items:center;justify-content:center;gap:12px;font:13px/1.5 system-ui,-apple-system,sans-serif;color:CanvasText;background:Canvas">` +
		`<style>@keyframes __gerdr_spin{to{transform:rotate(360deg)}}</style>` +
		`<div id="__gerdr_spinner" style="width:22px;height:22px;box-sizing:border-box;border-radius:50%;border:2px solid color-mix(in srgb,CanvasText 15%,transparent);border-top-color:color-mix(in srgb,CanvasText 60%,transparent);animation:__gerdr_spin .8s linear infinite"></div>` +
		`<div style="position:relative;opacity:.6">` + html.EscapeString(text) +
		`<div id="__gerdr_loading_slow" style="position:absolute;top:100%;left:50%;transform:translateX(-50%);margin-top:12px;width:max-content;max-width:320px;opacity:.75;text-align:center;visibility:hidden">` +
		i18n.S("第一次打开要下载界面用到的包，可能要一会儿。一直打不开的话：", "The first launch downloads the packages the UI uses, which may take a while. If it never loads: ") +
		`<a href="/rescue" onclick="if(window.__gerdrRescue){__gerdrRescue.open();return false}" style="color:inherit">` + i18n.S("急救面板", "Rescue panel") + `</a></div></div>` +
		`</div>`
}

// loadingScript runs right after loadingHTML: aligns the spinner's phase to the page clock (see loadingHTML).
const loadingScript = "var __s=document.getElementById('__gerdr_spinner');if(__s)__s.style.animationDelay=-(performance.now()%800)+'ms';\n"

// ServePage 出页面：importMap + 模块清单（window.__GERDR_UI__）+ 运行时，运行时 import 入口再渲染。
// nonce：内联脚本靠它过 CSP（importMap 只能内联）。
func (r *Renderer) ServePage(w http.ResponseWriter, req *http.Request, nonce string) {
	b := r.ensure()
	n := ` nonce="` + nonce + `"`
	var s strings.Builder
	s.WriteString("<!DOCTYPE html>\n<html lang=\"" + i18n.S("zh-CN", "en") + "\" style=\"color-scheme: light dark\">\n<head>\n<meta charset=\"UTF-8\">\n<meta name=\"viewport\" content=\"width=device-width, initial-scale=1, maximum-scale=1, user-scalable=no\" />\n")
	s.WriteString("<title>" + html.EscapeString(b.title) + "</title>\n")
	s.WriteString(r.headLinks(b))
	issues := b.issues
	if b.fatal != "" {
		issues = append([]errorItem{{Level: "error", Source: "build", Message: firstLine(b.fatal), Detail: b.fatal}}, issues...)
	}
	if issues == nil {
		issues = []errorItem{}
	}
	if b.waiting {
		r.servePreparing(w, b, n)
		return
	}
	im, preload, react := b.importMap, b.preload, "development"
	if choice := reactChoice(req); choice != "development" {
		// Asked for explicitly, the page waits for the production packages; by default it doesn't (the first start
		// downloads them, seconds to tens of seconds: Watch starts that in the background) and loads development meanwhile
		wait := time.Duration(0)
		if choice == "production" {
			wait = 20 * time.Second
		}
		if p, st := r.prodFor(b, wait); p != nil {
			im, preload, react = p.importMap, p.preload, "production"
		} else {
			react = st
		}
	}
	s.WriteString("<script" + n + ">" + strings.Replace(errorsScript, "%s", string(mustJSON(issues)), 1) + "</script>\n")
	s.WriteString(`<style type="text/tailwindcss" id="__gerdr_css__">` + escapeStyle(b.css) + "</style>\n")
	s.WriteString(`<script type="importmap"` + n + `>` + string(mustJSON(map[string]any{"imports": im})) + "</script>\n")
	// After the import map: a modulepreload starts module loading, and an import map that comes later is ignored.
	for _, u := range append([]string{b.entry}, preload...) {
		s.WriteString(`<link rel="modulepreload" href="` + html.EscapeString(u) + `">` + "\n")
	}
	s.WriteString("<script" + n + ">" + sameStyleScript + "</script>\n")
	s.WriteString(`<script type="module" src="/_esm/` + tailwindBrowser + `"></script>` + "\n")
	s.WriteString("</head>\n<body>\n<div id=\"root\">" + loadingHTML(i18n.S("加载中…", "Loading…")) + "</div>\n<script" + n + ">")
	s.WriteString(loadingScript)
	s.WriteString("setTimeout(function(){var e=document.getElementById('__gerdr_loading_slow');if(e)e.style.visibility='visible'},5000);\n")
	s.WriteString("window.__GERDR_UI__ = " + string(mustJSON(map[string]any{
		"base":    r.base,
		"build":   b.id,
		"entry":   b.entry,
		"modules": b.manifest,
		"error":   b.fatal,
		"mounts":  b.mounts,
		// Which React this page runs on (prod.go): "development", "production", or, when production was asked for,
		// "preparing" (its packages are still being bundled) / "unavailable"
		"react": react,
	})) + ";</script>\n")
	s.WriteString(`<script type="module" src="` + r.base + `/_ui/m/@runtime.js?v=` + runtimeVersion + `"></script>` + "\n")
	if r.base != "" {
		// A mounted site (the Work project) has no rescue panel: gerdr around it still works when it breaks.
		s.WriteString("</body>\n</html>\n")
		w.Header().Set("content-type", "text/html; charset=utf-8")
		w.Header().Set("cache-control", "no-store")
		w.Write([]byte(s.String()))
		return
	}
	// 急救面板（internal/server/rescue）：编进二进制、自带 React，挂在主界面右边、和它不共享任何东西 ——
	// 界面目录怎么改坏都打得开。放在页面外壳里而不是界面目录里，就是为了谁也改不掉它。
	s.WriteString(`<script defer src="/rescue/app.js"></script>` + "\n")
	s.WriteString("</body>\n</html>\n")
	w.Header().Set("content-type", "text/html; charset=utf-8")
	w.Header().Set("cache-control", "no-store")
	w.Write([]byte(s.String()))
}

// servePreparing: the vendor bundle isn't built yet (first start, or a package was just added). Rather than load the
// packages per file from esm.sh meanwhile (slow, and they'd compete with the build for the same downloads), show the
// loading screen and reload once a new build is out: the hot reload stream says hello with the current build id.
func (r *Renderer) servePreparing(w http.ResponseWriter, b *build, n string) {
	var s strings.Builder
	s.WriteString("<!DOCTYPE html>\n<html lang=\"" + i18n.S("zh-CN", "en") + "\" style=\"color-scheme: light dark\">\n<head>\n<meta charset=\"UTF-8\">\n<meta name=\"viewport\" content=\"width=device-width, initial-scale=1, maximum-scale=1, user-scalable=no\" />\n")
	s.WriteString("<title>" + html.EscapeString(b.title) + "</title>\n" + r.headLinks(b) + "</head>\n<body>\n")
	s.WriteString(loadingHTML(i18n.S("正在准备界面用到的包（新装或加了包之后只这一次）…", "Preparing the packages the UI uses (only after an install or a new package)…")))
	s.WriteString("<script" + n + ">" + loadingScript + "var B=" + string(mustJSON(b.id)) + ";var es=new EventSource(" + string(mustJSON(r.base+"/_ui/events")) +
		");es.onmessage=function(e){try{if(JSON.parse(e.data).build!==B)location.reload()}catch(_){}};" +
		"setTimeout(function(){var e=document.getElementById('__gerdr_loading_slow');if(e)e.style.visibility='visible'},20000);</script>\n")
	s.WriteString("</body>\n</html>\n")
	w.Header().Set("content-type", "text/html; charset=utf-8")
	w.Header().Set("cache-control", "no-store")
	w.Write([]byte(s.String()))
}

func escapeStyle(css string) string { return strings.ReplaceAll(css, "</", "<\\/") }

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

// headLinks: the icon and the web app manifest, on every page (an install can start while the UI is still preparing).
func (r *Renderer) headLinks(b *build) string {
	s := `<link rel="manifest" href="` + html.EscapeString(r.base) + `/manifest.webmanifest" />` + "\n"
	if b.favicon != "" {
		s = `<link rel="icon" href="` + html.EscapeString(b.favicon) + `" />` + "\n" + s
	}
	return s
}

// ServeManifest: the web app manifest, from the same title / favicon as the page (ui.json). It is what lets a browser
// install Gerdr as an app: a phone's "add to home screen", and the desktop shell that runs the page in the user's own
// Chrome as an installed web app (its own window, Dock icon and name; tools/chromeshell). id and scope are fixed so an
// installed app stays the same app when the title or icon change.
func (r *Renderer) ServeManifest(w http.ResponseWriter, req *http.Request) {
	b := r.ensure()
	base := r.base + "/"
	m := map[string]any{
		"id": base, "start_url": base, "scope": base, "display": "standalone",
		"name": b.title, "short_name": b.title,
	}
	if b.favicon != "" {
		icon := map[string]string{"src": b.favicon, "sizes": "any"}
		if t := mime.TypeByExtension(path.Ext(b.favicon)); t != "" {
			icon["type"] = t
		}
		m["icons"] = []map[string]string{icon}
	}
	w.Header().Set("Content-Type", "application/manifest+json")
	w.Header().Set("Cache-Control", "no-cache")
	_ = json.NewEncoder(w).Encode(m)
}
