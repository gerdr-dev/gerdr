package server

import (
	"net/http"

	"github.com/gerdr-dev/gerdr/internal/agents/native"
)

// Gerdr Agent（native）的模型设置页用的探测接口。配置本身走 config.json（PUT /api/config），这里只是帮着填：
// 拉服务商的模型列表、拿一个模型试说一句。服务商的参数由页面带上来（还没保存的草稿也能试）。
func (s *Server) llmRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/llm/models", func(w http.ResponseWriter, r *http.Request) {
		var p native.Provider
		if err := decode(r, &p); err != nil {
			httpError(w, 400, err.Error())
			return
		}
		ids, err := native.ListModels(r.Context(), p)
		if err != nil {
			httpError(w, 502, err.Error())
			return
		}
		writeJSON(w, map[string]any{"models": ids})
	})
	// Gerdr Agent 实际在用的 MCP（设置 → MCP 页）。probe=1：现在就连一遍看看（最多等 30 秒）
	mux.HandleFunc("GET /api/llm/mcp", func(w http.ResponseWriter, r *http.Request) {
		// cwd 给空：只算用户级的（和页面下面「Claude Code 自己的」那一节对得上；家目录也算一个项目，给家目录会混进只对它生效的那些）
		list, err := s.Hub.NativeMCP(r.Context(), "", r.URL.Query().Get("probe") == "1")
		if err != nil {
			httpError(w, 502, err.Error())
			return
		}
		writeJSON(w, map[string]any{"servers": list})
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
		reply, api, err := native.TestModel(r.Context(), b.Provider, b.Model)
		if err != nil {
			httpError(w, 502, err.Error())
			return
		}
		writeJSON(w, map[string]any{"reply": reply, "api": api})
	})
}
