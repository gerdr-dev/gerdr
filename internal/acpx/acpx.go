// Package acpx 是 gerdr 在 ACP v1 之上加的那几条扩展方法 —— 两个适配器（claude / codex）
// 和 hub 两头共用这一份定义，线路格式只在这里出现一次。
//
// 为什么要扩展：ACP v1 的核心协议里没有「往进行中的一轮插一句」「回退到某条消息之前」
// 「向人提一组带选项的问题」。插话沿用社区约定的 `_session/steering`（claude-agent-acp /
// codex-acp 两家官方适配器都认这个名字和形状），这样以后换回官方适配器或接别人的 ACP
// agent，hub 这一侧不用改。另外两条是 gerdr 自己的，用 `_gerdr/` 前缀以免和别人撞名。
package acpx

import "github.com/coder/acp-go-sdk"

const (
	// MethodSteer：client → agent。把一条消息插进正在跑的那一轮。
	MethodSteer = "_session/steering"
	// MethodRewind：client → agent。把对话回退到某条用户消息之前（那条消息和之后的全丢掉）。
	// 只动对话历史，不动文件 —— 文件由 hub 用影子 git 快照统一恢复（两家 agent 自带的
	// 文件回退都靠不住：claude 的 checkpoint 不跟踪 Bash 改的文件，codex 压根没有）。
	MethodRewind = "_gerdr/rewind"
	// MethodAsk：agent → client。向人提一组问题（claude 的 AskUserQuestion、codex 的
	// requestUserInput 都汇到这一条），client 回答案。
	MethodAsk = "_gerdr/ask"
	// MethodWithdraw：client → agent。收回还没被 agent 处理的插话（按 messageId），回真收回了的那些。
	// 按 Esc 时用：和 Claude Code 一样，排着队还没轮到的那句放回输入框。agent 不支持（codex 的 turn/steer
	// 一发就进了这一轮）就回 method not found，client 当作一条都没收回。
	MethodWithdraw = "_gerdr/withdraw"
	// MethodWarm：client → agent。把会话的进程先起来（不发消息）。claude 的斜杠命令列表是进程起来时才报的，
	// 输入框里打 / 时还没起就先起。不需要的 agent 回 method not found。
	MethodWarm = "_gerdr/warm"
	// MethodTaskStop：client → agent。停掉一个后台任务。
	MethodTaskStop = "_gerdr/task_stop"
	// MethodTaskBackground：client → agent。把一个正在前台跑的工具调用转到后台（claude 的 ctrl+b）。
	MethodTaskBackground = "_gerdr/task_background"
	// MethodAllowRules：client → agent。把几条权限规则加进 agent 的「允许」名单（claude：项目的
	// .claude/settings.local.json），给 auto 模式下被分类器拦掉的调用用 —— 规则从工具调用的
	// _meta.gerdr.autoDenied 来。
	MethodAllowRules = "_gerdr/allow_rules"
)

// AutoDeniedKey：工具调用的 _meta.gerdr.autoDenied = []string，这次调用被 auto 模式的分类器拒了，
// 加上这些规则就能放行它。
const AutoDeniedKey = "autoDenied"

type AllowRulesRequest struct {
	SessionId acp.SessionId `json:"sessionId"`
	Rules     []string      `json:"rules"`
}

// 后台任务和「agent 自己开的一轮」走 session_info_update 的 _meta.gerdr（和别的 update 同一条
// 有序的通道，不会和正文乱序）：
//   - tasks：[]Task，整份替换；
//   - autoTurn：true / false —— agent 在没有 session/prompt 的时候自己跑起来 / 停下来了
//     （claude 后台任务跑完会这样）。
const AutoTurnKey = "autoTurn"

