package native

import (
	"os"
	"regexp"
	"strings"

	"github.com/gerdr-dev/gerdr/internal/agents/native/llm"
	"github.com/gerdr-dev/gerdr/internal/config"
)

// Config 是 config.json 里的 "llm"：模型服务商和模型。设置一改下一轮就用新的（Agent 每次现取）。
//
//	"llm": {
//	  "providers": [{
//	    "id": "subhub", "name": "SubHub", "baseUrl": "https://example.com/v1", "apiKey": "sk-…",
//	    "models": [{"id": "gpt-6.1-sol"}, {"id": "deepseek-v4-pro"}]
//	  }, {
//	    "id": "anthropic", "api": "anthropic", "baseUrl": "https://api.anthropic.com", "apiKey": "sk-ant-…",
//	    "models": [{"id": "claude-opus-5-5"}]
//	  }],
//	  "defaultModel": "subhub/gpt-6.1-sol"
//	}
//
// 模型只写 id 就行：协议、思考档位、上下文窗口、能不能看图按 id 猜（defaults）；猜得不对再在模型上写。
type Config struct {
	Providers    []Provider `json:"providers"`
	DefaultModel string     `json:"defaultModel,omitempty"` // provider/model
	// SmallModel：起标题、压缩上下文这类杂活用的模型（provider/model），空 = 用会话自己的。
	SmallModel string `json:"smallModel,omitempty"`
}

