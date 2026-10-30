package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// anthropic: Anthropic Messages (/v1/messages). Claude directly, and gateways that speak Anthropic's protocol.
//
//   - Auth is x-api-key + anthropic-version, not Bearer. The base URL may or may not end in /v1 (Anthropic's own
//     docs give the bare host, OpenAI-style settings say "up to /v1"); AnthropicURL accepts both.
//   - Assistant turns are replayed as the exact blocks the API returned (Message.Blocks): thinking blocks carry a
//     signature and newer models check that earlier turns weren't edited, so rebuilding them from Text / Reasoning /
//     ToolCalls (which loses block order) can cost the thinking or a 400. A turn from another protocol is rebuilt
//     without its thinking.
//   - All tool results of one assistant turn go into one user message (splitting them is legal but teaches the model
//     to stop calling tools in parallel). Images go inside tool_result, unlike the OpenAI protocols.
//   - Claude 4.6+ thinks with thinking:{type:adaptive} + output_config.effort; display defaults to "omitted" on newer
//     models (thinking blocks with empty text), so ask for "summarized" or the UI shows nothing while it thinks.
//     budget_tokens is a 400 on Claude 4.7+ and is never sent.
//   - Prompt caching is opt-in: cache_control on the system prompt, the last tool and the last block of the last
//     message (3 of the 4 allowed breakpoints). usage.input_tokens excludes cache reads and writes; Usage.Input adds
//     them back so it means the same thing as on the other protocols.
//   - max_tokens is required.
type anthropic struct{ ep Endpoint }

const anthropicVersion = "2023-06-01"

// defaultMaxTokens: what we ask for when the model doesn't say (Model.MaxOutput). Every Claude since 4.5 takes at
// least 64k; a gateway with a lower cap answers 400 and the model's maxOutput fixes it.
const defaultMaxTokens = 32000

// AnthropicURL joins the base URL and an Anthropic path (/messages, /models), adding /v1 if the base lacks it.
func AnthropicURL(base, path string) string {
	base = strings.TrimRight(base, "/")
	if !strings.HasSuffix(base, "/v1") {
		base += "/v1"
	}
	return base + path
}

// AnthropicAuth is the auth headers for a key.
func AnthropicAuth(key string) http.Header {
	h := http.Header{}
	h.Set("anthropic-version", anthropicVersion)
	if key != "" {
		h.Set("x-api-key", key)
	}
	return h
}

