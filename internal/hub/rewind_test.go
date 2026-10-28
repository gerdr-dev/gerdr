package hub

import (
	"encoding/json"
	"testing"
)

// 导入 / 追上之后的历史是 agent 重放的 user_message_chunk，回退要认得它（不然「找不到这条消息」）。
func TestUserChunk(t *testing.T) {
	e := &Event{Kind: EvUpdate, Update: json.RawMessage(`{"sessionUpdate":"user_message_chunk","messageId":"u1","content":{"type":"text","text":"hi"}}`)}
	id, c, ok := userChunk(e)
	if !ok || id != "u1" || string(c) != `{"type":"text","text":"hi"}` {
		t.Fatalf("%v %q %s", ok, id, c)
	}
	if _, _, ok := userChunk(&Event{Kind: EvUpdate, Update: json.RawMessage(`{"sessionUpdate":"agent_message_chunk","messageId":"a1"}`)}); ok {
		t.Fatal("agent chunk")
	}
}
