package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// gemini: Google's native Gemini API (generativelanguage.googleapis.com, /v1beta/models/<model>:streamGenerateContent).
// Its OpenAI-compatible endpoint drops the thought signatures, which Gemini 3+ wants back on the function calls of
// the current turn, so Gemini gets its own protocol. Ported from pi (@mariozechner/pi-ai 0.73, providers/google.js
// and google-shared.js), as the other protocols were; where this file differs from pi it says why.
//
//   - Auth is x-goog-api-key. The base URL may end in an API version (/v1beta, as pi's catalog has it) or be the
//     bare host; GeminiURL adds /v1beta when it has none. A "models/" prefix on the model id (what ListModels
//     returns) is stripped.
//   - Streamed text deltas of one kind (thought or not) form one part; its signature is the last non-empty one that
//     came with it. Signatures are never moved across parts (Google's thought-signature docs).
//   - The turn is stored as those parts plus the model that wrote them (Message.Blocks, geminiTurn). Replayed to the
//     same model: thoughts and signatures kept (signatures only when valid base64: the API takes bytes). To another
//     model, or a turn from another protocol: thoughts become plain text, signatures are dropped.
//   - Function calls come whole. The Gemini API gives them no id, so one is made up; ids are sent only to models
//     behind Google's API that need them (claude-*, gpt-oss-*).
//   - Tool results of one turn are function responses in one user content. Images: inside functionResponse.parts
//     on Gemini 3+ (and non-Gemini models), else a separate user content after it.
//   - Tool schemas go as parametersJsonSchema (full JSON Schema), not parameters (an OpenAPI subset).
//   - Thinking (Request.Thinking == "gemini"): includeThoughts, and the effort as thinkingLevel on Gemini 3+ /
//     Gemma 4, as thinkingBudget on 2.x (a number effort is always a budget). "off" is the lowest the model allows:
//     MINIMAL only on Flash (unlike pi, see geminiFlash), LOW elsewhere.
//   - Caching is implicit on Gemini's side.
type gemini struct{ ep Endpoint }

// geminiTurn is Message.Blocks for a Gemini turn.
type geminiTurn struct {
	Model string           `json:"model"`
	Parts []map[string]any `json:"parts"`
}

// GeminiURL joins the base URL and a Gemini path (/models/…), adding /v1beta if the base has no API version.
func GeminiURL(base, path string) string {
	base = strings.TrimRight(base, "/")
	if i := strings.LastIndex(base, "/"); i < 0 || !strings.HasPrefix(base[i+1:], "v1") {
		base += "/v1beta"
	}
	return base + path
}

// GeminiModel is the model id as it goes in the URL path: without the "models/" prefix.
func GeminiModel(id string) string { return strings.TrimPrefix(id, "models/") }

// GeminiAuth is the auth headers for a key.
func GeminiAuth(key string) http.Header {
	h := http.Header{}
	if key != "" {
		h.Set("x-goog-api-key", key)
	}
	return h
}

var geminiCallSeq atomic.Int64

