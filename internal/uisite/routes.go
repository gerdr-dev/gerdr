package uisite

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/evanw/esbuild/pkg/api"
)

// route 是 src/pages/ 下的一个页面：
// src/pages/index.tsx → /，src/pages/settings.tsx → /settings，src/pages/s/[id].tsx → /s/:id，末段 index 表示所在目录，
// src/pages/404.tsx 是兜底。src/layout.tsx（可选）是所有页面外面那一层（放 <Outlet />）。
type route struct {
	File string
	Path string
}

var pageExts = map[string]bool{".tsx": true, ".ts": true, ".jsx": true, ".js": true}

const pagesDir = "src/pages/"
const layoutFile = "src/layout.tsx"

func buildRoutes(files []string) []route {
	var out []route
	seen := map[string]bool{}
	var list []string
	for _, f := range files {
		if strings.HasPrefix(f, pagesDir) {
			list = append(list, f)
		}
	}
	sort.Strings(list)
	for _, f := range list {
		rel := strings.TrimPrefix(f, pagesDir)
		ext := path.Ext(rel)
		if !pageExts[ext] || strings.HasSuffix(rel, ".d.ts") {
			continue
		}
		rel = strings.TrimSuffix(rel, ext)
		segs := strings.Split(rel, "/")
		// 下划线开头的文件 / 目录不是页面（放页面自己用的小组件）
		skip := false
		for i, s := range segs {
			if strings.HasPrefix(s, "_") {
				skip = true
			}
			if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
				segs[i] = ":" + s[1:len(s)-1]
			}
		}
		if skip {
			continue
		}
		if strings.EqualFold(segs[len(segs)-1], "index") {
			segs = segs[:len(segs)-1]
		}
		p := "/" + strings.Join(segs, "/")
		if seen[strings.ToLower(p)] {
			continue
		}
		seen[strings.ToLower(p)] = true
		out = append(out, route{File: f, Path: p})
	}
	return out
}

// entrySource 是入口模块（/_ui/m/@entry.js）：每个页面一条 react-router 路由，页面组件收到 { params }。
// 它不自己挂载，导出 createApp() 交给运行时（runtime.go）：运行时先接好 React Refresh 再渲染，
// 热更新整个换掉时也是拿新入口的 createApp 重新渲染。
func entrySource(routes []route, layout string, url func(file string) string) string {
	var b strings.Builder
	b.WriteString(`import React from "react";
import { createBrowserRouter, RouterProvider, Outlet, useParams, useRouteError } from "react-router";
`)
	for i, r := range routes {
		fmt.Fprintf(&b, "import P%d from %q;\n", i, url(r.File))
	}
	if layout != "" {
		fmt.Fprintf(&b, "import Layout from %q;\n", url(layout))
	} else {
		b.WriteString("const Layout = Outlet;\n")
	}
	b.WriteString(`
const h = React.createElement;
function report(e, source) {
  try { window.__GERDR_REPORT__ && window.__GERDR_REPORT__(source, (e && e.message) || e, (e && e.stack) || ""); } catch (_) {}
}
// 渲染出错：整页一个报错框（DevTools 里有 sourcemap，堆栈能点回源码）。挂着的时候记在
// __GERDR_ROUTE_ERRORS__ 里：已经出错的错误边界热替换救不回来，运行时看到它就整页刷新。
function RouteError() {
  const e = useRouteError();
  const text = String((e && (e.stack || e.message)) || e);
  React.useEffect(function () {
    window.__GERDR_ROUTE_ERRORS__ = (window.__GERDR_ROUTE_ERRORS__ || 0) + 1;
    report(e, "render");
    return function () { window.__GERDR_ROUTE_ERRORS__--; };
  }, [e]);
  return h("div", { style: { padding: 24, font: "12px/1.6 ui-monospace, monospace" } },
    h("pre", { style: { margin: 0, whiteSpace: "pre-wrap", color: "#e5484d" } }, text),
    h("p", { style: { marginTop: 16, opacity: 0.6, fontFamily: "system-ui" } }, "界面代码出错了。改好之后会自动刷新。"),
    h("a", { href: "/rescue", style: { fontFamily: "system-ui", fontSize: 14, color: "#7aa2f7" } }, "打开急救面板，叫 agent 来修 →"));
}
function NotFound() {
  return h("div", { style: { padding: 24, font: "14px system-ui", opacity: 0.6 } }, "404 · " + location.pathname);
}
function page(C) {
  return function Page() { return h(C, { params: useParams() }); };
}
const children = [
`)
	notFound := "NotFound"
	for i, r := range routes {
		if r.Path == "/" {
			fmt.Fprintf(&b, "  { index: true, Component: page(P%d) },\n", i)
			continue
		}
		fmt.Fprintf(&b, "  { path: %q, Component: page(P%d) },\n", strings.TrimPrefix(r.Path, "/"), i)
		if r.Path == "/404" {
			notFound = fmt.Sprintf("page(P%d)", i)
		}
	}
	fmt.Fprintf(&b, "  { path: \"*\", Component: %s },\n", notFound)
	b.WriteString(`];

export function createApp() {
  const router = createBrowserRouter([{ path: "/", Component: Layout, ErrorBoundary: RouteError, children: children }]);
  return h(RouterProvider, { router: router });
}
`)
	return b.String()
}

func formatMessage(m api.Message) string {
	if m.Location == nil {
		return m.Text
	}
	l := m.Location
	return fmt.Sprintf("%s:%d:%d: %s\n    %s", l.File, l.Line, l.Column, m.Text, strings.TrimSpace(l.LineText))
}
