package llm

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

// responses：OpenAI Responses（/responses）。GPT 的推理走它比 Chat Completions 好：思考能带回去（多轮工具调用之间
// 模型接得上自己的思路），还有推理摘要给人看。
//
//   - store=false（不在 OpenAI 那边存对话，我们自己管历史），所以推理要带 include:["reasoning.encrypted_content"]，
//     下一次请求原样放回 input 里（type:reasoning + encrypted_content）。
//   - input 必须是数组（网关对字符串报 "Input must be a list"，经网关实测）。
//   - 工具调用：output_item.added 给 call_id 和 name，function_call_arguments.delta 是参数片段，
//     output_item.done 给完整的。结果是 function_call_output{call_id, output}。
//   - 错误可能是 HTTP 200 之后流里的 response.failed / error 事件。
type responses struct{ ep Endpoint }

func (c *responses) Stream(ctx context.Context, req Request, on func(Event)) (Result, error) {
	body := map[string]any{
		"model":   req.Model,
		"stream":  true,
		"store":   false,
		"input":   responsesInput(req.Messages),
		"include": []string{"reasoning.encrypted_content"},
	}
	if req.System != "" {
		body["instructions"] = req.System
	}
	if len(req.Tools) > 0 {
		var tools []any
		for _, t := range req.Tools {
			tools = append(tools, map[string]any{"type": "function", "name": t.Name, "description": t.Description, "parameters": t.Parameters, "strict": false})
		}
		body["tools"] = tools
	}
	if req.Effort != "" {
		body["reasoning"] = map[string]any{"effort": req.Effort, "summary": "auto"}
	} else {
		body["reasoning"] = map[string]any{"summary": "auto"}
	}
	if req.MaxTokens > 0 {
		body["max_output_tokens"] = req.MaxTokens
	}
	if req.CacheKey != "" {
		body["prompt_cache_key"] = req.CacheKey
	}

	rc, err := post(ctx, c.ep, "/responses", body)
	if err != nil {
		return Result{Stop: "cancelled"}, err
	}
	defer rc.Close()

	var (
		text      strings.Builder
		thoughts  []Thought
		curThink  strings.Builder // 正在流的那段推理摘要
		calls     []ToolCall
		callIdx   = map[string]int{} // item id → calls 的下标
		usage     Usage
		status    string
		incReason string
	)
	err = readSSE(rc, func(d []byte) error {
		var ev struct {
			Type     string          `json:"type"`
			Delta    string          `json:"delta"`
			ItemID   string          `json:"item_id"`
			Item     json.RawMessage `json:"item"`
			Response *struct {
				Status            string `json:"status"`
				IncompleteDetails *struct {
					Reason string `json:"reason"`
				} `json:"incomplete_details"`
				Error *struct {
					Message string `json:"message"`
				} `json:"error"`
				Usage *struct {
					InputTokens        int `json:"input_tokens"`
					OutputTokens       int `json:"output_tokens"`
					InputTokensDetails *struct {
						CachedTokens int `json:"cached_tokens"`
					} `json:"input_tokens_details"`
					OutputTokensDetails *struct {
						ReasoningTokens int `json:"reasoning_tokens"`
					} `json:"output_tokens_details"`
				} `json:"usage"`
			} `json:"response"`
			Message string `json:"message"`
		}
		if json.Unmarshal(d, &ev) != nil {
			return nil
		}
		switch ev.Type {
		case "response.output_text.delta":
			text.WriteString(ev.Delta)
			on(Event{Kind: "text", Text: ev.Delta})
		case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
			curThink.WriteString(ev.Delta)
			on(Event{Kind: "thought", Text: ev.Delta})
		case "response.reasoning_summary_part.added":
			if curThink.Len() > 0 {
				curThink.WriteString("\n\n")
				on(Event{Kind: "thought", Text: "\n\n"})
			}
		case "response.output_item.added":
			var it struct {
				Type   string `json:"type"`
				ID     string `json:"id"`
				CallID string `json:"call_id"`
				Name   string `json:"name"`
			}
			_ = json.Unmarshal(ev.Item, &it)
			if it.Type == "function_call" {
				callIdx[it.ID] = len(calls)
				calls = append(calls, ToolCall{ID: it.CallID, Name: it.Name})
				on(Event{Kind: "tool", Index: len(calls) - 1, Name: it.Name})
			}
		case "response.function_call_arguments.delta":
			if i, ok := callIdx[ev.ItemID]; ok {
				calls[i].Args += ev.Delta
				on(Event{Kind: "tool_args", Index: i, Text: ev.Delta})
			}
		case "response.output_item.done":
			var it struct {
				Type             string `json:"type"`
				ID               string `json:"id"`
				CallID           string `json:"call_id"`
				Name             string `json:"name"`
				Arguments        string `json:"arguments"`
				EncryptedContent string `json:"encrypted_content"`
				Summary          []struct {
					Text string `json:"text"`
				} `json:"summary"`
			}
			_ = json.Unmarshal(ev.Item, &it)
			switch it.Type {
			case "reasoning":
				var parts []string
				for _, s := range it.Summary {
					parts = append(parts, s.Text)
				}
				t := strings.Join(parts, "\n\n")
				if t == "" {
					t = curThink.String()
				}
				thoughts = append(thoughts, Thought{Text: t, ID: it.ID, Encrypted: it.EncryptedContent})
				curThink.Reset()
			case "function_call":
				if i, ok := callIdx[it.ID]; ok {
					if it.Arguments != "" {
						calls[i].Args = it.Arguments
					}
					if it.CallID != "" {
						calls[i].ID = it.CallID
					}
				} else {
					calls = append(calls, ToolCall{ID: it.CallID, Name: it.Name, Args: it.Arguments})
				}
			}
		case "response.completed", "response.incomplete", "response.failed":
			if r := ev.Response; r != nil {
				status = r.Status
				if r.IncompleteDetails != nil {
					incReason = r.IncompleteDetails.Reason
				}
				if u := r.Usage; u != nil {
					usage = Usage{Input: u.InputTokens, Output: u.OutputTokens}
					if u.InputTokensDetails != nil {
						usage.CacheRead = u.InputTokensDetails.CachedTokens
					}
					if u.OutputTokensDetails != nil {
						usage.Reasoning = u.OutputTokensDetails.ReasoningTokens
					}
				}
				if ev.Type == "response.failed" {
					msg := "response failed"
					if r.Error != nil && r.Error.Message != "" {
						msg = r.Error.Message
					}
					return &APIError{Msg: msg}
				}
			}
		case "error":
			msg := ev.Message
			if msg == "" {
				msg = string(d)
			}
			return &APIError{Msg: msg}
		}
		return nil
	})
	if curThink.Len() > 0 { // 被打断时还没收到 output_item.done 的那段
		thoughts = append(thoughts, Thought{Text: curThink.String()})
	}
	msg := Message{Role: "assistant", Text: text.String(), Reasoning: thoughts, API: "responses"}
	for _, tc := range calls {
		if tc.Name != "" && tc.ID != "" {
			msg.ToolCalls = append(msg.ToolCalls, tc)
		}
	}
	res := Result{Message: msg, Usage: usage}
	if err = streamErr(ctx, err); err != nil {
		res.Stop = "cancelled"
		res.Message.ToolCalls = nil
		return res, err
	}
	switch {
	case status == "":
		return res, errors.New("stream disconnected: no response.completed")
	case len(msg.ToolCalls) > 0:
		res.Stop = "tool_use"
	case status == "incomplete" && incReason == "max_output_tokens":
		res.Stop = "length"
	default:
		res.Stop = "end"
	}
	return res, nil
}

