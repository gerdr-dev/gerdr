package server

import (
	"context"
	"encoding/base64"
	"net/http"
	"time"

	"github.com/gerdr-dev/gerdr/agents/native"
	"github.com/gerdr-dev/gerdr/agents/native/llm"
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
	// The thinking levels a model gets and where they come from (its config, the provider's model list, the id
	// guess): the settings page's level editor. Provider and model as on the page (an unsaved draft works too).
	mux.HandleFunc("POST /api/llm/efforts", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Provider native.Provider `json:"provider"`
			Model    native.Model    `json:"model"`
		}
		if err := decode(r, &b); err != nil {
			httpError(w, 400, err.Error())
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		writeJSON(w, native.ModelEfforts(ctx, agentwire.WithKey(b.Provider, s.Secrets), b.Model))
	})
	mux.HandleFunc("POST /api/image", s.generateImage)
	mux.HandleFunc("GET /api/image/models", func(w http.ResponseWriter, r *http.Request) {
		cfg := s.Hub.LLM()
		ms := cfg.ImageModels()
		if ms == nil {
			ms = []native.ImageModel{}
		}
		out := map[string]any{"models": ms, "configured": cfg.ImageModel}
		if _, err := cfg.ResolveImageModel(""); err != nil {
			out["error"] = err.Error()
		}
		writeJSON(w, out)
	})
}

type imageBlob struct {
	MIME string `json:"mime"`
	Data string `json:"data"` // base64
}

// POST /api/image: `gerdr image gen`. The backend makes the request so the keys stay here (agents' environments
// don't have them); the images go back in the response and the CLI writes them where it runs, which also works
// for a paired computer reached through /h/<host>/.
func (s *Server) generateImage(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Prompt string      `json:"prompt"`
		Images []imageBlob `json:"images"`
		Aspect string      `json:"aspect"`
		Size   string      `json:"size"`
		N      int         `json:"n"`
		Model  string      `json:"model"`
	}
	if err := decode(r, &b); err != nil {
		httpError(w, 400, err.Error())
		return
	}
	req := llm.ImageRequest{Prompt: b.Prompt, Aspect: b.Aspect, Size: b.Size, N: b.N}
	for _, im := range b.Images {
		req.Images = append(req.Images, llm.Part{Type: "image", MIME: im.MIME, Data: im.Data})
	}
	res, key, err := native.GenerateImage(r.Context(), s.Hub.LLM(), b.Model, req)
	if err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	out := []imageBlob{}
	for _, im := range res.Images {
		out = append(out, imageBlob{MIME: im.MIME, Data: base64.StdEncoding.EncodeToString(im.Data)})
	}
	writeJSON(w, map[string]any{"model": key, "text": res.Text, "images": out})
}
