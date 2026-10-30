package server

import (
	"net/http"

	"github.com/gerdr-dev/gerdr/agents/native"
	"github.com/gerdr-dev/gerdr/internal/agentwire"
)

// Gerdr Agent（native）的模型设置页用的探测接口。配置本身走 config.json（PUT /api/config），这里只是帮着填：
// 拉服务商的模型列表、拿一个模型试说一句。服务商的参数由页面带上来（还没保存的草稿也能试）。
// A saved key comes up as config.Hidden; WithKey swaps in the real one.
func (s *Server) llmRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/llm/models", func(w http.ResponseWriter, r *http.Request) {
		var p native.Provider
		if err := decode(r, &p); err != nil {
			httpError(w, 400, err.Error())
			return
		}
		ids, err := native.ListModels(r.Context(), agentwire.WithKey(p, s.Secrets))
		if err != nil {
			httpError(w, 502, err.Error())
			return
		}
		writeJSON(w, map[string]any{"models": ids})
	})
	mux.HandleFunc("POST /api/llm/test", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Provider native.Provider `json:"provider"`
			Model    native.Model    `json:"model"`
		}
		if err := decode(r, &b); err != nil {
			httpError(w, 400, err.Error())
			return
		}
		reply, api, err := native.TestModel(r.Context(), agentwire.WithKey(b.Provider, s.Secrets), b.Model)
		if err != nil {
			httpError(w, 502, err.Error())
			return
		}
		writeJSON(w, map[string]any{"reply": reply, "api": api})
	})
}
