package server

import (
	"net/http"

	"github.com/gerdr-dev/gerdr/agents/native"
	"github.com/gerdr-dev/gerdr/internal/memstore"
)

// 设置 →「记忆」：三家 agent 的长期记忆（Gerdr Agent、Claude Code、Codex），看、改、删。
// 只认它们记忆目录里的 .md（memstore.Allowed）。
func (s *Server) memoryRoutes(mux *http.ServeMux) {
	roots := func() memstore.Roots { return memstore.DefaultRoots(s.Dir) }
	mux.HandleFunc("GET /api/memory", func(w http.ResponseWriter, r *http.Request) {
		var cwds []string
		for _, x := range s.Hub.List() {
			cwds = append(cwds, x.Cwd)
		}
		writeJSON(w, map[string]any{"stores": memstore.List(roots(), cwds, native.ProjectMemorySlug)})
	})
	mux.HandleFunc("GET /api/memory/file", func(w http.ResponseWriter, r *http.Request) {
		c, err := memstore.Read(roots(), r.URL.Query().Get("path"))
		if err != nil {
			httpError(w, 400, err.Error())
			return
		}
		writeJSON(w, map[string]any{"content": c})
	})
	mux.HandleFunc("PUT /api/memory/file", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if err := decode(r, &b); err != nil {
			httpError(w, 400, err.Error())
			return
		}
		if err := memstore.Write(roots(), b.Path, b.Content); err != nil {
			httpError(w, 400, err.Error())
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	})
	mux.HandleFunc("DELETE /api/memory/file", func(w http.ResponseWriter, r *http.Request) {
		if err := memstore.Delete(roots(), r.URL.Query().Get("path")); err != nil {
			httpError(w, 400, err.Error())
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	})
}
