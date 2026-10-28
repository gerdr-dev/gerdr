// Package acpx 是 gerdr 在 ACP v1 之上加的那几条扩展方法 —— 两个适配器（claude / codex）
// 和 hub 两头共用这一份定义，线路格式只在这里出现一次。
//
// 为什么要扩展：ACP v1 的核心协议里没有「往进行中的一轮插一句」「回退到某条消息之前」
// 「向人提一组带选项的问题」。插话沿用社区约定的 `_session/steering`（claude-agent-acp /
// codex-acp 两家官方适配器都认这个名字和形状），这样以后换回官方适配器或接别人的 ACP
// agent，hub 这一侧不用改。另外两条是 gerdr 自己的，用 `_gerdr/` 前缀以免和别人撞名。
package acpx

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/coder/acp-go-sdk"
)

const (
	// MethodSteer：client → agent。把一条消息插进正在跑的那一轮。
	MethodSteer = "_session/steering"
	// MethodRewind：client → agent。把对话回退到某条用户消息之前（那条消息和之后的全丢掉）。
	// 只动对话历史，不动文件（gerdr 的回退只回退对话）。
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
	// MethodHolders：client → agent。这个会话此刻是不是还被别的进程开着（比如 herdr / 终端里的交互式 claude）。
	// 两边同时 resume 同一个会话，各写各的，记录文件里就分了叉 —— 谁也看不见对方说过的话（用户报的）。
	// hub 发消息前问一次，有人占着就先不发、让人挑（MethodTakeover）。不支持的 agent 回 method not found = 没人占。
	MethodHolders = "_gerdr/holders"
	// MethodTakeover：client → agent。处理「别处开着」：fork = 另起一份（带着全部历史复制出一个新会话，回新的 sessionId）；
	// kill = 关掉占着它的那些进程（最多等几秒），之后照常接着聊。
	MethodTakeover = "_gerdr/takeover"
	// MethodTranscriptStat：client → agent。这个会话在 agent 自己的记录文件有多大、什么时候改的。gerdr 的对话日志只记它
	// 自己经手的事；别处（herdr / 终端）接着聊过的话，记录文件会长、gerdr 这边不知道 —— 拿它判断要不要从记录重新载入。
	MethodTranscriptStat = "_gerdr/transcript_stat"
	// MethodUsage：client → agent。账号的用量额度（5 小时窗口、每周……用了百分之几、什么时候重置），不挂在哪个会话上。
	// 两边都有结构化的来源，不用解析 /usage 的文字：claude 是 stream-json 的 control_request get_usage，
	// codex 是 app-server 的 account/rateLimits/read。不支持的 agent 回 method not found。
	MethodUsage = "_gerdr/usage"
)

type UsageResponse struct {
	Plan   string       `json:"plan,omitempty"` // 订阅档位，agent 原样报的（team / max / plus / prolite……）
	Limits []UsageLimit `json:"limits"`
}

// UsageLimit：一条额度。Label 是给人看的（界面直接显示），Kind 是 agent 原样的类别，界面按它挑「最要紧的那条」。
type UsageLimit struct {
	Kind       string  `json:"kind"`
	Label      string  `json:"label"`
	Percent    float64 `json:"percent"`
	ResetsAt   int64   `json:"resetsAt,omitempty"`   // unix ms，0 = 不知道
	WindowMins int     `json:"windowMins,omitempty"` // 窗口多长，0 = 不知道
}

type TranscriptStatRequest struct {
	SessionId acp.SessionId `json:"sessionId"`
	Cwd       string        `json:"cwd,omitempty"`
	// Since：从文件的这个位置往后，把人说的话列出来（NewUserTexts）。-1 = 不列。
	Since int64 `json:"since"`
}

type TranscriptStatResponse struct {
	Size    int64 `json:"size"`
	ModTime int64 `json:"modTime"` // unix ms
	// NewUserTexts：Since 之后记录里人说的话。hub 拿去和自己的日志比：全是自己发过的 = 文件是自己写长的
	// （一轮之后的收尾、切模式……），不算「别处聊过」。只看文件大小的话，gerdr 自己一轮刚完就重启（没来得及记大小）
	// 也会被当成别处聊过（用户报的误报）。
	NewUserTexts []string `json:"newUserTexts,omitempty"`
}

type HoldersRequest struct {
	SessionId acp.SessionId `json:"sessionId"`
}

// Holder：一个正开着这个会话的别的进程。
type Holder struct {
	Pid    int    `json:"pid"`
	Name   string `json:"name,omitempty"`
	Status string `json:"status,omitempty"` // idle / busy……（agent 自己报的）
	Cwd    string `json:"cwd,omitempty"`
}

type HoldersResponse struct {
	Holders []Holder `json:"holders"`
}

type TakeoverRequest struct {
	SessionId acp.SessionId `json:"sessionId"`
	Action    string        `json:"action"` // fork | kill
}

type TakeoverResponse struct {
	SessionId acp.SessionId `json:"sessionId"` // fork 之后是新的
}

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
	// Now：立即插（截断正在生成的这次，马上处理）。默认（false）照 Claude Code 自己的输入框：排到下一个空档
	// （当前这步工具跑完）再塞进去，不打断。不分优先级的 agent（codex 的 turn/steer）忽略它。
	Now  bool           `json:"now,omitempty"`
	Meta map[string]any `json:"_meta,omitempty"`
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
	// Fork：新会话还只是个打算（claude 要等下一句话写进去才有记录）时，它从哪儿来。client 要存下来，
	// 接回这个会话（ResumeSession / LoadSession）时放在 _meta.gerdr.fork 里带回去：agent 进程重启过的话，
	// 它自己记的早没了，不带回去就会起一个空会话、前面的对话全丢（踩过：回退后撤回、gerdr 一重启，claude 说「这是第一条消息」）。
	Fork *PendingFork `json:"fork,omitempty"`
}

