package native

import (
	"context"
	"fmt"

	"github.com/gerdr-dev/gerdr/agents/native/llm"
)

// Complete runs one model call outside any session: no tools, no transcript. model is "provider/model" ("" =
// the default model, then the first), effort as in session config ("" = the model's default, "off" = thinking
// off). Model, Thinking, Effort and MaxTokens are filled in from the configuration; the caller sets System,
// Messages (images as llm.Part{Type: "image"}) and anything else.
func Complete(ctx context.Context, cfg Config, model, effort string, req llm.Request) (llm.Result, error) {
	m, ok := cfg.find(model)
	if !ok {
		return llm.Result{}, Available(cfg)
	}
	if model != "" && m.Key != model {
		return llm.Result{}, fmt.Errorf("no model %q in the configuration", model)
	}
	req.Model, req.Thinking, req.Effort = m.ID, m.Thinking, effortFor(m, effort)
	if req.MaxTokens == 0 {
		req.MaxTokens = m.MaxOutput
	}
	return m.client().Stream(ctx, req, func(llm.Event) {})
}
