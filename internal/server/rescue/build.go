//go:build ignore

// 把急救面板（rescue/src，React）打成一个 app.js，编进二进制。
//
// 主界面不打包、浏览器现从 esm.sh 拉 React；急救面板不能这样 —— 它要在主界面坏了的时候还能开，
// 主界面可能就坏在 esm.sh 拉不下来、界面目录里的包版本被改乱上。所以这里在编 gerdr 之前把 React 一起打进去，
// 运行时什么都不依赖。包从 ui/node_modules 取（先 cd ui && pnpm install），版本跟主界面一致。
//
// 改了 rescue/src 跑：go generate ./internal/server（或 make rescue），产物 app.js 入库 —— 编 gerdr 不需要 node。
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/evanw/esbuild/pkg/api"
)

func main() {
	// go generate 在 internal/server 里跑
	root, _ := filepath.Abs("../..")
	mods := filepath.Join(root, "ui", "node_modules")
	if _, err := os.Stat(filepath.Join(mods, "react")); err != nil {
		fmt.Fprintln(os.Stderr, "找不到 ui/node_modules/react：先 cd ui && pnpm install")
		os.Exit(1)
	}
	res := api.Build(api.BuildOptions{
		EntryPoints:       []string{"rescue/src/main.tsx"},
		Outfile:           "rescue/app.js",
		Bundle:            true,
		Write:             true,
		MinifyWhitespace:  true,
		MinifySyntax:      true,
		MinifyIdentifiers: true,
		Format:            api.FormatIIFE,
		Target:            api.ES2020,
		JSX:               api.JSXAutomatic,
		NodePaths:         []string{mods},
		Define:            map[string]string{"process.env.NODE_ENV": `"production"`},
		LegalComments:     api.LegalCommentsNone,
		Banner:            map[string]string{"js": "// 生成的文件，别手改：源码在 rescue/src，go generate ./internal/server 重新生成"},
	})
	for _, e := range res.Errors {
		loc := ""
		if e.Location != nil {
			loc = fmt.Sprintf("%s:%d: ", e.Location.File, e.Location.Line)
		}
		fmt.Fprintln(os.Stderr, loc+e.Text)
	}
	if len(res.Errors) > 0 {
		os.Exit(1)
	}
}