func responsesInput(msgs []Message) []any {
	var out []any
	var pendingImages []Part
	flushImages := func() {
		if len(pendingImages) == 0 {
			return
		}
		parts := []any{map[string]any{"type": "input_text", "text": "(Images returned by the tool calls above.)"}}
		for _, p := range pendingImages {
			parts = append(parts, map[string]any{"type": "input_image", "image_url": dataURL(p)})
		}
		out = append(out, map[string]any{"role": "user", "content": parts})
		pendingImages = nil
	}
	for _, m := range msgs {
		if m.Role != "tool" {
			flushImages()
		}
		switch m.Role {
		case "user":
			var parts []any
			for _, p := range m.Content {
				switch p.Type {
				case "text":
					parts = append(parts, map[string]any{"type": "input_text", "text": p.Text})
				case "image":
					parts = append(parts, map[string]any{"type": "input_image", "image_url": dataURL(p)})
				}
			}
			if len(parts) == 0 {
				parts = append(parts, map[string]any{"type": "input_text", "text": ""})
			}
			out = append(out, map[string]any{"role": "user", "content": parts})
		case "assistant":
			if m.API == "responses" {
				for _, t := range m.Reasoning {
					if t.Encrypted == "" {
						continue
					}
					summary := []any{}
					if t.Text != "" {
						summary = append(summary, map[string]any{"type": "summary_text", "text": t.Text})
					}
					it := map[string]any{"type": "reasoning", "encrypted_content": t.Encrypted, "summary": summary}
					if t.ID != "" {
						it["id"] = t.ID
					}
					out = append(out, it)
				}
			}
			if m.Text != "" {
				out = append(out, map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": m.Text}}})
			}
			for _, tc := range m.ToolCalls {
				args := tc.Args
				if !json.Valid([]byte(args)) {
					args = "{}"
				}
				out = append(out, map[string]any{"type": "function_call", "call_id": tc.ID, "name": tc.Name, "arguments": args})
			}
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
			out = append(out, map[string]any{"type": "function_call_output", "call_id": m.CallID, "output": strings.Join(text, "\n")})
		}
	}
	flushImages()
	return out
}
