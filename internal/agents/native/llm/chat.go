package llm

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

// chat：OpenAI Chat Completions（/chat/completions）。DeepSeek、各家网关都认这个。
//
// 实测 / 抄 pi 来的怪癖：
//   - 工具调用的流式片段按 index 拼；GPT 经网关时第一片之后每片都带 `"name":""`，不能拿空名字覆盖第一片的（实测 subhub）。
//   - DeepSeek 的思考在 delta.reasoning_content（有的实现叫 reasoning / reasoning_text）。开着思考时，
//     **历史里每条 assistant 都要带 reasoning_content**（没有就给空串），否则 400；带工具调用的那条要把原来的思考带回去，
//     模型才接得上自己的思路（DeepSeek 的文档：同一轮工具调用之间要回传）。
//   - 工具结果的消息里放不了图：图片另起一条 user 消息跟在这批工具结果后面。
//   - usage 只在最后一片（要 stream_options.include_usage）；缓存命中 OpenAI 报 prompt_tokens_details.cached_tokens，
//     DeepSeek 报 prompt_cache_hit_tokens。
type chat struct{ ep Endpoint }

func (c *chat) Stream(ctx context.Context, req Request, on func(Event)) (Result, error) {
	body := map[string]any{
		"model":          req.Model,
		"stream":         true,
		"stream_options": map[string]any{"include_usage": true},
		"messages":       chatMessages(req),
	}
	if len(req.Tools) > 0 {
		var tools []any
		for _, t := range req.Tools {
			tools = append(tools, map[string]any{"type": "function", "function": map[string]any{
				"name": t.Name, "description": t.Description, "parameters": t.Parameters,
			}})
		}
		body["tools"] = tools
	}
	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	}
	if req.CacheKey != "" && req.Thinking != "deepseek" {
		body["prompt_cache_key"] = req.CacheKey
	}
	switch req.Thinking {
	case "deepseek":
		if req.Effort == "off" {
			body["thinking"] = map[string]any{"type": "disabled"}
		} else {
			body["thinking"] = map[string]any{"type": "enabled"}
			if req.Effort != "" {
				body["reasoning_effort"] = req.Effort
			}
		}
	default:
		if req.Effort != "" {
			body["reasoning_effort"] = req.Effort
		}
	}

	rc, err := post(ctx, c.ep, "/chat/completions", body)
	if err != nil {
		return Result{Stop: "cancelled"}, err
	}
	defer rc.Close()

	var (
		text, thought strings.Builder
		calls         []ToolCall
		usage         Usage
		finish        string
	)
	err = readSSE(rc, func(d []byte) error {
		var ch struct {
			Choices []struct {
				Delta struct {
					Content          *string `json:"content"`
					ReasoningContent *string `json:"reasoning_content"`
					Reasoning        *string `json:"reasoning"`
					ReasoningText    *string `json:"reasoning_text"`
					ToolCalls        []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens        int `json:"prompt_tokens"`
				CompletionTokens    int `json:"completion_tokens"`
				PromptCacheHit      int `json:"prompt_cache_hit_tokens"`
				PromptTokensDetails *struct {
					CachedTokens int `json:"cached_tokens"`
				} `json:"prompt_tokens_details"`
				CompletionTokensDetails *struct {
					ReasoningTokens int `json:"reasoning_tokens"`
				} `json:"completion_tokens_details"`
			} `json:"usage"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(d, &ch); err != nil {
			return nil // 认不得的片段跳过
		}
		if ch.Error != nil {
			return &APIError{Msg: ch.Error.Message}
		}
		if u := ch.Usage; u != nil {
			usage = Usage{Input: u.PromptTokens, Output: u.CompletionTokens, CacheRead: u.PromptCacheHit}
			if u.PromptTokensDetails != nil && u.PromptTokensDetails.CachedTokens > 0 {
				usage.CacheRead = u.PromptTokensDetails.CachedTokens
			}
			if u.CompletionTokensDetails != nil {
				usage.Reasoning = u.CompletionTokensDetails.ReasoningTokens
			}
		}
		for _, cc := range ch.Choices {
			dl := cc.Delta
			for _, r := range []*string{dl.ReasoningContent, dl.Reasoning, dl.ReasoningText} {
				if r != nil && *r != "" {
					thought.WriteString(*r)
					on(Event{Kind: "thought", Text: *r})
					break
				}
			}
			if dl.Content != nil && *dl.Content != "" {
				text.WriteString(*dl.Content)
				on(Event{Kind: "text", Text: *dl.Content})
			}
			for _, tc := range dl.ToolCalls {
				for len(calls) <= tc.Index {
					calls = append(calls, ToolCall{})
				}
				cur := &calls[tc.Index]
				if tc.ID != "" && cur.ID == "" {
					cur.ID = tc.ID
				}
				if tc.Function.Name != "" && cur.Name == "" {
					cur.Name = tc.Function.Name
					on(Event{Kind: "tool", Index: tc.Index, Name: cur.Name})
				}
				if tc.Function.Arguments != "" {
					cur.Args += tc.Function.Arguments
					on(Event{Kind: "tool_args", Index: tc.Index, Text: tc.Function.Arguments})
				}
			}
			if cc.FinishReason != nil && *cc.FinishReason != "" {
				finish = *cc.FinishReason
			}
		}
		return nil
	})
	msg := Message{Role: "assistant", Text: text.String(), API: "chat"}
	if thought.Len() > 0 {
		msg.Reasoning = []Thought{{Text: thought.String()}}
	}
	for i, tc := range calls {
		if tc.Name == "" {
			continue // 只来了半截的调用
		}
		if tc.ID == "" {
			tc.ID = "call_" + itoa(i)
		}
		msg.ToolCalls = append(msg.ToolCalls, tc)
	}
	res := Result{Message: msg, Usage: usage}
	if err = streamErr(ctx, err); err != nil {
		res.Stop = "cancelled"
		res.Message.ToolCalls = nil // 半截的工具调用不能执行
		return res, err
	}
	switch {
	case len(msg.ToolCalls) > 0:
		res.Stop = "tool_use"
	case finish == "length":
		res.Stop = "length"
	case finish == "":
		// 没有 finish_reason 就结束了：连接被网关提前掐了
		return res, errors.New("stream disconnected: no finish_reason")
	default:
		res.Stop = "end"
	}
	return res, nil
}

func chatMessages(req Request) []any {
	var out []any
	if req.System != "" {
		out = append(out, map[string]any{"role": "system", "content": req.System})
	}
	deepseek := req.Thinking == "deepseek"
	var pendingImages []Part // 这批工具结果里的图，等工具结果都放完再作为一条 user 消息补上
	flushImages := func() {
		if len(pendingImages) == 0 {
			return
		}
		parts := []any{map[string]any{"type": "text", "text": "(Images returned by the tool calls above.)"}}
		for _, p := range pendingImages {
			parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]any{"url": dataURL(p)}})
		}
		out = append(out, map[string]any{"role": "user", "content": parts})
		pendingImages = nil
	}
	for _, m := range req.Messages {
		if m.Role != "tool" {
			flushImages()
		}
		switch m.Role {
		case "user":
			out = append(out, map[string]any{"role": "user", "content": chatContent(m.Content)})
		case "assistant":
			am := map[string]any{"role": "assistant"}
			if m.Text != "" {
				am["content"] = m.Text
			} else {
				am["content"] = nil
			}
			if len(m.ToolCalls) > 0 {
				var tcs []any
				for _, tc := range m.ToolCalls {
					args := tc.Args
					if !json.Valid([]byte(args)) {
						args = "{}"
					}
					tcs = append(tcs, map[string]any{"id": tc.ID, "type": "function", "function": map[string]any{"name": tc.Name, "arguments": args}})
				}
				am["tool_calls"] = tcs
			}
			if deepseek {
				rc := ""
				if m.API == "chat" {
					for _, t := range m.Reasoning {
						rc += t.Text
					}
				}
				am["reasoning_content"] = rc
			}
			out = append(out, am)
		case "tool":
			var text []string
			for _, p := range m.Content {
				switch p.Type {
				case "text":
					text = append(text, p.Text)
				case "image":
					pendingImages = append(pendingImages, p)
				}
			}
			out = append(out, map[string]any{"role": "tool", "tool_call_id": m.CallID, "content": strings.Join(text, "\n")})
		}
	}
	flushImages()
	return out
}

// chatContent：只有一段文字时发字符串（有的网关不认数组形式的纯文字）。
func chatContent(parts []Part) any {
	if len(parts) == 1 && parts[0].Type == "text" {
		return parts[0].Text
	}
	var out []any
	for _, p := range parts {
		switch p.Type {
		case "text":
			out = append(out, map[string]any{"type": "text", "text": p.Text})
		case "image":
			out = append(out, map[string]any{"type": "image_url", "image_url": map[string]any{"url": dataURL(p)}})
		}
	}
	if len(out) == 0 {
		return ""
	}
	return out
}

func itoa(i int) string {
	return strings.TrimSpace(string(mustJSON(i)))
}
