package uisite

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

// runtimeJS 是页面里的运行时（/_ui/m/@runtime.js），照 shuttle 的 localsite/runtime.go：
//
//  1. 先接上 React Refresh（injectIntoGlobalHook 必须在 react-dom 加载之前，所以 react-dom 用动态 import）；
//  2. import 入口，把每个已加载模块导出的组件注册给 Refresh（按「路径 导出名」），再用入口的 createApp 渲染；
//  3. 订阅 /_ui/events：
//     - 能热替换（boundaries）：重新 import 这些模块的新 URL、注册、performReactRefresh()，页面状态保留；
//     之后冒出 hooks 相关的报错（改了 hooks 的个数或顺序）就卸掉 root、新建一个重新渲染入口；
//     - 不能（boundaries 为 null）：import 新入口重新渲染（状态丢掉）；
//     - css：只换 <style id="__gerdr_css__">，Tailwind browser 自己重新生成；
//     - error：构建出错，盖一层报错，修好后刷新一次；reload：importMap 变了，整页刷新。
//
// 组件注册在运行时这边做（import 之后对导出注册），Go 不改写源码：没导出的内部组件改了会重新挂载。
const runtimeJS = `const S = window.__GERDR_UI__;
const errors = (window.__GERDR_UI_ERRORS__ = window.__GERDR_UI_ERRORS__ || []);
function report(source, e) {
  if (window.__GERDR_REPORT__) window.__GERDR_REPORT__(source, (e && e.message) || e, (e && e.stack) || "");
  else errors.push({ level: "error", source, message: String((e && e.message) || e), detail: (e && e.stack) || "" });
}
function overlay(text) {
  let el = document.getElementById("__gerdr_overlay");
  if (!text) { if (el) el.remove(); return; }
  if (!el) {
    el = document.createElement("pre");
    el.id = "__gerdr_overlay";
    el.style.cssText = "position:fixed;inset:0;z-index:2147483647;margin:0;padding:24px;overflow:auto;white-space:pre-wrap;font:12px/1.6 ui-monospace,monospace;color:#e5484d;background:rgba(10,10,10,.94)";
    document.body.appendChild(el);
  }
  el.textContent = "界面代码编译出错（改好之后自动恢复）：\n\n" + text;
}

let R = null;
try {
  const m = await import("react-refresh/runtime");
  R = m.default || m;
  R.injectIntoGlobalHook(window);
} catch (e) {
  console.warn("[gerdr-ui] React Refresh unavailable, updates will re-render the page", e);
}
window.$RefreshReg$ = () => {};
window.$RefreshSig$ = () => (t) => t;

const isComponent = (k, v) => (k === "default" || /^[A-Z]/.test(k)) && (typeof v === "function" || (v && typeof v === "object" && v.$$typeof));
async function register(path, url) {
  const ns = await import(url);
  if (R) for (const k of Object.keys(ns)) if (isComponent(k, ns[k])) R.register(ns[k], path + " " + k);
  return ns;
}

let mods = S.modules, build = S.build, broken = !!S.error, root = null;
const { createRoot } = await import("react-dom/client");
const hooksError = (m) => /hook|Should have a queue/i.test(String(m || ""));
async function mount(entry) {
  const app = await import(entry);
  await Promise.all(Object.entries(mods).filter(([, m]) => !m.lazy).map(([p, m]) => register(p, m.url)));
  if (!root) root = createRoot(document.getElementById("root"));
  root.render(app.createApp());
}

if (broken) overlay(S.error);
else await mount(S.entry).catch((e) => { report("render", e); overlay(String((e && e.stack) || e)); broken = true; });

async function apply(u) {
  if (u.build === build) return;
  if (u.type === "hello") return location.reload();
  if (u.type === "reload") return location.reload();
  if (u.type === "error") {
    build = u.build;
    broken = true;
    overlay(u.error);
    report("build", { message: u.error.split("\n")[0], stack: u.error });
    return;
  }
  if (broken) return location.reload();
  if ((window.__GERDR_ROUTE_ERRORS__ || 0) > 0) return location.reload();
  const prev = mods;
  mods = u.modules;
  build = u.build;
  if (u.css != null) { const el = document.getElementById("__gerdr_css__"); if (el) el.textContent = u.css; }
  if (u.boundaries && R) {
    if (!u.boundaries.length) return;
    const seen = errors.length;
    for (const p of u.boundaries) {
      if (prev[p]) await register(p, prev[p].url);
      await register(p, mods[p].url);
    }
    let failed = false;
    const held = (window.__GERDR_HOLD__ = []);
    try {
      try { R.performReactRefresh(); } catch (e) { if (!hooksError(e && e.message)) throw e; failed = true; }
      await new Promise((r) => setTimeout(r, 300));
      if (failed || held.length || errors.slice(seen).some((e) => hooksError(e.message))) {
        console.warn("[gerdr-ui] hooks changed, remounting");
        if (root) { root.unmount(); root = null; }
        await mount(u.entry);
        for (let i = errors.length - 1; i >= seen; i--) if (hooksError(errors[i].message) || hooksError(errors[i].detail)) errors.splice(i, 1);
        held.length = 0;
      }
    } finally {
      window.__GERDR_HOLD__ = null;
      for (const it of held) errors.push(it);
    }
    return;
  }
  await mount(u.entry);
}

let chain = Promise.resolve();
const es = new EventSource("/_ui/events");
es.onmessage = (e) => {
  const u = JSON.parse(e.data);
  chain = chain.then(() => apply(u)).catch((err) => { console.error("[gerdr-ui] hot update failed, reloading", err); location.reload(); });
};
`

var runtimeVersion = func() string {
	s := sha256.Sum256([]byte(runtimeJS))
	return hex.EncodeToString(s[:6])
}()

// Tailwind browser 编不了 @import（除了 tailwindcss 本身，它自带）和 @plugin；@source 也用不着（它直接扫页面上的 class）。
var cssUnsupportedRE = regexp.MustCompile(`(?m)^\s*@(import|plugin|source)\s+[^;]*;\s*\n?`)

func browserCSS(css string) (out string, dropped []string) {
	out = cssUnsupportedRE.ReplaceAllStringFunc(css, func(line string) string {
		t := strings.TrimSpace(line)
		if t != `@import "tailwindcss";` && t != `@import 'tailwindcss';` && !strings.HasPrefix(t, "@source") {
			dropped = append(dropped, t)
		}
		return ""
	})
	return out, dropped
}
