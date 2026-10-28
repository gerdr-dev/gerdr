package hub

import (
	"encoding/json"
	"testing"
	"time"
)

func TestChunkMerge(t *testing.T) {
	a := json.RawMessage(`{"sessionUpdate":"agent_message_chunk","messageId":"m1","content":{"type":"text","text":"Hel"}}`)
	b := json.RawMessage(`{"sessionUpdate":"agent_message_chunk","messageId":"m1","content":{"type":"text","text":"lo"}}`)
	c := json.RawMessage(`{"sessionUpdate":"agent_thought_chunk","messageId":"m1","content":{"type":"text","text":"x"}}`)
	withMeta := json.RawMessage(`{"sessionUpdate":"agent_message_chunk","messageId":"m1","content":{"type":"text","text":""},"_meta":{"gerdr":{"quickReplies":[]}}}`)
	ka, _, ok := chunkKey(a)
	kb, tb, _ := chunkKey(b)
	kc, _, _ := chunkKey(c)
	if !ok || ka != kb || ka == kc {
		t.Fatalf("keys: %q %q %q", ka, kb, kc)
	}
	if _, _, ok := chunkKey(withMeta); ok {
		t.Fatal("chunk with _meta must not merge (it carries quick replies)")
	}
	var m struct {
		Content struct{ Text string } `json:"content"`
	}
	_ = json.Unmarshal(mergeChunk(a, tb), &m)
	if m.Content.Text != "Hello" {
		t.Fatalf("merged = %q", m.Content.Text)
	}
}

func TestRetryWaits(t *testing.T) {
	var sum time.Duration
	for i, d := range retryWaits {
		if d > time.Minute || (i > 0 && d > 2*retryWaits[i-1]) {
			t.Fatalf("第 %d 次等 %v", i, d)
		}
		sum += d
	}
	if sum != 5*time.Minute {
		t.Fatalf("总共等 %v，应该是 5 分钟：%v", sum, retryWaits)
	}
}

func TestRetryable(t *testing.T) {
	for _, c := range []struct {
		msg  string
		want bool
	}{
		{"API Error: Connection error.", true},
		{"stream disconnected before completion", true},
		{"claude 进程意外退出：exit status 1", true},
		{"API Error: 529 overloaded_error", true},
		{"You've hit your usage limit", false},
		{"rate limit exceeded", false},
		{"Invalid model name", false},
		{"Failed to authenticate. API Error: 403 Request not allowed", true},
		{"Failed to authenticate. API Error: 401 Invalid authentication credentials", false},
	} {
		if got := retryable(c.msg); got != c.want {
			t.Errorf("%q: got %v", c.msg, got)
		}
	}
}
