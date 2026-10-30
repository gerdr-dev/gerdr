package llm

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// 真调接口（花钱）：NATIVE_LIVE_BASE / NATIVE_LIVE_KEY 给地址和 key，NATIVE_LIVE_MODELS 给 "api:model,..."。
// NATIVE_LIVE_EFFORT / NATIVE_LIVE_PROMPT override the effort and the question (to get thinking blocks replayed).
//
//	NATIVE_LIVE_MODELS=responses:gpt-6.1-sol,chat:deepseek-v4-pro,anthropic:claude-opus-5-5 go test ./native/llm -run Live -v
func TestLiveToolRoundTrip(t *testing.T) {
	base, key := os.Getenv("NATIVE_LIVE_BASE"), os.Getenv("NATIVE_LIVE_KEY")
	if base == "" || key == "" {
		t.Skip("NATIVE_LIVE_BASE / NATIVE_LIVE_KEY not set")
	}
	for _, spec := range strings.Split(os.Getenv("NATIVE_LIVE_MODELS"), ",") {
		api, model, _ := strings.Cut(spec, ":")
		t.Run(model, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			c := New(api, Endpoint{BaseURL: base, APIKey: key})
			req := Request{
				Model:  model,
				System: "You are a test agent. Use the tools when asked.",
				Tools: []Tool{{Name: "add", Description: "Add two integers", Parameters: map[string]any{
					"type": "object", "properties": map[string]any{"a": map[string]any{"type": "integer"}, "b": map[string]any{"type": "integer"}}, "required": []string{"a", "b"},
				}}},
				Messages: []Message{{Role: "user", Content: []Part{TextPart("Use the add tool to compute 17+25, then tell me the result in one short sentence.")}}},
				CacheKey: "live-test",
			}
			if api == "anthropic" {
				req.Thinking = "adaptive"
				if th := os.Getenv("NATIVE_LIVE_THINKING"); th != "" {
					req.Thinking = th
				}
			}
			if api == "chat" && strings.Contains(model, "deepseek") {
				req.Thinking = "deepseek"
				req.Effort = "high"
			} else {
				req.Effort = "low"
			}
			if e := os.Getenv("NATIVE_LIVE_EFFORT"); e != "" {
				req.Effort = e
			}
			if p := os.Getenv("NATIVE_LIVE_PROMPT"); p != "" { // e.g. one that makes the model think first
				req.Messages = []Message{{Role: "user", Content: []Part{TextPart(p)}}}
			}
			var thoughts int
			res, err := c.Stream(ctx, req, func(e Event) {
				if e.Kind == "thought" {
					thoughts++
				}
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("turn1 stop=%s calls=%+v thoughtChunks=%d reasoning=%d usage=%+v", res.Stop, res.Message.ToolCalls, thoughts, len(res.Message.Reasoning), res.Usage)
			if res.Stop != "tool_use" || len(res.Message.ToolCalls) != 1 {
				t.Fatalf("want one tool call, got %+v", res)
			}
			tc := res.Message.ToolCalls[0]
			req.Messages = append(req.Messages, res.Message, Message{Role: "tool", CallID: tc.ID, Content: []Part{TextPart("42")}})
			res2, err := c.Stream(ctx, req, func(Event) {})
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("turn2 stop=%s text=%q usage=%+v", res2.Stop, res2.Message.Text, res2.Usage)
			if !strings.Contains(res2.Message.Text, "42") {
				t.Fatalf("answer lacks 42: %q", res2.Message.Text)
			}
		})
	}
}

// TestLiveCache: a long system prompt (well over the largest minimum cacheable prefix, 4096 tokens) and three
// requests that each extend the last; the 2nd and 3rd should read most of their input from cache. Costs money.
//
//	NATIVE_LIVE_MODELS=anthropic:claude-opus-5-5 go test ./native/llm -run LiveCache -v
func TestLiveCache(t *testing.T) {
	base, key := os.Getenv("NATIVE_LIVE_BASE"), os.Getenv("NATIVE_LIVE_KEY")
	if base == "" || key == "" {
		t.Skip("NATIVE_LIVE_BASE / NATIVE_LIVE_KEY not set")
	}
	var sys strings.Builder
	sys.WriteString("You are a test agent. Answer in one short sentence.\n\nReference rules (ignore unless asked):\n")
	for i := range 400 {
		fmt.Fprintf(&sys, "Rule %d: when the user mentions item %d, reply that item %d belongs to shelf %d in aisle %d.\n", i, i, i, i%17, i%5)
	}
	for _, spec := range strings.Split(os.Getenv("NATIVE_LIVE_MODELS"), ",") {
		api, model, _ := strings.Cut(spec, ":")
		t.Run(model, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			c := New(api, Endpoint{BaseURL: base, APIKey: key})
			req := Request{Model: model, System: sys.String(), Effort: "low", CacheKey: "live-cache-" + model}
			if api == "anthropic" {
				req.Thinking = "adaptive"
			}
			var reads []int
			for i, q := range []string{"Which shelf is item 12 on?", "And item 250?", "And item 399?"} {
				req.Messages = append(req.Messages, Message{Role: "user", Content: []Part{TextPart(q)}})
				res, err := c.Stream(ctx, req, func(Event) {})
				if err != nil {
					t.Fatal(err)
				}
				t.Logf("request %d: usage=%+v text=%q", i+1, res.Usage, res.Message.Text)
				reads = append(reads, res.Usage.CacheRead)
				req.Messages = append(req.Messages, res.Message)
			}
			if reads[1] == 0 || reads[2] <= reads[1] {
				t.Fatalf("cache not read / not growing: %v", reads)
			}
		})
	}
}