type Task struct {
	ID          string `json:"id"`
	Type        string `json:"type"` // local_bash | local_agent | …（agent 原样报的）
	Description string `json:"description"`
	Subagent    string `json:"subagent,omitempty"`
	ToolCallID  string `json:"toolCallId,omitempty"`
	// Parent：这个任务是某个子 agent 起的（值 = 那个子 agent 的 toolCallId）。界面上挂在它下面，别看着像主 agent 自己在跑。
	Parent     string `json:"parent,omitempty"`
	Background bool   `json:"background"`
	Status     string `json:"status"` // running | completed | failed | killed …
	Progress   string `json:"progress,omitempty"`
	ToolUses   int    `json:"toolUses,omitempty"`
	Summary    string `json:"summary,omitempty"`
	StartedAt  int64  `json:"startedAt"`
	EndedAt    int64  `json:"endedAt,omitempty"`
}

type WithdrawRequest struct {
	SessionId  acp.SessionId `json:"sessionId"`
	MessageIds []string      `json:"messageIds"`
}

type WithdrawResponse struct {
	Withdrawn []string `json:"withdrawn"`
}

type TaskStopRequest struct {
	SessionId acp.SessionId `json:"sessionId"`
	TaskId    string        `json:"taskId"`
}

type TaskBackgroundRequest struct {
	SessionId  acp.SessionId `json:"sessionId"`
	ToolCallId string        `json:"toolCallId"`
}

type TaskBackgroundResponse struct {
	Backgrounded bool `json:"backgrounded"`
}

// SteerRequest 和官方适配器的 `_session/steering` 同形。
type SteerRequest struct {
	SessionId acp.SessionId      `json:"sessionId"`
	Prompt    []acp.ContentBlock `json:"prompt"`
	MessageId string             `json:"messageId,omitempty"`
	Meta      map[string]any     `json:"_meta,omitempty"`
}

// SteerResponse.Outcome：
//   - injected：插进了正在跑的那一轮；
//   - promptRequired：此刻没有在跑的一轮，消息没收下，client 自己改发 session/prompt。
//
// gerdr 的适配器永远走 promptRequired 那条（不替 client 偷偷开一轮 —— 那一轮没有
// session/prompt 的响应，client 拿不到它的结束信号）。
type SteerResponse struct {
	Outcome string `json:"outcome"`
}

const (
	SteerInjected       = "injected"
	SteerPromptRequired = "promptRequired"
)

type RewindRequest struct {
	SessionId acp.SessionId `json:"sessionId"`
	// MessageId 是当初 session/prompt 带的那个 messageId。回退之后它和它之后的全部消失。
	MessageId string `json:"messageId"`
}

type RewindResponse struct {
	// SessionId：回退之后的会话 id。codex 是就地截断（不变）；claude 是从截断点分叉出一个
	// 新会话（变了），client 要换成新 id 继续用。
	SessionId acp.SessionId `json:"sessionId"`
}

type AskOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

type AskQuestion struct {
	Id          string      `json:"id"`
	Header      string      `json:"header,omitempty"`
	Question    string      `json:"question"`
	Options     []AskOption `json:"options,omitempty"`
	MultiSelect bool        `json:"multiSelect,omitempty"`
	// AllowOther：能不能自己写一个答案（不在选项里的）。
	AllowOther bool `json:"allowOther,omitempty"`
	Secret     bool `json:"secret,omitempty"`
}

type AskRequest struct {
	SessionId  acp.SessionId `json:"sessionId"`
	ToolCallId string        `json:"toolCallId,omitempty"`
	Questions  []AskQuestion `json:"questions"`
}

// AskResponse：Answers 按问题 id 给，每题一组（单选就一个）。Cancelled 为真表示人不答，
// agent 那边按「工具被中止」处理。
type AskResponse struct {
	Answers   map[string][]string `json:"answers,omitempty"`
	Cancelled bool                `json:"cancelled,omitempty"`
}

// MetaKey 是 gerdr 往 ACP `_meta` 里塞东西时用的命名空间。
const MetaKey = "gerdr"

// Meta 拼一个 `{"gerdr": {...}}`。
func Meta(kv map[string]any) map[string]any { return map[string]any{MetaKey: kv} }

// GetMeta 读 `_meta.gerdr.<key>`。
func GetMeta(meta map[string]any, key string) (any, bool) {
	g, ok := meta[MetaKey].(map[string]any)
	if !ok {
		return nil, false
	}
	v, ok := g[key]
	return v, ok
}
