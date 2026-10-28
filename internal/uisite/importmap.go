package uisite

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// importMap：第三方包怎么加载。没有 node_modules、没有打包 —— 浏览器按 importMap 从 /_esm/（本机代理 + 磁盘缓存，
// esm.go）取 esm.sh 编好的 ESM。
//
// 只给代码里真用到的 specifier 生成条目（编译时 esbuild 报上来的，modules.go 的 Bare），版本取 package.json
// 的 dependencies（写死精确版本：CDN 按版本缓存，范围版本会今天明天拿到不一样的包）。
//
// 几条规矩，都是为了「全页面只有一份 React」：
//   - react / react-dom 用开发版（?dev）：React Refresh 要它，报错也全；
//   - 别的包一律 external=react,react-dom：它们 import 的 react 走 importMap，不让 esm.sh 自己再塞一份；
//   - react-dom 自己也 external=react（不然 esm.sh 按它的 peer 范围挑一个 react 版本，和我们的可能不是同一个）。

// 运行时自己要的包，不在界面的 package.json 里。
var builtinPackages = map[string]string{
	"react-refresh": "0.19.0",
}

const tailwindBrowser = "@tailwindcss/browser@4.1.11"

type packageJSON struct {
	Dependencies map[string]string `json:"dependencies"`
}

func readPackageJSON(dir string) (map[string]string, error) {
	b, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return nil, fmt.Errorf("读不到 package.json：%w", err)
	}
	var p packageJSON
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("package.json 写错了：%w", err)
	}
	out := map[string]string{}
	for k, v := range p.Dependencies {
		out[k] = cleanVersion(v)
	}
	for k, v := range builtinPackages {
		if _, ok := out[k]; !ok {
			out[k] = v
		}
	}
	return out, nil
}

// cleanVersion：^1.2.3 → 1.2.3（esm.sh 认范围，但精确版本 URL 才稳定）。
func cleanVersion(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimLeft(v, "^~=v")
	return v
}

// packageName：@scope/name/sub → @scope/name；name/sub → name。
func packageName(spec string) (pkg, sub string) {
	parts := strings.SplitN(spec, "/", 3)
	if strings.HasPrefix(spec, "@") && len(parts) >= 2 {
		pkg = parts[0] + "/" + parts[1]
		if len(parts) == 3 {
			sub = "/" + parts[2]
		}
		return
	}
	pkg = parts[0]
	if i := strings.Index(spec, "/"); i >= 0 {
		sub = spec[i:]
	}
	return
}

func esmQuery(pkg string) string {
	switch pkg {
	case "react", "react-refresh":
		return "dev"
	case "react-dom":
		return "dev&external=react"
	}
	return "dev&external=react,react-dom"
}

func esmURL(pkg, ver, sub string) string {
	return "/_esm/" + pkg + "@" + ver + sub + "?" + esmQuery(pkg)
}

// buildImportMap：bare 是代码里用到的第三方 specifier。缺版本的（package.json 里没写）报出来。
func buildImportMap(deps map[string]string, bare []string) (map[string]string, []string) {
	im := map[string]string{}
	var missing []string
	add := func(spec string) {
		pkg, sub := packageName(spec)
		ver, ok := deps[pkg]
		if !ok {
			missing = append(missing, pkg)
			return
		}
		im[spec] = esmURL(pkg, ver, sub)
	}
	// React 那几个总是要有：运行时用、JSX 编译出来的代码用、第三方包 external 掉的也指望它们。
	for _, s := range []string{"react", "react/jsx-runtime", "react/jsx-dev-runtime", "react-dom", "react-dom/client", "react-refresh/runtime", "react-router"} {
		add(s)
	}
	for _, s := range bare {
		add(s)
	}
	// 第三方包里 external 掉的 react 子路径（react/compiler-runtime 之类）：前缀映射兜底。
	for _, pkg := range []string{"react", "react-dom"} {
		if ver, ok := deps[pkg]; ok {
			im[pkg+"/"] = "/_esm/" + pkg + "@" + ver + "&" + esmQuery(pkg) + "/"
		}
	}
	sort.Strings(missing)
	return im, uniq(missing)
}