func (c *gemini) Stream(ctx context.Context, req Request, on func(Event)) (Result, error) {
	model := GeminiModel(req.Model)
	body := map[string]any{"contents": geminiContents(req.Messages, model)}
	if req.System != "" {
		body["systemInstruction"] = map[string]any{"parts": []any{map[string]any{"text": req.System}}}
	}
	if len(req.Tools) > 0 {
		var decls []any
		for _, t := range req.Tools {
			decls = append(decls, map[string]any{"name": t.Name, "description": t.Description, "parametersJsonSchema": t.Parameters})
		}
		body["tools"] = []any{map[string]any{"functionDeclarations": decls}}
	}
	gc := map[string]any{}
	if req.MaxTokens > 0 {
		gc["maxOutputTokens"] = req.MaxTokens
	}
	if tc := geminiThinking(model, req.Thinking, req.Effort); tc != nil {
		gc["thinkingConfig"] = tc
	}
	if len(gc) > 0 {
		body["generationConfig"] = gc
	}
	hdr := GeminiAuth(c.ep.APIKey)
	if req.CacheKey != "" {
		hdr.Set("X-Session-Id", req.CacheKey) // see post
	}
	rc, err := postURL(ctx, GeminiURL(c.ep.BaseURL, "/models/"+url.PathEscape(model)+":streamGenerateContent?alt=sse"), hdr, c.ep.Headers, body)
	if err != nil {
		return Result{Stop: "cancelled"}, err
	}
	defer rc.Close()

	var (
		parts   []map[string]any
		cur     map[string]any // the text / thought part deltas are going into; nil after a function call
		calls   []ToolCall
		text    strings.Builder
		thought strings.Builder
		usage   geminiUsage
		rawU    rawUsage
		finish  string
	)
	err = readSSE(rc, func(d []byte) error {
		var ev struct {
			Candidates []struct {
				Content struct {
					Parts []json.RawMessage `json:"parts"`
				} `json:"content"`
				FinishReason  string `json:"finishReason"`
				FinishMessage string `json:"finishMessage"`
			} `json:"candidates"`
			PromptFeedback *struct {
				BlockReason string `json:"blockReason"`
			} `json:"promptFeedback"`
			UsageMetadata json.RawMessage `json:"usageMetadata"`
			Error         *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
				Status  string `json:"status"`
			} `json:"error"`
		}
		if json.Unmarshal(d, &ev) != nil {
			return nil
		}
		if ev.Error != nil {
			if ev.Error.Code == 429 || ev.Error.Code >= 500 {
				return fmt.Errorf("stream disconnected: %s: %s", ev.Error.Status, ev.Error.Message)
			}
			return &APIError{Msg: ev.Error.Status + ": " + ev.Error.Message}
		}
		if len(ev.UsageMetadata) > 0 {
			_ = json.Unmarshal(ev.UsageMetadata, &usage)
			rawU.add(ev.UsageMetadata)
		}
		if len(ev.Candidates) == 0 {
			if ev.PromptFeedback != nil && ev.PromptFeedback.BlockReason != "" {
				return &APIError{Msg: "the prompt was blocked (" + ev.PromptFeedback.BlockReason + ")"}
			}
			return nil
		}
		cand := ev.Candidates[0]
		for _, raw := range cand.Content.Parts {
			var p struct {
				Text         *string `json:"text"`
				Thought      bool    `json:"thought"`
				Signature    string  `json:"thoughtSignature"`
				FunctionCall *struct {
					ID   string          `json:"id"`
					Name string          `json:"name"`
					Args json.RawMessage `json:"args"`
				} `json:"functionCall"`
			}
			if json.Unmarshal(raw, &p) != nil {
				continue
			}
			if p.Text != nil {
				if cur == nil || (cur["thought"] == true) != p.Thought {
					cur = map[string]any{"text": ""}
					if p.Thought {
						cur["thought"] = true
					}
					parts = append(parts, cur)
				}
				cur["text"] = cur["text"].(string) + *p.Text
				if p.Signature != "" {
					cur["thoughtSignature"] = p.Signature
				}
				if p.Thought {
					thought.WriteString(*p.Text)
				} else {
					text.WriteString(*p.Text)
				}
				if *p.Text != "" {
					kind := "text"
					if p.Thought {
						kind = "thought"
					}
					on(Event{Kind: kind, Text: *p.Text})
				}
			}
			if fc := p.FunctionCall; fc != nil {
				cur = nil
				args := string(fc.Args)
				if args == "" || args == "null" {
					args = "{}"
				}
				// as pi: made up when missing or a repeat within the turn
				id := fc.ID
				if id == "" || slices.ContainsFunc(calls, func(c ToolCall) bool { return c.ID == id }) {
					id = fmt.Sprintf("%s_%d_%d", fc.Name, time.Now().UnixMilli(), geminiCallSeq.Add(1))
				}
				i := len(calls)
				calls = append(calls, ToolCall{ID: id, Name: fc.Name, Args: args})
				on(Event{Kind: "tool", Index: i, Name: fc.Name})
				on(Event{Kind: "tool_args", Index: i, Text: args})
				part := map[string]any{"functionCall": map[string]any{"id": id, "name": fc.Name, "args": json.RawMessage(args)}}
				if p.Signature != "" {
					part["thoughtSignature"] = p.Signature
				}
				parts = append(parts, part)
			}
		}
		if cand.FinishReason != "" {
			finish = cand.FinishReason
			if cand.FinishMessage != "" {
				finish += ": " + cand.FinishMessage
			}
		}
		return nil
	})

	msg := Message{Role: "assistant", API: "gemini", Text: text.String()}
	if thought.Len() > 0 {
		msg.Reasoning = []Thought{{Text: thought.String()}}
	}
	res := Result{Message: msg, Usage: usage.toUsage()}
	res.Usage.Raw = rawU.json()
	if err = streamErr(ctx, err); err != nil {
		res.Stop = "cancelled"
		return res, err
	}
	if finish == "" {
		return res, errors.New("stream disconnected: no finishReason")
	}
	res.Message.ToolCalls = calls
	if len(parts) > 0 {
		res.Message.Blocks = mustJSON(geminiTurn{Model: model, Parts: parts})
	}
	reason, _, _ := strings.Cut(finish, ":")
	switch {
	case len(calls) > 0: // as pi: tool calls win over any finish reason
		res.Stop = "tool_use"
	case reason == "STOP":
		res.Stop = "end"
	case reason == "MAX_TOKENS":
		res.Stop = "length"
	default: // SAFETY, RECITATION, MALFORMED_FUNCTION_CALL…: the turn ended without an answer
		return res, &APIError{Msg: "the model stopped: " + finish}
	}
	return res, nil
}

