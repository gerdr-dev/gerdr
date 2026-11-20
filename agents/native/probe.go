package native

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/gerdr-dev/gerdr/agents/native/llm"
)

// 设置页用的两个探测：拉服务商的模型列表（顺便验证地址和 key 对不对）、拿一个模型真说一句话（验证协议猜对了没有）。

// ModelInfo is one model from a provider's model list: its id, and what the list says about it beyond that. Plain
// OpenAI / Gemini / Anthropic lists have only the id; a gateway may add (subhub does, 2026-11):
//
//	"reasoning": {"efforts": ["low", "medium", "high", "xhigh"], "default": "medium"}
//	"architecture": {"output_modalities": ["text"]}   // or ["image"], ["text", "image"]
//
// Efforts are the values to send as they are, low to high; Default the one used when none is sent.
type ModelInfo struct {
	ID            string
	Efforts       []string
	DefaultEffort string
	Output        []string // output modalities; empty = not said
}

// listModel is the fields both list shapes share per model.
type listModel struct {
	Reasoning *struct {
		Efforts []string `json:"efforts"`
		Default string   `json:"default"`
	} `json:"reasoning"`
	Architecture *struct {
		Output []string `json:"output_modalities"`
	} `json:"architecture"`
}

func (l listModel) info(id string) ModelInfo {
	mi := ModelInfo{ID: id}
	if l.Reasoning != nil {
		mi.Efforts, mi.DefaultEffort = l.Reasoning.Efforts, l.Reasoning.Default
	}
	if l.Architecture != nil {
		mi.Output = l.Architecture.Output
	}
	return mi
}

// ListModels: the provider's model ids in alphabetical order (the settings page's "fetch").
func ListModels(ctx context.Context, p Provider) ([]string, error) {
	ms, err := ListModelInfo(ctx, p)
	var out []string
	for _, m := range ms {
		out = append(out, m.ID)
	}
	return out, err
}

// ListModelInfo: GET <baseUrl>/models (every OpenAI-compatible API has it; Anthropic's is <base>/v1/models with other
// auth headers and the same shape; Gemini's is listGeminiModels), sorted by id.
func ListModelInfo(ctx context.Context, p Provider) ([]ModelInfo, error) {
	ep := resolved{Provider: p}.endpoint()
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if p.API == "gemini" {
		return listGeminiModels(ctx, ep)
	}
	url := strings.TrimRight(ep.BaseURL, "/") + "/models"
	if p.API == "anthropic" {
		url = llm.AnthropicURL(ep.BaseURL, "/models?limit=1000")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	if p.API == "anthropic" {
		req.Header = llm.AnthropicAuth(ep.APIKey)
	} else if ep.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+ep.APIKey)
	}
	for k, v := range ep.Headers {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d: %s", res.StatusCode, oneLine(string(b), 300))
	}
	var r struct {
		Data []struct {
			ID string `json:"id"`
			listModel
		} `json:"data"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("unexpected /models response: %s", oneLine(string(b), 200))
	}
	var out []ModelInfo
	for _, m := range r.Data {
		if m.ID != "" {
			out = append(out, m.info(m.ID))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// listGeminiModels: Gemini's models.list, paged ({models: [{name: "models/…", supportedGenerationMethods}],
// nextPageToken}); only the ones that can generateContent (not embeddings…), without the "models/" prefix.
func listGeminiModels(ctx context.Context, ep llm.Endpoint) ([]ModelInfo, error) {
	var out []ModelInfo
	token := ""
	for page := 0; page < 20; page++ {
		u := llm.GeminiURL(ep.BaseURL, "/models?pageSize=1000")
		if token != "" {
			u += "&pageToken=" + url.QueryEscape(token)
		}
		req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
		if err != nil {
			return nil, err
		}
		req.Header = llm.GeminiAuth(ep.APIKey)
		for k, v := range ep.Headers {
			req.Header.Set(k, v)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}
		b, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
		res.Body.Close()
		if res.StatusCode != 200 {
			return nil, fmt.Errorf("HTTP %d: %s", res.StatusCode, oneLine(string(b), 300))
		}
		var r struct {
			Models []struct {
				Name    string   `json:"name"`
				Methods []string `json:"supportedGenerationMethods"`
				listModel
			} `json:"models"`
			Next string `json:"nextPageToken"`
		}
		if err := json.Unmarshal(b, &r); err != nil {
			return nil, fmt.Errorf("unexpected /models response: %s", oneLine(string(b), 200))
		}
		for _, m := range r.Models {
			if m.Name != "" && (len(m.Methods) == 0 || slices.Contains(m.Methods, "generateContent")) {
				out = append(out, m.info(llm.GeminiModel(m.Name)))
			}
		}
		if token = r.Next; token == "" {
			break
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// TestModel：用这个模型说一句话，回它的回复和用的协议。
func TestModel(ctx context.Context, p Provider, m Model) (reply, api string, err error) {
	r := resolved{Key: p.ID + "/" + m.ID, Provider: p, Model: p.withDefaults(m)}
	if r.Kind == "image" {
		// An image model can't "reply OK": make one small image (costs one generation, on a button the person pressed)
		cfg := Config{Providers: []Provider{p}}
		cfg.Providers[0].Models = []Model{m}
		res, _, err := GenerateImage(ctx, cfg, r.Key, llm.ImageRequest{Prompt: "A single red circle on a plain white background, flat, minimal."})
		api = "images"
		if p.API == "gemini" {
			api = "gemini"
		}
		if err != nil {
			return "", api, err
		}
		return fmt.Sprintf("%s, %d KB", res.Images[0].MIME, (len(res.Images[0].Data)+1023)/1024), api, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	res, err := r.client().Stream(ctx, llm.Request{
		// The default effort, as a session would send: the lowest level was a guess the model may refuse (Gemini 4
		// answered MINIMAL with a 400), and then the test failed though the model worked.
		Model: r.ID, Thinking: r.Thinking, Effort: effortFor(r, ""),
		Messages: []llm.Message{{Role: "user", Content: []llm.Part{llm.TextPart("Reply with exactly: OK")}}},
	}, func(llm.Event) {})
	if err != nil {
		return "", r.API, err
	}
	return res.Message.Text, r.API, nil
}
