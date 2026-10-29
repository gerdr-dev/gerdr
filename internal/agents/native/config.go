package native

import (
	"os"
	"strings"

	"github.com/zbysir/gerdr/internal/agents/native/llm"
)

// Config 是 config.json 里的 "llm"：模型服务商和模型。设置一改下一轮就用新的（Agent 每次现取）。
//
//	"llm": {
//	  "providers": [{
//	    "id": "subhub", "name": "SubHub", "baseUrl": "https://example.com/v1", "apiKey": "sk-…",
//	    "models": [{"id": "gpt-6.1-sol"}, {"id": "deepseek-v4-pro"}]
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
	ID      string `json:"id"`
	Name    string `json:"name,omitempty"`
	BaseURL string `json:"baseUrl"`
	APIKey  string `json:"apiKey,omitempty"` //nolint:gosec
	// APIKeyEnv：key 从这个环境变量读（不想把 key 写进 config.json 时用）。
	APIKeyEnv string            `json:"apiKeyEnv,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
	Models    []Model           `json:"models"`
}

type Model struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	// API：chat（Chat Completions）| responses。空 = 按 id 猜：gpt / o 系列走 responses，别的走 chat。
	API string `json:"api,omitempty"`
	// Thinking：Chat Completions 上思考开关的格式。deepseek = thinking:{type:enabled|disabled}；空 = reasoning_effort。
	Thinking string `json:"thinking,omitempty"`
	// Efforts：思考档位（原样发给接口的值），从低到高；DefaultEffort 是默认档。都空 = 按 id 猜。
	Efforts       []string `json:"efforts,omitempty"`
	DefaultEffort string   `json:"defaultEffort,omitempty"`
	Window        int      `json:"window,omitempty"` // 上下文窗口（token）
	// CompactAt：上下文到多少 token 时自动压缩。空 = min(窗口的 80%, 200k) —— 窗口 1M 的模型真用满了一次请求很贵，也慢。
	CompactAt int   `json:"compactAt,omitempty"`
	Images    *bool `json:"images,omitempty"` // 能不能看图
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
			out = append(out, resolved{Key: p.ID + "/" + m.ID, Provider: p, Model: withDefaults(m)})
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

// withDefaults：按模型 id 补协议、思考档位、窗口。数值来自 pi 的 models_catalog（shuttle 在用的那份）。
func withDefaults(m Model) Model {
	id := strings.ToLower(m.ID)
	if i := strings.LastIndex(id, "/"); i >= 0 {
		id = id[i+1:]
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
