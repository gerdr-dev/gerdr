// Package llm is how the native agent talks to models: a vendor-neutral message format and three wire
// protocols (OpenAI Chat Completions, OpenAI Responses, Anthropic Messages), hand-written so every gateway quirk
// is visible in one place (DeepSeek's reasoning_content must be sent back, Responses reasoning goes back with its
// encrypted_content, Claude's thinking blocks are replayed verbatim…; each noted in its file).
//
// It is usable on its own for one-off calls: New(api, Endpoint{…}).Stream(ctx, Request{…}, onEvent). To pick
// the protocol, thinking format and effort from a native.Config, use native.Complete.
package llm

import (
	"context"
	"encoding/json"
)

// Message 是存进会话记录、也是发给模型的一条消息。只有四种角色：
//   - user：人说的话（或者插话、压缩后的摘要）。Content 里是文字和图片；
//   - assistant：模型的回复。Text 是正文，Reasoning 是思考（有的要原样带回去），ToolCalls 是它要调的工具；
//   - tool：一个工具调用的结果，CallID 对上 assistant 里的 ToolCall.ID。
//
// 一条 assistant 后面必须紧跟它每个 ToolCall 的 tool 结果，少一个两家接口都会 400 —— 打断时也要补齐（loop.go）。
type Message struct {
	Role      string     `json:"role"`
	Content   []Part     `json:"content,omitempty"`
	Text      string     `json:"text,omitempty"`
	Reasoning []Thought  `json:"reasoning,omitempty"`
	ToolCalls []ToolCall `json:"toolCalls,omitempty"`
	CallID    string     `json:"callId,omitempty"`
	IsError   bool       `json:"isError,omitempty"`
	// API：assistant 是哪种协议生成的。不透明的推理（Responses 的 encrypted_content）只能还给同一种协议，
	// 中途换了模型 / 协议就丢掉，只留文字。
	API string `json:"api,omitempty"`
	// Blocks: the assistant's content blocks exactly as the Anthropic API returned them (thinking with its
	// signature, text, tool_use, in their original order). Sent back verbatim to the same protocol: newer Claude
	// models reject (or silently drop thinking from) a history whose earlier turns were edited, and Text /
	// Reasoning / ToolCalls can't keep the order of interleaved blocks. Only set on a complete response; anything
	// that edits the message (keepPartial) must clear it.
	Blocks json.RawMessage `json:"blocks,omitempty"`
}

type Part struct {
	Type string `json:"type"` // text | image
	Text string `json:"text,omitempty"`
	MIME string `json:"mime,omitempty"`
	Data string `json:"data,omitempty"` // base64
}

func TextPart(s string) Part { return Part{Type: "text", Text: s} }

// Thought 是一段思考。Text 给人看；ID / Encrypted 是 Responses 要原样带回的推理 item（Anthropic 的 signature 也放 Encrypted）。
type Thought struct {
	Text      string `json:"text,omitempty"`
	ID        string `json:"id,omitempty"`
	Encrypted string `json:"encrypted,omitempty"`
}

type ToolCall struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Args string `json:"args"` // JSON 原文（模型给的可能不合法，执行时再解）
}

type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type Request struct {
	Model    string
	System   string
	Messages []Message
	Tools    []Tool
	// Effort：思考档位，原样按模型的规矩发（见 Model.Efforts）。空 = 不发，用接口默认。
	Effort string
	// Thinking：思考开关的格式。Chat Completions："deepseek" = thinking:{type}，"" = reasoning_effort；
	// Anthropic: "adaptive" = thinking:{type:adaptive} with a summary of the reasoning (Claude 4.6+); "updates" = the
	// same with the model's short progress notes instead (beta, Opus/Sonnet 5.5, Fable 5+); "" = no thinking param.
	Thinking string
	// CacheKey：前缀缓存的路由键（OpenAI 的 prompt_cache_key）。同一个会话用同一个，命中率高很多。
	CacheKey  string
	MaxTokens int
}

type Usage struct {
	Input     int `json:"input"` // 含缓存命中的部分
	Output    int `json:"output"`
	CacheRead int `json:"cacheRead,omitempty"`
	Reasoning int `json:"reasoning,omitempty"`
}

// Event 是流式片段。
type Event struct {
	Kind string // text | thought | tool（开始调一个工具，Name 有了）| tool_args（参数片段）
	Text string
	// Index：tool / tool_args 是第几个工具调用。
	Index int
	Name  string
}

type Result struct {
	Message Message
	Usage   Usage
	// Stop：end | tool_use | length | cancelled
	Stop string
}

// Client 是一种线路协议。Stream 边收边回调 onEvent，最后给完整的一条 assistant 消息。
// ctx 取消时返回已经收到的部分（Stop=cancelled）和 ctx.Err()。
type Client interface {
	Stream(ctx context.Context, req Request, onEvent func(Event)) (Result, error)
}

// Endpoint 是一个服务商的地址和凭据。
type Endpoint struct {
	BaseURL string            // 到 /v1 为止，比如 https://api.openai.com/v1
	APIKey  string            //nolint:gosec // 只在请求头里用
	Headers map[string]string // 额外的请求头
}

// New 按协议名拿客户端：chat（Chat Completions）| responses | anthropic（Messages）。
func New(api string, ep Endpoint) Client {
	switch api {
	case "responses":
		return &responses{ep: ep}
	case "anthropic":
		return &anthropic{ep: ep}
	}
	return &chat{ep: ep}
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
