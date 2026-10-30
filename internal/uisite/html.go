package uisite

import (
	"encoding/json"
	"html"
	"net/http"
	"strings"
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

const loadingHTML = `<div id="__gerdr_loading" style="position:fixed;inset:0;display:flex;flex-direction:column;align-items:center;justify-content:center;gap:12px;font:13px/1.5 system-ui,-apple-system,sans-serif;color:CanvasText;background:Canvas">` +
	`<style>@keyframes __gerdr_spin{to{transform:rotate(360deg)}}</style>` +
	`<div style="width:22px;height:22px;border-radius:50%;border:2px solid color-mix(in srgb,CanvasText 15%,transparent);border-top-color:color-mix(in srgb,CanvasText 60%,transparent);animation:__gerdr_spin .8s linear infinite"></div>` +
	`<div style="opacity:.6">加载中…</div>` +
	`<div id="__gerdr_loading_slow" style="opacity:.45;max-width:320px;text-align:center;visibility:hidden">第一次打开要下载界面用到的包，可能要一会儿。一直打不开的话：<a href="/rescue" onclick="if(window.__gerdrRescue){__gerdrRescue.open();return false}" style="color:inherit">急救面板</a></div>` +
	`</div>`

// ServePage 出页面：importMap + 模块清单（window.__GERDR_UI__）+ 运行时，运行时 import 入口再渲染。
// nonce：内联脚本靠它过 CSP（importMap 只能内联）。
func (r *Renderer) ServePage(w http.ResponseWriter, req *http.Request, nonce string) {
	b := r.ensure()
	n := ` nonce="` + nonce + `"`
	var s strings.Builder
	s.WriteString("<!DOCTYPE html>\n<html lang=\"zh-CN\" style=\"color-scheme: light dark\">\n<head>\n<meta charset=\"UTF-8\">\n<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\" />\n")
	s.WriteString("<title>" + html.EscapeString(b.title) + "</title>\n")
	if b.favicon != "" {
		s.WriteString(`<link rel="icon" href="` + html.EscapeString(b.favicon) + `" />` + "\n")
	}
	issues := b.issues
	if b.fatal != "" {
		issues = append([]errorItem{{Level: "error", Source: "build", Message: firstLine(b.fatal), Detail: b.fatal}}, issues...)
	}
	if issues == nil {
		issues = []errorItem{}
	}
	s.WriteString("<script" + n + ">" + strings.Replace(errorsScript, "%s", string(mustJSON(issues)), 1) + "</script>\n")
	s.WriteString(`<style type="text/tailwindcss" id="__gerdr_css__">` + escapeStyle(b.css) + "</style>\n")
	s.WriteString(`<script type="importmap"` + n + `>` + string(mustJSON(map[string]any{"imports": b.importMap})) + "</script>\n")
	s.WriteString(`<script type="module" src="/_esm/` + tailwindBrowser + `"></script>` + "\n")
	s.WriteString("</head>\n<body>\n<div id=\"root\">" + loadingHTML + "</div>\n<script" + n + ">")
	s.WriteString("setTimeout(function(){var e=document.getElementById('__gerdr_loading_slow');if(e)e.style.visibility='visible'},5000);\n")
	s.WriteString("window.__GERDR_UI__ = " + string(mustJSON(map[string]any{
		"base":    r.base,
		"build":   b.id,
		"entry":   b.entry,
		"modules": b.manifest,
		"error":   b.fatal,
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

func escapeStyle(css string) string { return strings.ReplaceAll(css, "</", "<\\/") }

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
