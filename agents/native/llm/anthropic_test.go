package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A recorded-shape stream: thinking (with signature), text, a tool call whose input arrives in pieces.
const anthropicStream = `event: message_start
data: {"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":10,"cache_read_input_tokens":90,"cache_creation_input_tokens":5,"output_tokens":1}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Let me look."}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"SIG"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"Reading it."}}

event: content_block_start
data: {"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"toolu_1","name":"Read","input":{}}}

event: content_block_delta
data: {"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"path\":"}}

event: content_block_delta
data: {"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"\"a.go\"}"}}

event: ping
data: {"type":"ping"}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":42}}

event: message_stop
data: {"type":"message_stop"}

`

func TestAnthropicStream(t *testing.T) {
	var got map[string]any
	var hdr http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			http.Error(w, "bad path "+r.URL.Path, 404)
			return
		}
		hdr = r.Header
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &got)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, anthropicStream)
	}))
	defer srv.Close()

	c := New("anthropic", Endpoint{BaseURL: srv.URL, APIKey: "k"}) // no /v1: added
	var thought, text, args strings.Builder
	res, err := c.Stream(context.Background(), Request{
		Model: "claude-opus-5-5", System: "sys", Thinking: "adaptive", Effort: "high",
		Tools:    []Tool{{Name: "Read", Parameters: map[string]any{"type": "object"}}},
		Messages: []Message{{Role: "user", Content: []Part{TextPart("hi")}}},
	}, func(e Event) {
		switch e.Kind {
		case "thought":
			thought.WriteString(e.Text)
		case "text":
			text.WriteString(e.Text)
		case "tool_args":
			args.WriteString(e.Text)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if hdr.Get("x-api-key") != "k" || hdr.Get("anthropic-version") == "" || hdr.Get("Authorization") != "" {
		t.Fatalf("auth headers: %v", hdr)
	}
	if got["max_tokens"] == nil || got["thinking"].(map[string]any)["type"] != "adaptive" ||
		got["output_config"].(map[string]any)["effort"] != "high" {
		t.Fatalf("body: %v", got)
	}
	if res.Stop != "tool_use" || thought.String() != "Let me look." || text.String() != "Reading it." || args.String() != `{"path":"a.go"}` {
		t.Fatalf("stream: %+v %q %q %q", res, thought.String(), text.String(), args.String())
	}
	m := res.Message
	if len(m.ToolCalls) != 1 || m.ToolCalls[0].ID != "toolu_1" || m.ToolCalls[0].Args != `{"path":"a.go"}` {
		t.Fatalf("calls: %+v", m.ToolCalls)
	}
	if res.Usage.Input != 105 || res.Usage.CacheRead != 90 || res.Usage.Output != 42 {
		t.Fatalf("usage: %+v", res.Usage)
	}
	var blocks []map[string]any
	_ = json.Unmarshal(m.Blocks, &blocks)
	if len(blocks) != 3 || blocks[0]["type"] != "thinking" || blocks[0]["signature"] != "SIG" || blocks[2]["type"] != "tool_use" {
		t.Fatalf("blocks: %s", m.Blocks)
	}
}

func TestAnthropicMessages(t *testing.T) {
	asst := Message{Role: "assistant", API: "anthropic", Text: "x", Blocks: json.RawMessage(`[{"type":"thinking","thinking":"t","signature":"s"},{"type":"text","text":"x"},{"type":"tool_use","id":"a","name":"Read","input":{}},{"type":"tool_use","id":"b","name":"Read","input":{}}]`),
		ToolCalls: []ToolCall{{ID: "a", Name: "Read", Args: "{}"}, {ID: "b", Name: "Read", Args: "{}"}}}
	msgs := anthropicMessages([]Message{
		{Role: "user", Content: []Part{TextPart("hi"), TextPart("")}},
		asst,
		{Role: "tool", CallID: "a", Content: []Part{TextPart("one"), {Type: "image", MIME: "image/jpeg", Data: "AAA"}}},
		{Role: "tool", CallID: "b", IsError: true, Content: []Part{TextPart("boom")}},
		{Role: "user", Content: []Part{TextPart("wait")}},
		// a GPT turn: its reasoning can't go to Claude
		{Role: "assistant", API: "responses", Text: "done", Reasoning: []Thought{{Text: "r", Encrypted: "enc"}}},
		{Role: "user", Content: []Part{TextPart("next")}},
	})
	markLastForCache(msgs, map[string]any{"type": "ephemeral"})
	b, _ := json.Marshal(msgs)
	s := string(b)
	if len(msgs) != 5 {
		t.Fatalf("want 5 messages (tool results + interjection merged), got %d: %s", len(msgs), s)
	}
	if !strings.Contains(s, `"signature":"s"`) || strings.Contains(s, "enc") {
		t.Fatalf("thinking replay: %s", s)
	}
	tr := msgs[2]["content"].([]any)
	if len(tr) != 3 || tr[0].(map[string]any)["type"] != "tool_result" || tr[1].(map[string]any)["is_error"] != true {
		t.Fatalf("tool results: %s", s)
	}
	if !strings.Contains(s, `"media_type":"image/jpeg"`) || strings.Contains(s, `"text":""`) {
		t.Fatalf("parts: %s", s)
	}
	last := msgs[4]["content"].([]any)
	if last[len(last)-1].(map[string]any)["cache_control"] == nil {
		t.Fatalf("no cache breakpoint on the last block: %s", s)
	}
}

func TestAnthropicURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://api.anthropic.com":      "https://api.anthropic.com/v1/messages",
		"https://api.anthropic.com/":     "https://api.anthropic.com/v1/messages",
		"https://gw.example.com/x/v1":    "https://gw.example.com/x/v1/messages",
		"https://gw.example.com/x/v1/":   "https://gw.example.com/x/v1/messages",
		"https://gw.example.com/anthrop": "https://gw.example.com/anthrop/v1/messages",
	} {
		if got := AnthropicURL(in, "/messages"); got != want {
			t.Errorf("%s: %s", in, got)
		}
	}
}

// Thinking off: Sonnet 5.x takes only between_tools, the other adaptive Claude models disabled.
func TestAnthropicThinkingOff(t *testing.T) {
	for model, want := range map[string]string{"claude-sonnet-5-5": "between_tools", "claude-sonnet-5": "between_tools", "claude-opus-5-5": "disabled", "claude-fable-5-1": "disabled"} {
		var body map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			json.NewDecoder(r.Body).Decode(&body)
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
		}))
		c := New("anthropic", Endpoint{BaseURL: srv.URL})
		c.Stream(context.Background(), Request{Model: model, Thinking: "updates", Effort: "off", Messages: []Message{{Role: "user", Content: []Part{TextPart("hi")}}}}, func(Event) {})
		srv.Close()
		th, _ := body["thinking"].(map[string]any)
		if th["type"] != want || body["output_config"] != nil {
			t.Errorf("%s: thinking %v output_config %v, want type %s", model, body["thinking"], body["output_config"], want)
		}
	}
}