func (c *anthropic) Stream(ctx context.Context, req Request, on func(Event)) (Result, error) {
	cc := map[string]any{"type": "ephemeral"}
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultMaxTokens
	}
	msgs := anthropicMessages(req.Messages)
	markLastForCache(msgs, cc)
	body := map[string]any{
		"model":      req.Model,
		"stream":     true,
		"max_tokens": maxTokens,
		"messages":   msgs,
	}
	if req.System != "" {
		body["system"] = []any{map[string]any{"type": "text", "text": req.System, "cache_control": cc}}
	}
	if len(req.Tools) > 0 {
		var tools []any
		for i, t := range req.Tools {
			tool := map[string]any{"name": t.Name, "description": t.Description, "input_schema": t.Parameters}
			if i == len(req.Tools)-1 {
				tool["cache_control"] = cc
			}
			tools = append(tools, tool)
		}
		body["tools"] = tools
	}
	thinkingOff := req.Effort == "off"
	if req.Thinking == "adaptive" && !thinkingOff {
		body["thinking"] = map[string]any{"type": "adaptive", "display": "summarized"}
	}
	if req.Effort != "" && !thinkingOff {
		body["output_config"] = map[string]any{"effort": req.Effort}
	}

	rc, err := postURL(ctx, AnthropicURL(c.ep.BaseURL, "/messages"), AnthropicAuth(c.ep.APIKey), c.ep.Headers, body)
	if err != nil {
		return Result{Stop: "cancelled"}, err
	}
	defer rc.Close()

	type block struct {
		typ                   string
		text, thinking, sig   string
		data                  string // redacted_thinking
		id, name              string
		input                 strings.Builder
		call                  int             // index into calls (tool_use)
		raw                   json.RawMessage // a block type we don't know: kept as it started
		thinkingSeen, started bool
	}
	var (
		blocks   []*block
		calls    []ToolCall
		usage    anthropicUsage
		stop     string
		thoughts int // thinking blocks seen so far: a blank line between them in the UI
	)
	at := func(i int) *block {
		for len(blocks) <= i {
			blocks = append(blocks, &block{})
		}
		return blocks[i]
	}
	err = readSSE(rc, func(d []byte) error {
		var ev struct {
			Type    string `json:"type"`
			Index   int    `json:"index"`
			Message *struct {
				Usage anthropicUsage `json:"usage"`
			} `json:"message"`
			ContentBlock json.RawMessage `json:"content_block"`
			Delta        *struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				Thinking    string `json:"thinking"`
				Signature   string `json:"signature"`
				PartialJSON string `json:"partial_json"`
				StopReason  string `json:"stop_reason"`
			} `json:"delta"`
			Usage *anthropicUsage `json:"usage"`
			Error *struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(d, &ev) != nil {
			return nil
		}
		switch ev.Type {
		case "message_start":
			if ev.Message != nil {
				usage.merge(ev.Message.Usage)
			}
		case "content_block_start":
			var cb struct {
				Type      string `json:"type"`
				Text      string `json:"text"`
				Thinking  string `json:"thinking"`
				Signature string `json:"signature"`
				Data      string `json:"data"`
				ID        string `json:"id"`
				Name      string `json:"name"`
			}
			_ = json.Unmarshal(ev.ContentBlock, &cb)
			b := at(ev.Index)
			b.typ, b.started = cb.Type, true
			switch cb.Type {
			case "text":
				b.text = cb.Text
				if cb.Text != "" {
					on(Event{Kind: "text", Text: cb.Text})
				}
			case "thinking":
				if thoughts > 0 {
					on(Event{Kind: "thought", Text: "\n\n"})
				}
				thoughts++
				b.thinking, b.sig = cb.Thinking, cb.Signature
				if cb.Thinking != "" {
					on(Event{Kind: "thought", Text: cb.Thinking})
				}
			case "redacted_thinking":
				b.data = cb.Data
			case "tool_use":
				b.id, b.name, b.call = cb.ID, cb.Name, len(calls)
				calls = append(calls, ToolCall{ID: cb.ID, Name: cb.Name})
				on(Event{Kind: "tool", Index: b.call, Name: cb.Name})
			default:
				b.raw = append(json.RawMessage(nil), ev.ContentBlock...)
			}
		case "content_block_delta":
			if ev.Delta == nil {
				return nil
			}
			b := at(ev.Index)
			switch ev.Delta.Type {
			case "text_delta":
				b.text += ev.Delta.Text
				on(Event{Kind: "text", Text: ev.Delta.Text})
			case "thinking_delta":
				b.thinking += ev.Delta.Thinking
				on(Event{Kind: "thought", Text: ev.Delta.Thinking})
			case "signature_delta":
				b.sig += ev.Delta.Signature
			case "input_json_delta":
				if b.typ == "tool_use" {
					b.input.WriteString(ev.Delta.PartialJSON)
					on(Event{Kind: "tool_args", Index: b.call, Text: ev.Delta.PartialJSON})
				}
			}
		case "message_delta":
			if ev.Delta != nil && ev.Delta.StopReason != "" {
				stop = ev.Delta.StopReason
			}
			if ev.Usage != nil {
				usage.merge(*ev.Usage)
			}
		case "error":
			if ev.Error == nil {
				return &APIError{Msg: string(d)}
			}
			// overloaded / api_error mid-stream are transient: worded so the hub retries the turn
			if ev.Error.Type == "overloaded_error" || ev.Error.Type == "api_error" {
				return fmt.Errorf("stream disconnected: %s: %s", ev.Error.Type, ev.Error.Message)
			}
			return &APIError{Msg: ev.Error.Type + ": " + ev.Error.Message}
		}
		return nil
	})

	msg := Message{Role: "assistant", API: "anthropic"}
	var texts []string
	var replay []any
	for _, b := range blocks {
		if !b.started {
			continue
		}
		switch b.typ {
		case "text":
			if strings.TrimSpace(b.text) == "" {
				continue // the API rejects empty text blocks in history
			}
			texts = append(texts, b.text)
			replay = append(replay, map[string]any{"type": "text", "text": b.text})
		case "thinking":
			msg.Reasoning = append(msg.Reasoning, Thought{Text: b.thinking, Encrypted: b.sig})
			if b.sig != "" {
				replay = append(replay, map[string]any{"type": "thinking", "thinking": b.thinking, "signature": b.sig})
			}
		case "redacted_thinking":
			replay = append(replay, map[string]any{"type": "redacted_thinking", "data": b.data})
		case "tool_use":
			args := b.input.String()
			if args == "" {
				args = "{}"
			}
			calls[b.call].Args = args
			replay = append(replay, map[string]any{"type": "tool_use", "id": b.id, "name": b.name, "input": jsonOrEmpty(args)})
		default:
			if len(b.raw) > 0 {
				replay = append(replay, b.raw)
			}
		}
	}
	msg.Text = strings.Join(texts, "\n\n")
	for _, tc := range calls {
		if tc.ID != "" && tc.Name != "" {
			msg.ToolCalls = append(msg.ToolCalls, tc)
		}
	}
	res := Result{Message: msg, Usage: usage.toUsage()}
	if err = streamErr(ctx, err); err != nil {
		res.Stop = "cancelled"
		res.Message.ToolCalls = nil
		return res, err
	}
	if stop == "" {
		return res, errors.New("stream disconnected: no stop_reason")
	}
	if len(replay) > 0 {
		res.Message.Blocks = mustJSON(replay)
	}
	switch {
	case stop == "refusal":
		return res, &APIError{Msg: "the model declined to continue (stop_reason: refusal)"}
	case len(res.Message.ToolCalls) > 0:
		res.Stop = "tool_use"
	case stop == "max_tokens":
		res.Stop = "length"
	default:
		res.Stop = "end"
	}
	return res, nil
}