// PendingFork：回退出来、还没说过话的会话从哪儿来。From 为空 = 回退到了第一条，就是个全新会话。
type PendingFork struct {
	From string `json:"from,omitempty"`
	At   string `json:"at,omitempty"`
}

// GetPendingFork 读 _meta.gerdr.fork（经过一趟 JSON，到这边是 map）。
func GetPendingFork(meta map[string]any) *PendingFork {
	v, ok := GetMeta(meta, "fork")
	if !ok || v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var f PendingFork
	if json.Unmarshal(b, &f) != nil {
		return nil
	}
	return &f
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
// AskDeclined：没回答时给 agent 的话（两家适配器共用）。要说清楚是人的选择、不是界面故障，并告诉它接下来怎么做 ——
// 只说「被关掉了」的话，agent 会猜成「界面不支持这个工具」、换着法子一遍遍再问（用户报的）。
func AskDeclined(reason, text string) string {
	switch reason {
	case "replied":
		// 原话直接放进来：光说「下一条消息是回答」的话，agent 收到这句就立刻接着说话（「还没收到你的回答」），
		// 插话要晚一步才到（实测）
		if text != "" {
			return "The user did not pick an option; they answered in the chat instead: \"" + text + "\". Treat that as their answer to this question and continue. (The same text also arrives as their next chat message — no need to acknowledge it twice.) Do not ask this question again."
		}
		return "The user did not pick an option; they replied in the chat instead. Their next message is their answer to this question — read it and continue. Do not ask this question again."
	case "skipped":
		return "The user deliberately chose not to answer this question (they clicked \"skip\" in the gerdr UI; the question was displayed fine — this is not a UI problem). Do not ask it again with the question tool. Continue with your best judgment, or ask in plain text in your reply if you really need the answer."
	}
	return "The question was withdrawn because the turn was interrupted. Do not re-ask it unless the user brings it up."
}

type AskResponse struct {
	Answers   map[string][]string `json:"answers,omitempty"`
	Cancelled bool                `json:"cancelled,omitempty"`
	// Reason：为什么没回答。光一个 cancelled 的话 agent 分不清「人不想选」和「界面坏了」，会猜成后者、
	// 还一遍遍再问（用户报的）。适配器按它给 agent 说不同的话。
	//   skipped：人在卡片上点了「不回答」（主动的）；
	//   replied：人没选选项，直接在对话里说了话 —— 他接下来那条消息就是答复（照 Claude Code：提问挂着时打字 = 回答）；
	//   空：这一轮结束了 / 被打断了，问题作废。
	Reason string `json:"reason,omitempty"`
	Text   string `json:"text,omitempty"` // reason=replied 时：人在对话里说的那句话
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

// DescribeRequest：把一个在等人处理的请求说成一句话（会话列表的 waiting、`gerdr session read`、手环上显示的就是它）。
// 只说「在等人回答问题」的话，看的人 / agent 不知道问的是什么、该不该现在去处理（用户报的）。
//
//	permission → 「审批：<工具标题>」
//	ask        → 「回答：<第一道题>（<选项 / 选项>）」，多道题后面带「等 N 道题」
func DescribeRequest(kind string, raw json.RawMessage) string {
	switch kind {
	case "permission":
		var r struct {
			ToolCall struct {
				Title string `json:"title"`
			} `json:"toolCall"`
		}
		_ = json.Unmarshal(raw, &r)
		if r.ToolCall.Title == "" {
			return "审批"
		}
		return "审批：" + r.ToolCall.Title
	case "ask":
		var r AskRequest
		_ = json.Unmarshal(raw, &r)
		if len(r.Questions) == 0 {
			return "回答问题"
		}
		q := r.Questions[0]
		s := "回答：" + q.Question
		var opts []string
		for _, o := range q.Options {
			opts = append(opts, o.Label)
		}
		if len(opts) > 0 {
			s += "（" + strings.Join(opts, " / ") + "）"
		}
		if n := len(r.Questions); n > 1 {
			s += fmt.Sprintf(" 等 %d 道题", n)
		}
		return s
	}
	return kind
}

// MediaHint：起 agent 时追加给它的一句说明（claude 的 --append-system-prompt、codex 的 developerInstructions）——
// gerdr 的对话界面会把 Markdown 图片语法引用的本机文件直接画出来（ui 的 ChatMedia：图片、视频、音频、PDF），
// 但 agent 不知道的话只会甩一条路径让人自己去开。照 shuttle 的写法。
const MediaHint = "You are running inside gerdr, a GUI for coding agents. Its chat view renders Markdown image syntax inline: " +
	"when the user should see or hear a local image, video, audio file or PDF (a screenshot, a generated or downloaded image/video, a QR code, a voice-over…), " +
	"embed it in your reply with image syntax and an absolute path, e.g. ![login QR code](/Users/me/project/out/qr.png) " +
	"(wrap the path in <> if it contains spaces: ![demo](</Users/me/my dir/demo.mp4>)). Several images on consecutive lines show as a grid. " +
	"Use a plain link instead when the file is just being mentioned, not shown."
