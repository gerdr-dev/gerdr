package server

import (
	_ "embed"
	"net/http"

	"github.com/gerdr-dev/gerdr/internal/webui"
)

// 急救面板：主界面（界面目录，人和 agent 都能改）改坏了的时候，从页面右侧拉出来，叫 agent 来修、或者先退回之前的一版。
// 脚本编进二进制（rescue/src 是源码，React 一起打进 app.js），由 uisite 的页面外壳直接引入，挂在主界面旁边、
// 和它不共享任何东西 —— 主界面怎么坏它都能用。为什么这样、怎么重新生成：rescue/build.go、rescue/src/main.tsx。
//
//go:generate go run ./rescue/build.go
//go:embed rescue/app.js
var rescueJS []byte

func (s *Server) rescueRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /rescue/app.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(rescueJS)
	})
	// 老入口（报错页上的链接、App 菜单、记住的网址）：回主界面并把面板打开
	mux.HandleFunc("GET /rescue", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/?rescue=1", http.StatusFound)
	})
	mux.HandleFunc("GET /api/ui/history", func(w http.ResponseWriter, r *http.Request) {
		list, err := webui.History(s.UI.Dir(), 15)
		if err != nil {
			httpError(w, 400, err.Error())
			return
		}
		writeJSON(w, list)
	})
	mux.HandleFunc("POST /api/ui/restore", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Sha string `json:"sha"`
		}
		if err := decode(r, &b); err != nil {
			httpError(w, 400, err.Error())
			return
		}
		saved, err := webui.Restore(s.UI.Dir(), b.Sha)
		if err != nil {
			httpError(w, 400, err.Error())
			return
		}
		s.Log.Info("ui restored", "to", b.Sha, "saved", saved)
		writeJSON(w, map[string]any{"saved": saved})
	})
}
