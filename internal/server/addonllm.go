package server

// g.llm: one short call to a model configured in Gerdr (Settings → Models, config.json llm), made by the backend
// so the keys never reach the page. Asked for by the voice add-on: a fast model fixes what speech recognition
// misheard ("对不起" where the conversation is about alignment, "对不齐") using the conversation as context —
// something a phrase list can't do. General on purpose: any add-on that needs a model (summaries, titles,
// translation) uses the person's own providers instead of asking for another key.
// No tools and no history: native.Complete, thinking as low as the model goes.

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gerdr-dev/gerdr/agents/native"
	"github.com/gerdr-dev/gerdr/agents/native/llm"
)

type addonLLMReq struct {
	Model    string `json:"model"` // provider/model; "" = the default model
	System   string `json:"system"`
	Messages []struct {
		Role    string `json:"role"` // user | assistant
		Content string `json:"content"`
	} `json:"messages"`
	MaxTokens int    `json:"maxTokens"`
	Effort    string `json:"effort"` // "" = off (quick jobs); else as in a session's config
}

func (s *Server) addonLLM(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.addonByID(id) == nil {
		httpError(w, 404, "no such add-on: "+id)
		return
	}
	var b addonLLMReq
	if err := decode(r, &b); err != nil {
		httpError(w, 400, err.Error())
		return
	}
	var msgs []llm.Message
	for _, m := range b.Messages {
		role := m.Role
		if role != "assistant" {
			role = "user"
		}
		msgs = append(msgs, llm.Message{Role: role, Content: []llm.Part{llm.TextPart(m.Content)}, Text: m.Content})
	}
	if len(msgs) == 0 {
		httpError(w, 400, "messages is empty")
		return
	}
	effort := b.Effort
	if effort == "" {
		effort = "off"
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	start := time.Now()
	req := llm.Request{System: b.System, Messages: msgs, MaxTokens: b.MaxTokens}
	res, err := native.Complete(ctx, s.Hub.LLM(), b.Model, effort, req)
	if e := strings.ToLower(errStr(err)); effort == "off" && (strings.Contains(e, "thinking") || strings.Contains(e, "effort")) {
		// Some models refuse thinking off (Claude Fable: "thinking.type.disabled is not supported"; GLM through a
		// gateway: "did not match any variant of ... ReasoningEffort"): their default then
		res, err = native.Complete(ctx, s.Hub.LLM(), b.Model, "", req)
	}
	if err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, map[string]any{"text": res.Message.Text, "ms": time.Since(start).Milliseconds()})
}

// GET /api/addons/{id}/llm/models: the configured models ("provider/model"), for an add-on's model picker.
func (s *Server) addonLLMModels(w http.ResponseWriter, r *http.Request) {
	out := s.Hub.LLM().ModelKeys()
	if out == nil {
		out = []string{}
	}
	writeJSON(w, out)
}

func errStr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
