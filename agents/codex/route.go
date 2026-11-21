package codex

import (
	"context"
	"encoding/json"

	"github.com/gerdr-dev/gerdr/rpcline"
)

// Routes hands a thread's model API traffic to someone serving its address locally (in gerdr: an add-on's service,
// internal/addons/routes.go).
type Routes interface {
	// Rewrite returns the address to use instead of base (the provider's base URL codex would use), "" to keep it.
	Rewrite(base string) string
}

// route works out where this thread's model requests should go instead, and the thread config override that sends
// them there: the address of the provider codex is set to use, as config/read reports it for cwd (profiles, -c
// arguments, project layers all applied), overridden for the thread only. Tested with codex 0.166: a thread's
// config may change the active provider's base_url (model_providers.<id>.base_url, or openai_base_url for the
// built-in openai one) but not which provider is active (turn/start then fails with modelProviderMismatch), so only
// the address is touched. A built-in provider without an address set (signed in with ChatGPT) is never routed.
func (a *Agent) route(ctx context.Context, rpc *rpcline.Conn, cwd string) (key, to string) {
	r := a.opts.Routes
	if r == nil {
		return "", ""
	}
	var res struct {
		Config struct {
			ModelProvider  *string `json:"model_provider"`
			OpenAIBaseURL  *string `json:"openai_base_url"`
			ModelProviders map[string]struct {
				BaseURL *string `json:"base_url"`
			} `json:"model_providers"`
		} `json:"config"`
	}
	p := map[string]any{}
	if cwd != "" {
		p["cwd"] = cwd
	}
	if err := rpc.Call(ctx, "config/read", p, &res); err != nil {
		a.log.Warn("config/read for api routes failed", "err", err)
		return "", ""
	}
	c := res.Config
	provider := "openai"
	if c.ModelProvider != nil && *c.ModelProvider != "" {
		provider = *c.ModelProvider
	}
	if mp, ok := c.ModelProviders[provider]; ok {
		if mp.BaseURL == nil || *mp.BaseURL == "" {
			return "", ""
		}
		if to := r.Rewrite(*mp.BaseURL); to != "" {
			return "model_providers." + provider + ".base_url", to
		}
		return "", ""
	}
	if provider == "openai" && c.OpenAIBaseURL != nil && *c.OpenAIBaseURL != "" {
		if to := r.Rewrite(*c.OpenAIBaseURL); to != "" {
			return "openai_base_url", to
		}
	}
	return "", ""
}

func routeFP(key, to string) string {
	if to == "" {
		return ""
	}
	b, _ := json.Marshal([]string{key, to})
	return "\x00route:" + string(b)
}