type Provider struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	// API is the provider's protocol: "" = OpenAI-compatible (each model picks chat / responses), anthropic =
	// Anthropic Messages for every model (Model.API is ignored). Chosen when adding the provider: a gateway can't
	// be told apart by its URL.
	API     string `json:"api,omitempty"`
	BaseURL string `json:"baseUrl"`
	// APIKey is config.Hidden in config.json: the key itself is in secrets.json (WithKeys fills it in).
	APIKey string `json:"apiKey,omitempty"` //nolint:gosec
	// APIKeyEnv: read the key from this environment variable instead. Agents don't see it
	// (sysenv.SetSecretEnv drops it from their environment).
	APIKeyEnv string            `json:"apiKeyEnv,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
	Models    []Model           `json:"models"`
}

type Model struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	// API：chat（Chat Completions）| responses。空 = 按 id 猜：gpt / o 系列走 responses，别的走 chat。
	// 服务商是 anthropic 协议时不看这个，一律 anthropic。
	API string `json:"api,omitempty"`
	// Thinking：思考开关的格式。Chat Completions：deepseek = thinking:{type:enabled|disabled}，空 = reasoning_effort；
	// Anthropic: adaptive = thinking:{type:adaptive} with a reasoning summary; updates = the model's short progress
	// notes instead (Opus/Sonnet 5.5, Fable 5+, the default there). Guessed from the id; empty = no thinking param.
	Thinking string `json:"thinking,omitempty"`
	// Efforts：思考档位（原样发给接口的值），从低到高；DefaultEffort 是默认档。都空 = 按 id 猜。
	Efforts       []string `json:"efforts,omitempty"`
	DefaultEffort string   `json:"defaultEffort,omitempty"`
	Window        int      `json:"window,omitempty"` // 上下文窗口（token）
	// CompactAt：上下文到多少 token 时自动压缩。空 = min(窗口的 80%, 200k) —— 窗口 1M 的模型真用满了一次请求很贵，也慢。
	CompactAt int   `json:"compactAt,omitempty"`
	Images    *bool `json:"images,omitempty"` // 能不能看图
	// MaxOutput: max_tokens per request. 0 = don't send it (OpenAI protocols) / 32000 (Anthropic, where it is
	// required; Claude 4.6+ defaults to 64000).
	MaxOutput int `json:"maxOutput,omitempty"`
}

// Only：只留这几个服务商（按 id）。一个 agent 用 native 引擎、只走某家的模型（设置 → Agent 里配的 providers）；
// ids 为空 = 全部。默认模型 / 杂活模型不在留下的里面时，find 自己退回留下的第一个。
func (c Config) Only(ids []string) Config {
	if len(ids) == 0 {
		return c
	}
	keep := map[string]bool{}
	for _, id := range ids {
		keep[id] = true
	}
	out := c
	out.Providers = nil
	for _, p := range c.Providers {
		if keep[p.ID] {
			out.Providers = append(out.Providers, p)
		}
	}
	return out
}

// WithKeys swaps config.Hidden for the real keys from secrets.json.
func (c Config) WithKeys(sec *config.Secrets) Config {
	ps := make([]Provider, len(c.Providers))
	for i, p := range c.Providers {
		ps[i] = p.WithKey(sec)
	}
	c.Providers = ps
	return c
}

func (p Provider) WithKey(sec *config.Secrets) Provider {
	if p.APIKey == config.Hidden {
		p.APIKey = sec.Get(config.LLMKeyName(p.ID))
	}
	return p
}

// KeyEnvs lists the environment variables the providers take their keys from.
func (c Config) KeyEnvs() []string {
	var out []string
	for _, p := range c.Providers {
		if p.APIKeyEnv != "" {
			out = append(out, p.APIKeyEnv)
		}
	}
	return out
}

// resolved 是一个能直接用的模型：服务商的地址 + 补全了默认值的模型属性。
type resolved struct {
	Key      string // provider/model
	Provider Provider
	Model
}

func (r resolved) endpoint() llm.Endpoint {
	key := r.Provider.APIKey
	if key == "" && r.Provider.APIKeyEnv != "" {
		key = os.Getenv(r.Provider.APIKeyEnv)
	}
	return llm.Endpoint{BaseURL: r.Provider.BaseURL, APIKey: key, Headers: r.Provider.Headers}
}

func (r resolved) client() llm.Client { return llm.New(r.API, r.endpoint()) }

func (r resolved) seesImages() bool { return r.Images != nil && *r.Images }

func (r resolved) compactAt() int {
	if r.CompactAt > 0 {
		return r.CompactAt
	}
	return min(r.Window*8/10, 200_000)
}

// models 列出所有模型（补好默认值），按配置里的顺序。
func (c Config) models() []resolved {
	var out []resolved
	for _, p := range c.Providers {
		if p.ID == "" || p.BaseURL == "" {
			continue
		}
		for _, m := range p.Models {
			if m.ID == "" {
				continue
			}
			out = append(out, resolved{Key: p.ID + "/" + m.ID, Provider: p, Model: p.withDefaults(m)})
		}
	}
	return out
}

// find 按 provider/model 找；key 为空或找不到时退回默认模型、再退回第一个。
func (c Config) find(key string) (resolved, bool) {
	ms := c.models()
	if len(ms) == 0 {
		return resolved{}, false
	}
	for _, k := range []string{key, c.DefaultModel} {
		for _, m := range ms {
			if k != "" && m.Key == k {
				return m, true
			}
		}
	}
	return ms[0], true
}

// withDefaults is the model with this provider's protocol and the defaults guessed from its id.
func (p Provider) withDefaults(m Model) Model {
	if p.API == "anthropic" {
		m.API = "anthropic"
	}
	return withDefaults(m)
}

// adaptiveClaude: Claude models that think with thinking:{type:adaptive} + output_config.effort (4.6 and later;
// budget_tokens is a 400 on 4.7+). Same pattern shuttle uses.
var adaptiveClaude = regexp.MustCompile(`claude-(opus|sonnet|haiku|fable|mythos)-(4[.-][6-9]|[5-9]|\d{2})`)

// updatesClaude: the ones that take thinking.display "updates" — Opus / Sonnet 5.5 and Fable / Mythos 5+, and
// anything later. Haiku isn't on the list.
var updatesClaude = regexp.MustCompile(`claude-((opus|sonnet)-(5[.-][5-9]|[6-9]|\d{2})|(fable|mythos)-([5-9]|\d{2}))`)

// withDefaults：按模型 id 补协议、思考档位、窗口。数值来自 pi 的 models_catalog（shuttle 在用的那份）。
func withDefaults(m Model) Model {
	id := strings.ToLower(m.ID)
	if i := strings.LastIndex(id, "/"); i >= 0 {
		id = id[i+1:]
	}
	if m.API == "anthropic" {
		return anthropicDefaults(m, id)
	}
	gpt := strings.HasPrefix(id, "gpt-") || strings.HasPrefix(id, "o3") || strings.HasPrefix(id, "o4") || strings.HasPrefix(id, "codex")
	deepseek := strings.Contains(id, "deepseek")
	if m.API == "" {
		if gpt {
			m.API = "responses"
		} else {
			m.API = "chat"
		}
	}
	if m.Thinking == "" && deepseek {
		m.Thinking = "deepseek"
	}
	if len(m.Efforts) == 0 {
		switch {
		case gpt:
			m.Efforts = []string{"low", "medium", "high", "xhigh"}
			if m.DefaultEffort == "" {
				m.DefaultEffort = "medium"
			}
		case deepseek:
			// DeepSeek V4：思考只有 关 / 高 / 最高 三档（pi 的资料：low、medium 不认）
			m.Efforts = []string{"off", "high", "max"}
			if m.DefaultEffort == "" {
				m.DefaultEffort = "high"
			}
		}
	}
	if m.Window == 0 {
		switch {
		case gpt:
			m.Window = 272_000
		case deepseek:
			m.Window = 1_000_000
		default:
			m.Window = 128_000
		}
	}
	if m.Images == nil {
		v := !deepseek // DeepSeek 是纯文本模型，发图直接 400
		m.Images = &v
	}
	if m.Name == "" {
		m.Name = m.ID
	}
	return m
}

// anthropicDefaults: the same guesses for a model on the Anthropic protocol. Claude 4.6+ gets adaptive thinking and
// its effort levels (xhigh came with 4.7); anything else behind an Anthropic-compatible gateway gets no thinking
// parameters unless the model says so, because each gateway takes different ones.
func anthropicDefaults(m Model, id string) Model {
	if adaptiveClaude.MatchString(id) {
		if m.Thinking == "" {
			// "updates" where the model takes it, as Claude Code does: the reasoning stays hidden and what shows is
			// the model's own short notes. Those come from the "keep the user posted" line in basePrompt — without
			// it the model wrote none for many steps and nothing showed (2026-10-30).
			m.Thinking = "adaptive"
			if updatesClaude.MatchString(id) {
				m.Thinking = "updates"
			}
		}
		if len(m.Efforts) == 0 {
			if strings.Contains(id, "4-6") || strings.Contains(id, "4.6") {
				m.Efforts = []string{"low", "medium", "high", "max"}
			} else {
				m.Efforts = []string{"low", "medium", "high", "xhigh", "max"}
			}
		}
		if m.DefaultEffort == "" {
			m.DefaultEffort = "high" // sent explicitly: Opus 5.5 and Haiku 5.5 would otherwise default to medium
		}
		if m.Window == 0 {
			m.Window = 1_000_000
		}
		if m.MaxOutput == 0 {
			m.MaxOutput = 64_000
		}
	}
	if m.Window == 0 {
		switch {
		case strings.Contains(id, "deepseek"):
			m.Window = 1_000_000
		default:
			m.Window = 200_000
		}
	}
	if m.Images == nil {
		v := !strings.Contains(id, "deepseek")
		m.Images = &v
	}
	if m.Name == "" {
		m.Name = m.ID
	}
	return m
}
