package native

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gerdr-dev/gerdr/agents/native/llm"
)

// 设置页用的两个探测：拉服务商的模型列表（顺便验证地址和 key 对不对）、拿一个模型真说一句话（验证协议猜对了没有）。

// ListModels：GET <baseUrl>/models（OpenAI 兼容的都有；Anthropic 是 <base>/v1/models，鉴权头不一样，回的形状一样），
// 回模型 id，按字母排。
func ListModels(ctx context.Context, p Provider) ([]string, error) {
	ep := resolved{Provider: p}.endpoint()
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
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
		} `json:"data"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("unexpected /models response: %s", oneLine(string(b), 200))
	}
	var out []string
	for _, m := range r.Data {
		if m.ID != "" {
			out = append(out, m.ID)
		}
	}
	sort.Strings(out)
	return out, nil
}

// TestModel：用这个模型说一句话，回它的回复和用的协议。
func TestModel(ctx context.Context, p Provider, m Model) (reply, api string, err error) {
	r := resolved{Key: p.ID + "/" + m.ID, Provider: p, Model: p.withDefaults(m)}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	res, err := r.client().Stream(ctx, llm.Request{
		Model: r.ID, Thinking: r.Thinking, Effort: lowestEffort(r),
		Messages: []llm.Message{{Role: "user", Content: []llm.Part{llm.TextPart("Reply with exactly: OK")}}},
	}, func(llm.Event) {})
	if err != nil {
		return "", r.API, err
	}
	return res.Message.Text, r.API, nil
}