type geminiUsage struct {
	Prompt     int `json:"promptTokenCount"`
	Candidates int `json:"candidatesTokenCount"`
	Thoughts   int `json:"thoughtsTokenCount"`
	Cached     int `json:"cachedContentTokenCount"`
}

// toUsage: promptTokenCount includes the cached part (Usage.Input does too, unlike pi's input); Output counts
// thinking, as pi does and as OpenAI's completion_tokens does.
func (u geminiUsage) toUsage() Usage {
	return Usage{Input: u.Prompt, Output: u.Candidates + u.Thoughts, CacheRead: u.Cached, Reasoning: u.Thoughts}
}

// ---- model families (pi's isGemini3ProModel / isGemini3FlashModel / isGemma4Model, widened to later versions) ----

var geminiMajorRe = regexp.MustCompile(`^gemini(?:-live)?-(\d+)`)

// geminiMajor is the version in a gemini-<n>… id; 0 = not one (gemini-flash-latest, claude-… behind Google).
func geminiMajor(model string) int {
	m := geminiMajorRe.FindStringSubmatch(strings.ToLower(GeminiModel(model)))
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

// geminiLevels: the model takes thinkingLevel rather than thinkingBudget. pi lists Gemini 3 Pro / Flash and Gemma 4;
// any Gemini 3+ here, since a newer model is more likely to keep 3's levels than to go back to budgets.
func geminiLevels(model string) bool {
	return geminiMajor(model) >= 3 || strings.Contains(strings.ToLower(model), "gemma-4") || strings.Contains(strings.ToLower(model), "gemma4")
}

// geminiFlash: the Flash family (Flash, Flash-Lite), the only ones that take MINIMAL. pi offers minimal on every
// non-Pro level model, but gemini-4-neon-medium answered MINIMAL with a 400 (2026-11-20).
func geminiFlash(model string) bool { return strings.Contains(strings.ToLower(model), "flash") }

// geminiPro: Gemini 3+ Pro, which takes only LOW / HIGH and can't turn thinking off.
func geminiPro(model string) bool {
	return geminiMajor(model) >= 3 && strings.Contains(strings.ToLower(model), "-pro")
}

// GeminiReasons: the model thinks (pi's catalog: reasoning = 2.5 and later, the -latest aliases, Gemma 4).
func GeminiReasons(model string) bool {
	id := strings.ToLower(GeminiModel(model))
	if n := geminiMajor(id); n >= 3 {
		return true
	}
	return strings.HasPrefix(id, "gemini-2.5") || strings.HasPrefix(id, "gemini-live-2.5") ||
		(strings.HasPrefix(id, "gemini-") && strings.HasSuffix(id, "-latest")) || geminiLevels(id)
}

// GeminiEfforts: the levels to offer, low to high.
func GeminiEfforts(model string) []string {
	switch {
	case geminiPro(model):
		return []string{"low", "high"}
	case geminiLevels(model) && !geminiFlash(model):
		return []string{"low", "medium", "high"}
	}
	return []string{"minimal", "low", "medium", "high"} // Flash; on 2.5 these are budgets
}

// geminiThinking is generationConfig.thinkingConfig; nil = none (Thinking isn't "gemini").
func geminiThinking(model, thinking, effort string) map[string]any {
	if thinking != "gemini" {
		return nil
	}
	if effort == "off" { // pi's getDisabledThinkingConfig: hidden, and as low as the model goes
		switch {
		case geminiLevels(model) && geminiFlash(model):
			return map[string]any{"thinkingLevel": "MINIMAL"}
		case geminiLevels(model):
			return map[string]any{"thinkingLevel": "LOW"}
		}
		return map[string]any{"thinkingBudget": 0}
	}
	tc := map[string]any{"includeThoughts": true}
	if effort == "" {
		return tc
	}
	if n, err := strconv.Atoi(effort); err == nil {
		tc["thinkingBudget"] = n
		return tc
	}
	if geminiLevels(model) {
		tc["thinkingLevel"] = geminiLevel(model, effort)
	} else {
		tc["thinkingBudget"] = geminiBudget(model, effort)
	}
	return tc
}

// geminiLevel is pi's getThinkingLevel; an effort it doesn't know goes as written.
func geminiLevel(model, effort string) string {
	if geminiPro(model) {
		switch effort {
		case "minimal", "low":
			return "LOW"
		case "medium", "high":
			return "HIGH"
		}
	}
	return strings.ToUpper(effort)
}

// geminiBudget is pi's getGoogleBudget for 2.x; -1 = the model decides.
func geminiBudget(model, effort string) int {
	id := strings.ToLower(model)
	var b map[string]int
	switch {
	case strings.Contains(id, "2.5-pro"):
		b = map[string]int{"minimal": 128, "low": 2048, "medium": 8192, "high": 32768}
	case strings.Contains(id, "2.5-flash-lite"):
		b = map[string]int{"minimal": 512, "low": 2048, "medium": 8192, "high": 24576}
	case strings.Contains(id, "2.5-flash"):
		b = map[string]int{"minimal": 128, "low": 2048, "medium": 8192, "high": 24576}
	}
	if n, ok := b[effort]; ok {
		return n
	}
	return -1
}

// ---- history (pi's convertMessages + transformMessages) ----

// geminiNeedsCallID: models behind Google's API that need tool call ids (pi's requiresToolCallId).
func geminiNeedsCallID(model string) bool {
	return strings.HasPrefix(model, "claude-") || strings.HasPrefix(model, "gpt-oss-")
}

var geminiCallIDBad = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

func geminiCallID(id string) string {
	id = geminiCallIDBad.ReplaceAllString(id, "_")
	if len(id) > 64 {
		id = id[:64]
	}
	return id
}

var base64Sig = regexp.MustCompile(`^[A-Za-z0-9+/]+={0,2}$`)

func validSignature(s string) bool { return s != "" && len(s)%4 == 0 && base64Sig.MatchString(s) }

func geminiContents(msgs []Message, model string) []map[string]any {
	needID := geminiNeedsCallID(model)
	names := map[string]string{} // call id → function name: a functionResponse names its function
	for _, m := range msgs {
		for _, tc := range m.ToolCalls {
			names[tc.ID] = tc.Name
		}
	}
	out := []map[string]any{}
	for _, m := range msgs {
		switch m.Role {
		case "user":
			if parts := geminiParts(m.Content); len(parts) > 0 {
				out = append(out, map[string]any{"role": "user", "parts": parts})
			}
		case "assistant":
			if parts := geminiModelParts(m, model, needID); len(parts) > 0 {
				out = append(out, map[string]any{"role": "model", "parts": parts})
			}
		case "tool":
			var texts []string
			var images []any
			for _, p := range m.Content {
				switch p.Type {
				case "text":
					texts = append(texts, p.Text)
				case "image":
					images = append(images, geminiParts([]Part{p})...)
				}
			}
			value := strings.Join(texts, "\n")
			if value == "" && len(images) > 0 {
				value = "(see attached image)"
			}
			key := "output"
			if m.IsError {
				key = "error"
			}
			fr := map[string]any{"name": names[m.CallID], "response": map[string]any{key: value}}
			multimodal := geminiMajor(model) == 0 || geminiMajor(model) >= 3
			if len(images) > 0 && multimodal {
				fr["parts"] = images
			}
			if needID {
				fr["id"] = geminiCallID(m.CallID)
			}
			part := map[string]any{"functionResponse": fr}
			// all function responses of a turn in one user content
			if n := len(out); n > 0 && out[n-1]["role"] == "user" && hasFunctionResponse(out[n-1]) {
				out[n-1]["parts"] = append(out[n-1]["parts"].([]any), part)
			} else {
				out = append(out, map[string]any{"role": "user", "parts": []any{part}})
			}
			if len(images) > 0 && !multimodal {
				out = append(out, map[string]any{"role": "user", "parts": append([]any{map[string]any{"text": "Tool result image:"}}, images...)})
			}
		}
	}
	return out
}

func hasFunctionResponse(c map[string]any) bool {
	for _, p := range c["parts"].([]any) {
		if m, ok := p.(map[string]any); ok && m["functionResponse"] != nil {
			return true
		}
	}
	return false
}

// geminiModelParts rebuilds an assistant turn for model: the stored parts when it was Gemini's, else from Text /
// Reasoning / ToolCalls. Thoughts and signatures survive only when the same model wrote the turn.
func geminiModelParts(m Message, model string, needID bool) []any {
	var turn geminiTurn
	if m.API == "gemini" && len(m.Blocks) > 0 {
		_ = json.Unmarshal(m.Blocks, &turn)
	}
	same := turn.Model == model
	stored := turn.Parts
	if len(stored) == 0 {
		// another protocol (or a partial turn, whose Blocks were cleared): pi converts its thinking to text
		for _, t := range m.Reasoning {
			stored = append(stored, map[string]any{"text": t.Text})
		}
		stored = append(stored, map[string]any{"text": m.Text})
		for _, tc := range m.ToolCalls {
			stored = append(stored, map[string]any{"functionCall": map[string]any{"id": tc.ID, "name": tc.Name, "args": jsonOrEmpty(tc.Args)}})
		}
		same = false
	}
	var out []any
	for _, p := range stored {
		sig, _ := p["thoughtSignature"].(string)
		keepSig := same && validSignature(sig)
		if fc, ok := p["functionCall"].(map[string]any); ok {
			call := map[string]any{"name": fc["name"], "args": fc["args"]}
			if call["args"] == nil {
				call["args"] = map[string]any{}
			}
			if id, _ := fc["id"].(string); needID && id != "" {
				call["id"] = geminiCallID(id)
			}
			part := map[string]any{"functionCall": call}
			if keepSig {
				part["thoughtSignature"] = sig
			}
			out = append(out, part)
			continue
		}
		t, isText := p["text"].(string)
		if !isText {
			if same {
				out = append(out, p) // a part kind we don't know, from this model: as it came
			}
			continue
		}
		if strings.TrimSpace(t) == "" {
			continue // pi skips empty text and thinking, signature or not
		}
		part := map[string]any{"text": t}
		if p["thought"] == true && same {
			part["thought"] = true
		}
		if keepSig {
			part["thoughtSignature"] = sig
		}
		out = append(out, part)
	}
	return out
}

func geminiParts(parts []Part) []any {
	var out []any
	for _, p := range parts {
		switch p.Type {
		case "text":
			if strings.TrimSpace(p.Text) != "" {
				out = append(out, map[string]any{"text": p.Text})
			}
		case "image":
			mime := p.MIME
			if mime == "" {
				mime = "image/png"
			}
			out = append(out, map[string]any{"inlineData": map[string]any{"mimeType": mime, "data": p.Data}})
		}
	}
	return out
}
