package server

import (
	"embed"
	"net/http"

	"github.com/zbysir/gerdr/internal/webui"
)

// 急救面板：主界面（界面目录，人和 agent 都能改）改坏了的时候，在这一页叫 agent 来修、或者先退回之前的一版。
// 页面编进二进制、不走 uisite（不编译界面目录、不从 esm.sh 拉包），所以主界面怎么坏它都打得开。
// 入口：主界面的报错页（uisite 生成的，也不在界面目录里）、App 菜单「帮助 → 急救」、直接打 /rescue。
//
//go:embed rescue
var rescueFS embed.FS

func (s *Server) rescueRoutes(mux *http.ServeMux) {
	serve := func(name, ctype string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			b, _ := rescueFS.ReadFile("rescue/" + name)
			w.Header().Set("Content-Type", ctype)
			w.Header().Set("Cache-Control", "no-store")
			_, _ = w.Write(b)
		}
	}
	mux.HandleFunc("GET /rescue", serve("index.html", "text/html; charset=utf-8"))
	mux.HandleFunc("GET /rescue/app.js", serve("app.js", "text/javascript; charset=utf-8"))
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