type anthropicUsage struct {
	InputTokens         int `json:"input_tokens"`
	OutputTokens        int `json:"output_tokens"`
	CacheReadTokens     int `json:"cache_read_input_tokens"`
	CacheCreationTokens int `json:"cache_creation_input_tokens"`
}

// merge: message_delta's numbers are cumulative (overwrite, don't add) and any field may be missing, so each
// non-zero field replaces the earlier one on its own (same rule as weave's Anthropic gateway).
func (u *anthropicUsage) merge(o anthropicUsage) {
	for _, f := range []struct{ dst, src *int }{
		{&u.InputTokens, &o.InputTokens}, {&u.OutputTokens, &o.OutputTokens},
		{&u.CacheReadTokens, &o.CacheReadTokens}, {&u.CacheCreationTokens, &o.CacheCreationTokens},
	} {
		if *f.src > 0 {
			*f.dst = *f.src
		}
	}
}

func (u anthropicUsage) toUsage() Usage {
	return Usage{Input: u.InputTokens + u.CacheReadTokens + u.CacheCreationTokens, Output: u.OutputTokens, CacheRead: u.CacheReadTokens}
}

func anthropicMessages(msgs []Message) []map[string]any {
	out := []map[string]any{}
	// user appends to the previous message when it is a user one too: a turn's tool results (and an interjection
	// right after them) go in one message.
	user := func(blocks []any) {
		if len(blocks) == 0 {
			return
		}
		if n := len(out); n > 0 && out[n-1]["role"] == "user" {
			out[n-1]["content"] = append(out[n-1]["content"].([]any), blocks...)
			return
		}
		out = append(out, map[string]any{"role": "user", "content": blocks})
	}
	for _, m := range msgs {
		switch m.Role {
		case "user":
			user(anthropicParts(m.Content))
		case "assistant":
			if m.API == "anthropic" && len(m.Blocks) > 0 {
				out = append(out, map[string]any{"role": "assistant", "content": m.Blocks})
				continue
			}
			var blocks []any
			if m.API == "anthropic" {
				for _, t := range m.Reasoning {
					if t.Encrypted != "" {
						blocks = append(blocks, map[string]any{"type": "thinking", "thinking": t.Text, "signature": t.Encrypted})
					}
				}
			}
			if strings.TrimSpace(m.Text) != "" {
				blocks = append(blocks, map[string]any{"type": "text", "text": m.Text})
			}
			for _, tc := range m.ToolCalls {
				blocks = append(blocks, map[string]any{"type": "tool_use", "id": tc.ID, "name": tc.Name, "input": jsonOrEmpty(tc.Args)})
			}
			if len(blocks) > 0 {
				out = append(out, map[string]any{"role": "assistant", "content": blocks})
			}
		case "tool":
			tr := map[string]any{"type": "tool_result", "tool_use_id": m.CallID}
			if content := anthropicParts(m.Content); len(content) > 0 {
				tr["content"] = content
			}
			if m.IsError {
				tr["is_error"] = true
			}
			user([]any{tr})
		}
	}
	return out
}

func anthropicParts(parts []Part) []any {
	var out []any
	for _, p := range parts {
		switch p.Type {
		case "text":
			if strings.TrimSpace(p.Text) != "" {
				out = append(out, map[string]any{"type": "text", "text": p.Text})
			}
		case "image":
			mime := p.MIME
			if mime == "" {
				mime = "image/png"
			}
			out = append(out, map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": mime, "data": p.Data}})
		}
	}
	return out
}

// markLastForCache puts a cache breakpoint on the last block of the last message, so the next request (this
// history plus a little more) reads all of it from cache.
func markLastForCache(msgs []map[string]any, cc map[string]any) {
	if len(msgs) == 0 || msgs[len(msgs)-1]["role"] != "user" {
		return
	}
	blocks, _ := msgs[len(msgs)-1]["content"].([]any)
	if len(blocks) == 0 {
		return
	}
	if b, ok := blocks[len(blocks)-1].(map[string]any); ok {
		b["cache_control"] = cc
	}
}

func jsonOrEmpty(s string) json.RawMessage {
	if s == "" || !json.Valid([]byte(s)) {
		return json.RawMessage("{}")
	}
	return json.RawMessage(s)
}
