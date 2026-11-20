package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// The shape streamGenerateContent?alt=sse sends: thought summary deltas, text deltas with the signature on the
// last one, then a function call with its own signature, finishReason and usage on the last chunk.
const geminiStream = `data: {"candidates":[{"content":{"role":"model","parts":[{"text":"Let me ","thought":true}]}}]}

data: {"candidates":[{"content":{"role":"model","parts":[{"text":"look.","thought":true}]}}]}

data: {"candidates":[{"content":{"role":"model","parts":[{"text":"Reading "}]}}]}

data: {"candidates":[{"content":{"role":"model","parts":[{"text":"it.","thoughtSignature":"U0lHMQ=="}]}}]}

data: {"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"Read","args":{"path":"a.go"}},"thoughtSignature":"U0lHMg=="}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":100,"cachedContentTokenCount":60,"candidatesTokenCount":20,"thoughtsTokenCount":30,"totalTokenCount":150}}

`

func TestGeminiStream(t *testing.T) {
	var got map[string]any
	var hdr http.Header
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.RequestURI()
		hdr = r.Header
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &got)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, geminiStream)
	}))
	defer srv.Close()

	c := New("gemini", Endpoint{BaseURL: srv.URL, APIKey: "k"}) // no version: /v1beta added
	var thought, text, args strings.Builder
	var tool string
	res, err := c.Stream(context.Background(), Request{
		Model: "models/gemini-3-pro-preview", System: "sys", Thinking: "gemini", Effort: "medium",
		Tools:    []Tool{{Name: "Read", Description: "read", Parameters: map[string]any{"type": "object", "$schema": "x"}}},
		Messages: []Message{{Role: "user", Content: []Part{TextPart("hi")}}},
	}, func(e Event) {
		switch e.Kind {
		case "thought":
			thought.WriteString(e.Text)
		case "text":
			text.WriteString(e.Text)
		case "tool":
			tool = e.Name
		case "tool_args":
			args.WriteString(e.Text)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if path != "/v1beta/models/gemini-3-pro-preview:streamGenerateContent?alt=sse" {
		t.Errorf("path %s", path)
	}
	if hdr.Get("x-goog-api-key") != "k" || hdr.Get("Authorization") != "" {
		t.Errorf("auth headers %v", hdr)
	}
	gc := got["generationConfig"].(map[string]any)["thinkingConfig"].(map[string]any)
	if gc["thinkingLevel"] != "HIGH" || gc["includeThoughts"] != true { // Pro: medium → HIGH, as pi
		t.Errorf("thinkingConfig %v", gc)
	}
	decl := got["tools"].([]any)[0].(map[string]any)["functionDeclarations"].([]any)[0].(map[string]any)
	if decl["parametersJsonSchema"] == nil || decl["parameters"] != nil {
		t.Errorf("declaration %v", decl)
	}
	if got["systemInstruction"] == nil {
		t.Error("no systemInstruction")
	}
	if thought.String() != "Let me look." || text.String() != "Reading it." || tool != "Read" || args.String() != `{"path":"a.go"}` {
		t.Errorf("events: %q %q %q %q", thought.String(), text.String(), tool, args.String())
	}
	m := res.Message
	if res.Stop != "tool_use" || m.Text != "Reading it." || len(m.ToolCalls) != 1 || m.ToolCalls[0].ID == "" || m.API != "gemini" {
		t.Fatalf("result %+v", res)
	}
	if u := res.Usage; u.Input != 100 || u.CacheRead != 60 || u.Output != 50 || u.Reasoning != 30 {
		t.Errorf("usage %+v", u)
	}
	var turn geminiTurn
	_ = json.Unmarshal(m.Blocks, &turn)
	if turn.Model != "gemini-3-pro-preview" || len(turn.Parts) != 3 {
		t.Fatalf("blocks %s", m.Blocks)
	}
	if turn.Parts[0]["thought"] != true || turn.Parts[0]["text"] != "Let me look." ||
		turn.Parts[1]["text"] != "Reading it." || turn.Parts[1]["thoughtSignature"] != "U0lHMQ==" ||
		turn.Parts[2]["thoughtSignature"] != "U0lHMg==" {
		t.Errorf("parts %s", m.Blocks)
	}

	// Replayed to the same model: thoughts and signatures as they came, no call id.
	hist := []Message{{Role: "user", Content: []Part{TextPart("hi")}}, m,
		{Role: "tool", CallID: m.ToolCalls[0].ID, Content: []Part{TextPart("package a")}}}
	same := geminiContents(hist, "gemini-3-pro-preview")
	want := `[{"parts":[{"text":"hi"}],"role":"user"},` +
		`{"parts":[{"text":"Let me look.","thought":true},{"text":"Reading it.","thoughtSignature":"U0lHMQ=="},{"functionCall":{"args":{"path":"a.go"},"name":"Read"},"thoughtSignature":"U0lHMg=="}],"role":"model"},` +
		`{"parts":[{"functionResponse":{"name":"Read","response":{"output":"package a"}}}],"role":"user"}]`
	if b, _ := json.Marshal(same); string(b) != want {
		t.Errorf("same model:\n%s\nwant\n%s", b, want)
	}
	// To another model: the thought is plain text, no signatures.
	other := geminiContents(hist, "gemini-3-flash-preview")
	if b, _ := json.Marshal(other[1]); string(b) != `{"parts":[{"text":"Let me look."},{"text":"Reading it."},{"functionCall":{"args":{"path":"a.go"},"name":"Read"}}],"role":"model"}` {
		t.Errorf("other model: %s", b)
	}
}

func TestGeminiContents(t *testing.T) {
	img := Part{Type: "image", MIME: "image/jpeg", Data: "AAAA"}
	hist := []Message{
		{Role: "user", Content: []Part{TextPart("look")}},
		// from another protocol: rebuilt, reasoning as text, ids only for models that need them
		{Role: "assistant", API: "anthropic", Text: "Two.", Reasoning: []Thought{{Text: "hmm", Encrypted: "sig"}},
			ToolCalls: []ToolCall{{ID: "toolu_1", Name: "Shot", Args: `{}`}, {ID: "toolu_2", Name: "Read", Args: `bad`}}},
		{Role: "tool", CallID: "toolu_1", Content: []Part{img}},
		{Role: "tool", CallID: "toolu_2", IsError: true, Content: []Part{TextPart("no such file")}},
		{Role: "user", Content: []Part{TextPart("and?")}},
	}
	b, _ := json.Marshal(geminiContents(hist, "gemini-3-pro-preview"))
	want := `[{"parts":[{"text":"look"}],"role":"user"},` +
		`{"parts":[{"text":"hmm"},{"text":"Two."},{"functionCall":{"args":{},"name":"Shot"}},{"functionCall":{"args":{},"name":"Read"}}],"role":"model"},` +
		`{"parts":[{"functionResponse":{"name":"Shot","parts":[{"inlineData":{"data":"AAAA","mimeType":"image/jpeg"}}],"response":{"output":"(see attached image)"}}},` +
		`{"functionResponse":{"name":"Read","response":{"error":"no such file"}}}],"role":"user"},` +
		`{"parts":[{"text":"and?"}],"role":"user"}]`
	if string(b) != want {
		t.Errorf("gemini 3:\n%s\nwant\n%s", b, want)
	}
	// Gemini 2.5: the image goes in a user content of its own
	c := geminiContents(hist[:3], "gemini-2.5-pro")
	if b, _ := json.Marshal(c[len(c)-1]); string(b) != `{"parts":[{"text":"Tool result image:"},{"inlineData":{"data":"AAAA","mimeType":"image/jpeg"}}],"role":"user"}` {
		t.Errorf("2.5 image: %s", b)
	}
	// claude behind Google's API: ids sent, normalized
	c = geminiContents(hist[:3], "claude-opus-5-5")
	if b, _ := json.Marshal(c); !strings.Contains(string(b), `"id":"toolu_1"`) {
		t.Errorf("claude ids: %s", b)
	}
}

func TestGeminiThinking(t *testing.T) {
	for _, c := range []struct {
		model, effort string
		want          map[string]any
	}{
		{"gemini-3-pro-preview", "low", map[string]any{"includeThoughts": true, "thinkingLevel": "LOW"}},
		{"gemini-3-pro-preview", "off", map[string]any{"thinkingLevel": "LOW"}},
		{"gemini-3-flash-preview", "medium", map[string]any{"includeThoughts": true, "thinkingLevel": "MEDIUM"}},
		{"gemini-3-flash-preview", "off", map[string]any{"thinkingLevel": "MINIMAL"}},
		{"gemini-4-neon-medium", "medium", map[string]any{"includeThoughts": true, "thinkingLevel": "MEDIUM"}},
		{"gemini-4-neon-medium", "off", map[string]any{"thinkingLevel": "LOW"}}, // MINIMAL is a 400 there
		{"gemini-2.5-pro", "high", map[string]any{"includeThoughts": true, "thinkingBudget": 32768}},
		{"gemini-2.5-flash", "off", map[string]any{"thinkingBudget": 0}},
		{"gemini-2.5-flash", "4096", map[string]any{"includeThoughts": true, "thinkingBudget": 4096}},
		{"gemini-flash-latest", "high", map[string]any{"includeThoughts": true, "thinkingBudget": -1}},
	} {
		if got := geminiThinking(c.model, "gemini", c.effort); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s %s: %v, want %v", c.model, c.effort, got, c.want)
		}
	}
	if geminiThinking("gemini-2.0-flash", "", "high") != nil {
		t.Error("thinking without Thinking=gemini")
	}
	if !GeminiReasons("gemini-4-neon-medium") || GeminiReasons("gemini-2.0-flash") || !GeminiReasons("gemini-flash-latest") {
		t.Error("GeminiReasons")
	}
}

func TestGeminiURL(t *testing.T) {
	for base, want := range map[string]string{
		"https://generativelanguage.googleapis.com":         "https://generativelanguage.googleapis.com/v1beta/models",
		"https://generativelanguage.googleapis.com/v1beta/": "https://generativelanguage.googleapis.com/v1beta/models",
		"https://gw.example.com/google/v1":                  "https://gw.example.com/google/v1/models",
	} {
		if got := GeminiURL(base, "/models"); got != want {
			t.Errorf("%s: %s", base, got)
		}
	}
}

func TestGeminiStopReasons(t *testing.T) {
	for stream, wantErr := range map[string]string{
		`data: {"candidates":[{"content":{"parts":[{"text":"x"}]},"finishReason":"SAFETY"}]}` + "\n\n": "SAFETY",
		`data: {"promptFeedback":{"blockReason":"OTHER"}}` + "\n\n":                                     "blocked",
		`data: {"candidates":[{"content":{"parts":[{"text":"x"}]}}]}` + "\n\n":                          "stream disconnected",
		`data: {"error":{"code":503,"message":"overloaded","status":"UNAVAILABLE"}}` + "\n\n":           "stream disconnected",
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, stream) }))
		_, err := New("gemini", Endpoint{BaseURL: srv.URL}).Stream(context.Background(), Request{Model: "gemini-3-flash-preview"}, func(Event) {})
		srv.Close()
		if err == nil || !strings.Contains(err.Error(), wantErr) {
			t.Errorf("%s: %v", stream, err)
		}
	}
}
