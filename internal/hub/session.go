package hub

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/coder/acp-go-sdk"
	"github.com/google/uuid"

	"github.com/gerdr-dev/gerdr/internal/acpx"
	"github.com/gerdr-dev/gerdr/internal/agenthost"
)

// Record 是落盘的那部分（sessions/index.json 里一条）。
type Record struct {
	ID             string `json:"id"`
	Agent          string `json:"agent"`
	AgentSessionID string `json:"agentSessionId"`
	Cwd            string `json:"cwd"`
	Title          string `json:"title,omitempty"`
	CustomTitle    bool   `json:"customTitle,omitempty"`
	// 自动标题的状态（title.go）。TitleStale: legacy — /clear used to keep the old title until the next message
	// replaced it; it now clears the title outright, but records saved before that may still carry the flag.
	// TitleKey：默认逻辑在哪段上下文的哪一档起过名了；PluginTitleFor：插件在哪段上下文里起过名（这段里默认逻辑让开）。
	TitleStale     bool              `json:"titleStale,omitempty"`
	TitleKey       string            `json:"titleKey,omitempty"`
	PluginTitleFor string            `json:"pluginTitleFor,omitempty"`
	CreatedAt      int64             `json:"createdAt"`
	UpdatedAt      int64             `json:"updatedAt"`
	Mode           string            `json:"mode,omitempty"`
	Config         map[string]string `json:"config,omitempty"`
	// SyncedSize：gerdr 最后一次和 agent 的记录文件对齐时，那个文件有多大（catchUp）。比它大 = 别处接着聊过。
	SyncedSize int64 `json:"syncedSize,omitempty"`
	// Fork：回退出来、agent 还没写出记录的会话从哪儿来（acpx.RewindResponse.Fork）。接回会话时带给 agent：
	// 它重启过的话自己记的就没了，起成一个空会话，前面的对话全丢。换 id 时清掉（rekey）；agent 那边记录有了就不再理它。
	Fork *acpx.PendingFork `json:"fork,omitempty"`
	// Cleared: legacy. Indexes written before /clear split the old conversation into its own session (clear.go) list
	// the agent session ids this tab had before each /clear here; migrateCleared turns them into sessions on load.
	Cleared []ClearedCtx `json:"cleared,omitempty"`
	// CtxTokens / CtxWindow: how full the context is (the "上下文 37%" under the composer). Saved with the record because
	// adapters only learn them from a request: kept in memory, a restarted gerdr showed nothing until the next turn,
	// and claude only reports the window at the end of a turn, so not even during the first one.
	CtxTokens int `json:"ctxTokens,omitempty"`
	CtxWindow int `json:"ctxWindow,omitempty"`
}

// ClearedCtx is one context dropped by /clear: its agent session id and what it was called at the time.
type ClearedCtx struct {
	ID    string `json:"id"`
	Title string `json:"title,omitempty"`
	At    int64  `json:"at"`
}

// 会话状态（给侧栏和提示用）。
const (
	StIdle     = "idle"
	StRunning  = "running"
	StWaiting  = "waiting" // 等人：审批 / 提问
	StError    = "error"
	StStarting = "starting"
)

type Session struct {
	h  *Hub
	lf logFile

	mu          sync.Mutex
	escMu       sync.Mutex // Escape 一次只跑一个：连按几下 Esc 的话，并发的几次会各自把同一句话还回去（输入框里叠好几遍，踩过）
	rec         Record
	events      []*Event
	attached    bool // agent 那边接上了（gerdr 重启后要先 ResumeSession）
	status      string
	running     bool
	catchingUp  bool
	adopting    bool              // 在等 gerdr 重启前没收尾的那一轮（adoptOrphanTurn），CatchUp 别插手
	orphanSince int64             // 载入时日志里有一轮没收尾：最后一条日志的时间（只在 New 里用一次）
	steering    map[string]string // 正在插的话（msgID → 早到的队列状态），见 SendFrom
	lastCatch   time.Time         // 正在从 agent 记录重新载入（CatchUp / Reload），别重入
	lastErr     string
	modes       *acp.SessionModeState
	config      []acp.SessionConfigOption
	commands    []acp.AvailableCommand
	pending     map[string]*pending
	turnMsg     string // 正在跑的那一轮是哪条消息开的
	auto        bool   // 正在跑的这一轮是 agent 自己开的（后台任务跑完了在汇报），没有 runTurn 在等
	// autoNext：「agent 自己开了一轮」先于上一轮 session/prompt 的响应到了（两条路，顺序不保证）。
	// runTurn 收尾时看到它就直接接上一个 auto 的轮次，不然这一轮在界面上是「闲着」。
	autoNext bool
	// closeAfter：会话自己 `gerdr session close` 了自己（用户叫它「做完就关掉 pane」）。当场关会把它正在跑的这一轮
	// （就是发出这条命令的这一轮）掐掉，收尾的话说不出来；所以记下来，这一轮跑完再关（Hub.CloseAfterTurn）
	closeAfter  bool
	tasks       []acpx.Task
	turnStarted int64                // 这一轮几点开始的（状态行：跑了多久）
	turnTokens  int                  // 这一轮输出了多少 token（状态行）
	sending     map[string]bool      // 正在处理的 client 消息 id（同一条重发时挡住并发的那次）
	retryCancel chan struct{}        // 自动重试正在等：关掉它 = 人按了停止
	shut        bool                 // Hub.Shutdown 关掉的（关了 pane）：这一轮因为进程被杀报的错不算错、不自动重试；再接上时清掉
	shells      map[string]*shellRun // 输入框里 `!命令` 还在跑的（shell.go）
	attachMu    sync.Mutex
}

type pending struct {
	kind string // permission | ask
	ch   chan json.RawMessage
	req  json.RawMessage // 原样的请求（GET /api/pending 给外面的程序看）
	at   int64
}

func (s *Session) waitingLocked() string {
	var first *pending
	for _, p := range s.pending {
		if first == nil || p.at < first.at {
			first = p
		}
	}
	if first == nil {
		return ""
	}
	w := acpx.DescribeRequest(first.kind, first.req)
	if n := len(s.pending); n > 1 {
		w += fmt.Sprintf("（还有 %d 个）", n-1)
	}
	return w
}

// PendingReq：一个在等人处理的审批 / 提问（GET /api/pending）。
type PendingReq struct {
	SessionID string          `json:"sessionId"`
	Title     string          `json:"title,omitempty"`
	Agent     string          `json:"agent"`
	Cwd       string          `json:"cwd"`
	ReqID     string          `json:"reqId"`
	Kind      string          `json:"kind"` // permission | ask
	Request   json.RawMessage `json:"request"`
	Summary   string          `json:"summary"` // 一句话（acpx.DescribeRequest）
	At        int64           `json:"at"`      // unix ms
}

// Pending：这个会话里在等人处理的请求，早的在前。
func (s *Session) Pending() []PendingReq {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []PendingReq{}
	for id, p := range s.pending {
		out = append(out, PendingReq{SessionID: s.rec.ID, Title: s.rec.Title, Agent: s.rec.Agent, Cwd: s.rec.Cwd, ReqID: id, Kind: p.kind, Request: p.req, Summary: acpx.DescribeRequest(p.kind, p.req), At: p.at})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At < out[j].At })
	return out
}

func newSession(h *Hub, r Record) *Session {
	return &Session{h: h, rec: r, status: StIdle, pending: map[string]*pending{}, steering: map[string]string{},
		lf: logFile{path: filepath.Join(h.opt.Dir, "sessions", r.ID+".jsonl")}}
}

func newID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// recoverAfterRestart：上次 gerdr 退出时还在跑的那一轮，日志里没有结尾 —— 先补一个「中断」。
// 但那一轮**不一定**真断了：gerdr 被强杀 / 崩溃时来不及杀 claude，claude 只看到 stdin 断了，会把手上这一轮
// 跑完再退（实测：gerdr 重启后它又跑了一分钟，回复写进了自己的记录，gerdr 这边只剩半句 + 「中断」）。
// 所以返回日志里最后一条的时间（没有没收尾的轮次 = 0），由 adoptOrphanTurn 去核对、追回来。
func (s *Session) recoverAfterRestart() (openSince int64) {
	open := false
	for _, e := range s.events {
		switch e.Kind {
		case EvTurnStart:
			open = true
		case EvTurnEnd:
			open = false
		}
	}
	if open {
		openSince = s.events[len(s.events)-1].TS
		s.events = append(s.events, &Event{Seq: s.h.nextSeqLocked(), TS: now(), Kind: EvTurnEnd, StopReason: "cancelled", Error: "gerdr 重启，这一轮被中断了"})
	}
	// 还在跑的 `!命令`：进程跟着 gerdr 没了（或者成了没人管的孤儿），补一个结束，卡片别一直转
	shellDone := map[string]bool{}
	for _, e := range s.events {
		if e.Kind == EvShellDone {
			shellDone[e.MessageID] = true
		}
	}
	for _, e := range s.events {
		if e.Kind == EvShell && !shellDone[e.MessageID] {
			s.events = append(s.events, &Event{Seq: s.h.nextSeqLocked(), TS: now(), Kind: EvShellDone, MessageID: e.MessageID, Level: "lost"})
		}
	}
	// 没回答的审批 / 提问也一样作废。
	answered := map[string]bool{}
	for _, e := range s.events {
		if e.Kind == EvPermissionDone || e.Kind == EvAskDone {
			answered[e.ReqID] = true
		}
	}
	for _, e := range s.events {
		if (e.Kind == EvPermission || e.Kind == EvAsk) && !answered[e.ReqID] {
			kind := EvPermissionDone
			if e.Kind == EvAsk {
				kind = EvAskDone
			}
			s.events = append(s.events, &Event{Seq: s.h.nextSeqLocked(), TS: now(), Kind: kind, ReqID: e.ReqID, Response: json.RawMessage(`{"cancelled":true}`)})
		}
	}
	return openSince
}

func (h *Hub) nextSeqLocked() int64 {
	h.seq++
	return h.seq
}

// Summary 是侧栏 / 面板一览要的那点东西。
type Summary struct {
	Record
	Status   string                    `json:"status"`
	Error    string                    `json:"error,omitempty"`
	Modes    *acp.SessionModeState     `json:"modes,omitempty"`
	Config   []acp.SessionConfigOption `json:"configOptions,omitempty"`
	Commands []acp.AvailableCommand    `json:"commands,omitempty"`
	// CommandsCached：Commands 是借的缓存（这个会话的 agent 进程还没起，用这个 agent 上次报的）。
	// 缓存可能是旧版写的（升级 gerdr 后适配器多接了命令、claude 多了 skill），界面打 / 时照样先把进程起起来拿新的。
	CommandsCached bool         `json:"commandsCached,omitempty"`
	Pending        int          `json:"pending"`
	LastSeq        int64        `json:"lastSeq"`
	Tasks          []acpx.Task  `json:"tasks,omitempty"`
	Turn           *TurnInfo    `json:"turn,omitempty"`    // 在跑的这一轮（没在跑是 nil）
	Context        *ContextInfo `json:"context,omitempty"` // 上下文用量（agent 报过才有）
	Base           string       `json:"base,omitempty"`    // agent 实际是哪一种（变体的底子；界面上按它认图标和功能）
	Branch         string       `json:"branch,omitempty"`  // cwd 所在 git 仓库的当前分支（pane 顶部）
	// Waiting：在等人处理什么，一句话（最早的那个请求；acpx.DescribeRequest）。会话列表上一眼看得出「问的是什么」，
	// 手环这种只轮列表的不用再逐个去读日志
	Waiting string `json:"waiting,omitempty"`
}

type ContextInfo struct {
	Tokens int `json:"tokens"`
	Window int `json:"window"`
}

type TurnInfo struct {
	StartedAt    int64 `json:"startedAt"`
	OutputTokens int   `json:"outputTokens"`
}

func (s *Session) contextLocked() *ContextInfo {
	if s.rec.CtxTokens == 0 || s.rec.CtxWindow == 0 {
		return nil
	}
	return &ContextInfo{Tokens: s.rec.CtxTokens, Window: s.rec.CtxWindow}
}

func (s *Session) turnInfoLocked() *TurnInfo {
	if !s.running {
		return nil
	}
	return &TurnInfo{StartedAt: s.turnStarted, OutputTokens: s.turnTokens}
}

func (s *Session) Summary() Summary {
	s.mu.Lock()
	defer s.mu.Unlock()
	var last int64
	if n := len(s.events); n > 0 {
		last = s.events[n-1].Seq
	}
	cmds, cached := s.commands, false
	if len(cmds) == 0 && s.h.cmds != nil {
		cached = true
		cmds = s.h.cmds.get(s.rec.Agent) // 这个会话的进程还没起：用这个 agent 上次报的
		if len(cmds) == 0 {
			cmds = s.h.cmds.get(s.h.Base(s.rec.Agent)) // 新建的变体还没报过：先借原版的
		}
	}
	modes, config := s.modes, s.config
	if (modes == nil || len(config) == 0) && s.h.opts != nil {
		modes, config = s.optsFallbackLocked(modes, config)
	}
	return Summary{Record: s.rec, Status: s.status, Error: s.lastErr, Modes: modes, Config: config, Commands: cmds, CommandsCached: cached && len(cmds) > 0, Pending: len(s.pending), LastSeq: last, Tasks: s.tasks, Turn: s.turnInfoLocked(), Context: s.contextLocked(), Base: s.h.Base(s.rec.Agent), Branch: gitBranch(s.rec.Cwd), Waiting: s.waitingLocked()}
}

func (s *Session) ID() string { return s.rec.ID }

// Events 返回整份日志的拷贝。
func (s *Session) Events() []*Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Event, len(s.events))
	copy(out, s.events)
	return out
}

// emit 记一条日志并广播。merge=true 时尝试并进上一条流式片段（只并内存里、还没落盘的）。
func (s *Session) emit(e *Event) {
	e.Seq = s.h.nextSeq()
	e.TS = now()
	s.mu.Lock()
	merged := false
	if e.Kind == EvUpdate {
		if key, text, ok := chunkKey(e.Update); ok {
			if n := len(s.events); n > 0 && n > s.lf.flushed {
				last := s.events[n-1]
				if last.Kind == EvUpdate {
					if k2, _, ok2 := chunkKey(last.Update); ok2 && k2 == key {
						// 合并后的那条换成新对象：广播出去的旧指针可能还在别的 goroutine 里被序列化。
						cp := *last
						cp.Update = mergeChunk(last.Update, text)
						cp.Seq = e.Seq
						s.events[n-1] = &cp
						merged = true
					}
				}
			}
		}
	}
	if !merged {
		s.events = append(s.events, e)
	}
	s.rec.UpdatedAt = e.TS
	s.mu.Unlock()
	s.h.broadcastEvents(s.rec.ID, []*Event{e}, false)
}

// flush 把还没落盘的日志写下去。force=false 时最后一条流式片段先不写（它可能还会长）。
func (s *Session) flush(force bool) {
	s.mu.Lock()
	n := len(s.events)
	if !force && n > 0 && s.events[n-1].Kind == EvUpdate {
		if _, _, ok := chunkKey(s.events[n-1].Update); ok {
			n--
		}
	}
	if n <= s.lf.flushed {
		s.mu.Unlock()
		return
	}
	batch := append([]*Event(nil), s.events[s.lf.flushed:n]...)
	from := s.lf.flushed
	s.lf.flushed = n
	s.mu.Unlock()
	if err := s.lf.append(batch); err != nil {
		s.h.log.Warn("write session log", "id", s.rec.ID, "err", err)
		s.mu.Lock()
		if s.lf.flushed == n {
			s.lf.flushed = from
		}
		s.mu.Unlock()
	}
}

func (s *Session) setStatus() {
	s.mu.Lock()
	st := StIdle
	switch {
	case len(s.pending) > 0:
		st = StWaiting
	case s.running:
		st = StRunning
	case s.lastErr != "":
		st = StError
	}
	changed := st != s.status
	s.status = st
	s.mu.Unlock()
	if changed {
		s.h.broadcastSession(s)
	}
}

// ensureAttached：gerdr 重启后（或 agent 进程换过之后）先把会话接回 agent 那边。
func (s *Session) ensureAttached(ctx context.Context) (*agenthost.Host, error) {
	c, err := s.h.host(ctx, s.rec.Agent)
	if err != nil {
		return nil, err
	}
	s.attachMu.Lock()
	defer s.attachMu.Unlock()
	s.mu.Lock()
	ok := s.attached
	rec := s.rec
	s.mu.Unlock()
	if ok {
		return c, nil
	}
	meta := map[string]any{}
	if rec.Mode != "" {
		meta["mode"] = rec.Mode
	}
	for k, v := range rec.Config {
		meta[k] = v
	}
	if rec.Fork != nil {
		meta["fork"] = rec.Fork
	}
	res, err := c.Conn.ResumeSession(ctx, acp.ResumeSessionRequest{SessionId: acp.SessionId(rec.AgentSessionID), Cwd: rec.Cwd, McpServers: []acp.McpServer{}, Meta: acpx.Meta(meta)})
	if err != nil {
		// 一句话都没说过的会话，agent 那边多半压根没落盘（claude / codex 都是第一条消息才写），
		// 接不回来很正常：换一个新的 agent 会话顶上，人看不出区别。
		if !s.hasUserMessages() {
			nres, nerr := c.Conn.NewSession(ctx, acp.NewSessionRequest{Cwd: rec.Cwd, McpServers: []acp.McpServer{}, Meta: acpx.Meta(meta)})
			if nerr == nil {
				s.h.rekey(s, string(nres.SessionId))
				s.mu.Lock()
				s.attached, s.shut = true, false
				s.modes, s.config = nres.Modes, nres.ConfigOptions
				s.h.opts.set(s.rec.Agent, s.modes, s.config)
				s.mu.Unlock()
				s.syncRecFromState()
				s.h.broadcastSession(s)
				return c, nil
			}
		}
		return nil, fmt.Errorf("接回会话失败：%s", errText(err))
	}
	s.mu.Lock()
	s.attached, s.shut = true, false
	s.modes, s.config = res.Modes, res.ConfigOptions
	s.h.opts.set(s.rec.Agent, s.modes, s.config)
	s.mu.Unlock()
	s.syncRecFromState()
	s.h.broadcastSession(s)
	return c, nil
}

func (s *Session) hasUserMessages() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.events {
		if e.Kind == EvUser {
			return true
		}
	}
	return false
}

// syncRecFromState 把当前模式 / 配置记进 Record（下次接回时原样带上）。
// optsFallbackLocked：还没接上 agent 的会话，下拉框先按这类 agent 上次报的选项画（optCache），当前值用自己的记录。
func (s *Session) optsFallbackLocked(modes *acp.SessionModeState, config []acp.SessionConfigOption) (*acp.SessionModeState, []acp.SessionConfigOption) {
	o, ok := s.h.opts.get(s.rec.Agent)
	if !ok {
		o, ok = s.h.opts.get(s.h.Base(s.rec.Agent))
	}
	if !ok {
		return modes, config
	}
	if modes == nil && o.Modes != nil {
		modes = o.Modes
		if s.rec.Mode != "" {
			modes.CurrentModeId = acp.SessionModeId(s.rec.Mode)
		}
	}
	if len(config) == 0 {
		config = o.Config
		for _, c := range config {
			if c.Select != nil {
				// 空值 = 没手选过、用 agent 的默认（旧版存下的「auto」就是空），别拿它盖掉缓存里那个实际值
				if v, ok := s.rec.Config[string(c.Select.Id)]; ok && v != "" {
					c.Select.CurrentValue = acp.SessionConfigValueId(v)
				}
			}
		}
	}
	return modes, config
}

func (s *Session) syncRecFromState() {
	s.mu.Lock()
	if s.modes != nil {
		s.rec.Mode = string(s.modes.CurrentModeId)
	}
	if len(s.config) > 0 {
		if s.rec.Config == nil {
			s.rec.Config = map[string]string{}
		}
		for _, o := range s.config {
			if o.Select != nil {
				s.rec.Config[string(o.Select.Id)] = string(o.Select.CurrentValue)
			}
		}
	}
	s.mu.Unlock()
}

// ---- 说话 ----

type SendResult struct {
	MessageID string `json:"messageId"`
	Steered   bool   `json:"steered"`
}

// Send 发一条消息：有一轮在跑就插进去（steer），没有就开一轮。这正是 TUI 那边做不好的事：
// 人不用管 agent 此刻忙不忙，说就是了。
// Send：clientID 是 client 自己生成的消息 id（UUID）。网络不好时 client 会拿同一个 id 重发 ——
// 第一次其实已经到了、只是响应丢了 —— 这里认出来直接回上次的结果，不会发出两条。
func (s *Session) Send(ctx context.Context, content []acp.ContentBlock, clientID string) (SendResult, error) {
	return s.SendFrom(ctx, content, clientID, "", false)
}

// SendFrom：from 是发送方的会话 id（另一个 agent 经 `gerdr session send` 发来的），记在日志里给界面标出来。
// urgent：在跑时立即插话（截断当前这次生成），见 acpx.SteerRequest.Now；不在跑时没区别。
func (s *Session) SendFrom(ctx context.Context, content []acp.ContentBlock, clientID, from string, urgent bool) (SendResult, error) {
	msgID := uuid.NewString()
	if _, err := uuid.Parse(clientID); err == nil {
		msgID = clientID
		s.mu.Lock()
		if s.sending[msgID] {
			s.mu.Unlock()
			return SendResult{}, errors.New("这条消息正在发送")
		}
		for _, e := range s.events {
			if e.Kind == EvUser && e.MessageID == msgID {
				s.mu.Unlock()
				return SendResult{MessageID: msgID, Steered: e.Steer}, nil
			}
		}
		if s.sending == nil {
			s.sending = map[string]bool{}
		}
		s.sending[msgID] = true
		s.mu.Unlock()
		defer func() {
			s.mu.Lock()
			delete(s.sending, msgID)
			s.mu.Unlock()
		}()
	}
	c, err := s.ensureAttached(ctx)
	if err != nil {
		// codex：别处（终端 / Codex App）开着这个 thread 时接不回来 —— 它自己防住了分叉，但光秃秃一句英文报错人不知道怎么办
		if strings.Contains(err.Error(), "active writer") {
			return SendResult{}, &HeldError{Holders: []acpx.Holder{{Name: "另一个 Codex（终端或 Codex App）"}}}
		}
		return SendResult{}, err
	}
	// 别处（herdr / 终端里的交互式 claude）还开着这个会话：先不发，让人挑（另起一份 / 关掉那边）。
	// 在记下这条消息之前拦 —— 处理完 client 拿同一个 id 重发，对话里不会多出一条。
	if hs := s.heldElsewhere(ctx, c); len(hs) > 0 {
		return SendResult{}, &HeldError{Holders: hs}
	}
	s.mu.Lock()
	running := s.running
	agentSID := s.rec.AgentSessionID
	var asks []string
	for id, p := range s.pending {
		if p.kind == "ask" {
			asks = append(asks, id)
		}
	}
	s.mu.Unlock()
	// 提问还挂着，人却直接打字说话了：照 Claude Code，这句话就是回答 —— 先把提问收掉（告诉 agent「他直接在对话里回了」），
	// 再把话插进去。不收的话 agent 一直卡在等提问，插进去的话要等它超时 / 被关掉才轮得到。
	if len(asks) > 0 {
		var said []string
		for _, b := range content {
			if b.Text != nil {
				said = append(said, b.Text.Text)
			}
		}
		resp, _ := json.Marshal(map[string]any{"cancelled": true, "reason": "replied", "text": strings.Join(said, "\n")})
		for _, id := range asks {
			_ = s.Answer(id, resp)
		}
	}

	// 人在这条之前用 `!` 跑过的命令：输出接在最前面一起发过去（shell.go）。日志里记的也是发出去的原样 ——
	// agent 的记录里就是这样，回头比对（CatchUp）、重新载入才对得上；界面认出这几块，不画成人说的话
	s.mu.Lock()
	content = withShell(s.pendingShellLocked(), content)
	s.mu.Unlock()
	raw := make([]json.RawMessage, 0, len(content))
	for _, b := range content {
		j, _ := json.Marshal(b)
		raw = append(raw, j)
	}

	if running && c.Steering() {
		// 插话的「排队中」状态（command_lifecycle → steer_state）常常比 MethodSteer 的返回先到 —— 那时这条消息还没记进日志。
		// 先登记，早到的状态暂存在这儿，记日志之后补发（见下面和 onGerdrInfo）
		s.mu.Lock()
		s.steering[msgID] = ""
		s.mu.Unlock()
		defer func() {
			s.mu.Lock()
			delete(s.steering, msgID)
			s.mu.Unlock()
		}()
		// promptRequired 有两种可能：这一轮刚好结束了（等 running 落下来，按新一轮发），或者
		// 这一轮还没在 agent 那边真正开始（比如 claude 进程还在启动）——那就隔一会儿再插一次。
		for i := 0; i < 100; i++ {
			res, err := agenthost.Call[acpx.SteerResponse](ctx, c, acpx.MethodSteer, acpx.SteerRequest{SessionId: acp.SessionId(agentSID), Prompt: content, MessageId: msgID, Now: urgent})
			if err != nil {
				return SendResult{}, err
			}
			if res.Outcome == acpx.SteerInjected {
				s.emit(&Event{Kind: EvUser, MessageID: msgID, Content: raw, Steer: true, From: from})
				s.mu.Lock()
				early := s.steering[msgID]
				delete(s.steering, msgID)
				s.mu.Unlock()
				if early != "" {
					s.emit(&Event{Kind: EvSteerState, MessageID: msgID, Text: early})
				}
				s.touchTitle(content)
				go s.autoTitle()
				return SendResult{MessageID: msgID, Steered: true}, nil
			}
			time.Sleep(150 * time.Millisecond)
			s.mu.Lock()
			r := s.running
			s.mu.Unlock()
			if !r {
				break
			}
		}
	} else if running {
		return SendResult{}, errors.New("这个 agent 不支持中途插话，等这一轮结束再发")
	}

	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return SendResult{}, errors.New("这一轮还在跑")
	}
	s.running = true
	s.lastErr = ""
	s.turnMsg = msgID
	s.turnStarted, s.turnTokens = now(), 0
	s.mu.Unlock()

	s.emit(&Event{Kind: EvUser, MessageID: msgID, Content: raw, From: from})
	s.emit(&Event{Kind: EvTurnStart, MessageID: msgID})
	s.touchTitle(content)
	s.setStatus()
	// Name it as soon as the prompt is in, not only when the turn ends: a first turn can run for many minutes, and
	// until then the tab showed the raw first message (users read that as auto-rename not working)
	go s.autoTitle()

	go s.runTurn(c, agentSID, msgID, content)
	return SendResult{MessageID: msgID}, nil
}

// retryWaits：一轮因为网络出错失败时自动重试的间隔（agent 自己的重试已经用完了才会走到这里）。
// 指数回退：5 秒起翻倍，单次最多 1 分钟，所有等待加起来不超过 5 分钟 —— 偶发的 403 / 过载常常要一两分钟才缓过来，
// 原来固定三次（5/15/30 秒）不到一分钟就放弃了；再往后多半是真坏了，别让人一直等。
var retryWaits = backoff(5*time.Second, time.Minute, 5*time.Minute)

func backoff(first, max, total time.Duration) []time.Duration {
	var out []time.Duration
	for d, sum := first, time.Duration(0); sum < total; d *= 2 {
		d = min(d, max, total-sum)
		out = append(out, d)
		sum += d
	}
	return out
}

// retryable：值得自动重来的错误 —— 网络 / 过载 / agent 进程半路死了。额度用完、参数错这类不重试
// （重试也一样，还白白烧钱）。
func retryable(msg string) bool {
	m := strings.ToLower(msg)
	// 「API Error: 403 Request not allowed」：API 那边偶发的拒绝（前面还挂着一句 Failed to authenticate），
	// 不是登录失效，过一会儿就好（用户报的）—— 先于下面的 403 判掉。
	if strings.Contains(m, "request not allowed") {
		return true
	}
	for _, k := range []string{"rate limit", "usage limit", "quota", "invalid", "unauthorized", "401", "403", "credit"} {
		if strings.Contains(m, k) {
			return false
		}
	}
	for _, k := range []string{"econnreset", "etimedout", "enotfound", "eai_again", "econnrefused", "socket hang up", "network", "connection",
		"timeout", "timed out", "overloaded", "529", "502", "503", "504", "stream disconnected", "fetch failed", "进程意外退出", "进程退出了"} {
		if strings.Contains(m, k) {
			return true
		}
	}
	return false
}

func (s *Session) runTurn(c *agenthost.Host, agentSID, msgID string, content []acp.ContentBlock) {
	res, err := c.Conn.Prompt(context.Background(), acp.PromptRequest{SessionId: acp.SessionId(agentSID), MessageId: &msgID, Prompt: content})
	// 网络出错：等一会儿让 agent 从断的地方接着做（发一句「继续」，不进对话记录 —— 界面上是一行提示）。
	for attempt := 0; err != nil && attempt < len(retryWaits) && retryable(errText(err)) && !s.isShut(); attempt++ {
		wait := retryWaits[attempt]
		s.emit(&Event{Kind: EvNotice, Level: "retry", Until: now() + wait.Milliseconds(),
			Text: fmt.Sprintf("出错了：%s。%d 秒后自动重试（第 %d/%d 次），按停止可以取消。", oneLine(errText(err), 200), int(wait.Seconds()), attempt+1, len(retryWaits))})
		if !s.sleepUnlessCancelled(wait) || s.isShut() {
			break
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		c2, aerr := s.ensureAttached(ctx)
		cancel()
		if aerr != nil {
			err = aerr
			continue
		}
		c, agentSID = c2, s.agentSID()
		rid := uuid.NewString()
		res, err = c.Conn.Prompt(context.Background(), acp.PromptRequest{SessionId: acp.SessionId(agentSID), MessageId: &rid, Prompt: []acp.ContentBlock{
			acp.TextBlock("继续。上一次请求因为网络错误中断了，请从中断的地方接着做。"),
		}})
	}
	ev := &Event{Kind: EvTurnEnd, MessageID: msgID}
	s.mu.Lock()
	ev.DurationMs, ev.OutputTokens = now()-s.turnStarted, s.turnTokens
	s.mu.Unlock()
	if err != nil && s.isShut() {
		err, ev.StopReason = nil, "cancelled" // 人关了 pane、进程是我们杀的：和按停止一样
	} else if err != nil {
		ev.Error = errText(err)
		ev.StopReason = "error"
	} else {
		ev.StopReason = string(res.StopReason)
	}
	// 这一轮结束了，还挂着的审批 / 提问全部作废（agent 那边已经不等了）。
	s.mu.Lock()
	for id, p := range s.pending {
		close(p.ch)
		delete(s.pending, id)
	}
	s.running = false
	s.turnMsg = ""
	go s.markSynced(10 * time.Second) // 这一轮是我们写的：记下文件现在多大（claude 在一轮之后还会补写标题之类，等一会儿再量）
	if err != nil {
		s.lastErr = ev.Error
	}
	next := s.autoNext && err == nil
	s.autoNext = false
	// agent 自己又接着开了一轮（后台任务回来了）就等那一轮也跑完。出错了、人按了停止就不关了：人要看一眼 / 改主意了
	closeNow := s.closeAfter && !next && err == nil && ev.StopReason != "cancelled"
	if !next {
		s.closeAfter = false
	}
	if next {
		s.running, s.auto = true, true
		s.turnStarted, s.turnTokens = now(), 0
	}
	s.mu.Unlock()
	s.emit(ev)
	go s.autoTitle()
	if next {
		s.emit(&Event{Kind: EvTurnStart, Auto: true})
	}
	s.flush(true)
	s.setStatus()
	s.h.saveIndex()
	if closeNow {
		go s.h.CloseUI(context.Background(), s.rec.ID)
	}
}

func errText(err error) string {
	var re *acp.RequestError
	if errors.As(err, &re) {
		if m, ok := re.Data.(map[string]any); ok {
			if msg, ok := m["message"].(string); ok && msg != "" {
				return msg
			}
			if msg, ok := m["error"].(string); ok && msg != "" {
				return msg
			}
		}
		if re.Message != "" {
			return re.Message
		}
	}
	return err.Error()
}

func (s *Session) touchTitle(content []acp.ContentBlock) {
	s.mu.Lock()
	if s.rec.Title != "" && !(s.rec.TitleStale && !s.rec.CustomTitle) {
		s.mu.Unlock()
		return
	}
	for _, b := range content {
		if b.Text != nil && strings.TrimSpace(b.Text.Text) != "" && !strings.HasPrefix(strings.TrimSpace(b.Text.Text), "/") && !strings.HasPrefix(b.Text.Text, shellPrefix) {
			t := strings.Join(strings.Fields(b.Text.Text), " ")
			if utf8.RuneCountInString(t) > 60 {
				t = string([]rune(t)[:60]) + "…"
			}
			s.rec.Title, s.rec.TitleStale = t, false
			break
		}
	}
	s.mu.Unlock()
	s.h.broadcastSession(s)
	s.h.saveIndex()
}

func (s *Session) isShut() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.shut
}

// sleepUnlessCancelled：自动重试前的等待；人按了停止就提前返回 false。
func (s *Session) sleepUnlessCancelled(d time.Duration) bool {
	s.mu.Lock()
	s.retryCancel = make(chan struct{})
	ch := s.retryCancel
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.retryCancel = nil
		s.mu.Unlock()
	}()
	select {
	case <-time.After(d):
		return true
	case <-ch:
		return false
	}
}

func oneLine(t string, n int) string {
	t = strings.Join(strings.Fields(t), " ")
	if utf8.RuneCountInString(t) > n {
		t = string([]rune(t)[:n]) + "…"
	}
	return t
}

func (s *Session) Cancel(ctx context.Context) error {
	s.mu.Lock()
	if s.retryCancel != nil {
		close(s.retryCancel)
		s.retryCancel = nil
	}
	running := s.running
	sid := s.rec.AgentSessionID
	s.mu.Unlock()
	if !running {
		return nil
	}
	c, err := s.h.host(ctx, s.rec.Agent)
	if err != nil {
		return err
	}
	return c.Conn.Cancel(ctx, acp.CancelNotification{SessionId: acp.SessionId(sid)})
}

// ---- agent 推来的 ----

func (s *Session) onUpdate(u acp.SessionUpdate) {
	raw, err := json.Marshal(u)
	if err != nil {
		return
	}
	switch {
	case u.CurrentModeUpdate != nil:
		s.mu.Lock()
		if s.modes != nil {
			s.modes.CurrentModeId = u.CurrentModeUpdate.CurrentModeId
		}
		s.mu.Unlock()
		s.syncRecFromState()
		s.h.broadcastSession(s)
	case u.AvailableCommandsUpdate != nil:
		s.mu.Lock()
		s.commands = u.AvailableCommandsUpdate.AvailableCommands
		s.mu.Unlock()
		s.h.cmds.set(s.rec.Agent, u.AvailableCommandsUpdate.AvailableCommands)
		s.h.broadcastSession(s)
		return // 不进日志：几十上百条命令，每次起进程都推一遍
	case u.ConfigOptionUpdate != nil:
		s.mu.Lock()
		s.config = u.ConfigOptionUpdate.ConfigOptions
		s.h.opts.set(s.rec.Agent, s.modes, s.config)
		s.mu.Unlock()
		s.syncRecFromState()
		s.h.broadcastSession(s)
		return
	case u.SessionInfoUpdate != nil:
		if g, ok := u.SessionInfoUpdate.Meta["gerdr"].(map[string]any); ok {
			s.onGerdrInfo(g)
		}
		if g, ok := u.SessionInfoUpdate.Meta["gerdr"].(map[string]any); ok {
			if n, ok := g["notice"].(string); ok && n != "" {
				lv, _ := g["noticeLevel"].(string)
				if lv == "" {
					lv = "warn"
				}
				ev := &Event{Kind: EvNotice, Level: lv, Text: n}
				// The adapter reports how long the wait is; turn it into a deadline here so the UI can count down
				// (a bare "9 秒后" stays at 9 forever, and a replayed log would restart the count)
				if d := num(g["noticeDelayMs"]); d > 0 {
					ev.Until = now() + d
				}
				s.emit(ev)
			}
			// 插话排队 / 被读到：只记用户发过的那几条（command_lifecycle 对每条写进去的消息都报）
			if st, ok := g["steer"].(map[string]any); ok {
				id, _ := st["id"].(string)
				state, _ := st["state"].(string)
				if id != "" && (state == "queued" || state == "started") {
					s.mu.Lock()
					_, early := s.steering[id]
					if early {
						s.steering[id] = state // 消息还没记进日志：先存着，记完再补（started 会盖掉 queued）
					}
					s.mu.Unlock()
					if !early && s.isSteer(id) {
						s.emit(&Event{Kind: EvSteerState, MessageID: id, Text: state})
					}
				}
			}
			// agent 那边换了会话 id（claude 的 /clear）：之后回退、恢复都按新的来；对话里留一条分隔线
			if id, ok := g["sessionId"].(string); ok && id != "" {
				s.mu.Lock()
				prev := s.rec
				s.mu.Unlock()
				s.h.rekey(s, id)
				if c, _ := g["cleared"].(bool); c {
					if prev.AgentSessionID != "" && prev.AgentSessionID != id {
						s.h.splitOffCleared(s, prev)
					}
					s.mu.Lock()
					// The title belongs to what was cleared away, renamed or not (a /rename names that piece of work, not
					// the tab): drop it right away rather than leave the old name on an empty context until the next
					// message (two entries with the same name in the session list). Auto-naming starts over (title.go).
					s.rec.Title, s.rec.TitleStale, s.rec.CustomTitle = "", false, false
					// The usage was the cleared context's: hide it until the next request reports the new one (the
					// window stays, it is the model's), instead of showing the old percentage over an empty conversation.
					s.rec.CtxTokens = 0
					s.mu.Unlock()
					s.h.broadcastSession(s)
					s.emit(&Event{Kind: EvNotice, Level: "clear", Text: "上下文已清空"})
				}
			}
		}
		if t := u.SessionInfoUpdate.Title; t != nil && *t != "" {
			// agent 里人主动改的名（claude 的 /rename）：和在 gerdr 里改名一样，覆盖并且以后不再自动改。
			renamed := false
			if g, ok := u.SessionInfoUpdate.Meta["gerdr"].(map[string]any); ok {
				renamed, _ = g["renamed"].(bool)
			}
			s.mu.Lock()
			custom := s.rec.CustomTitle
			if renamed {
				s.rec.Title, s.rec.CustomTitle = *t, true
				custom = false
			} else if !custom {
				s.rec.Title = *t
			}
			s.mu.Unlock()
			if !custom {
				s.h.broadcastSession(s)
				s.h.saveIndex()
			}
		}
		return
	case u.UsageUpdate != nil:
		return
	}
	s.emit(&Event{Kind: EvUpdate, Update: raw})
}

// onGerdrInfo：适配器经 session_info_update._meta.gerdr 报的后台任务和「自己开的一轮」。
func (s *Session) onGerdrInfo(g map[string]any) {
	if p, ok := g[acpx.ProgressKey].(map[string]any); ok {
		s.mu.Lock()
		if n, ok := p["outputTokens"].(float64); ok {
			s.turnTokens = int(n)
		}
		if n, ok := p["contextTokens"].(float64); ok && n > 0 {
			s.rec.CtxTokens = int(n)
		}
		if n, ok := p["contextWindow"].(float64); ok && n > 0 {
			s.rec.CtxWindow = int(n)
		}
		s.mu.Unlock()
		s.h.broadcastSession(s)
	}
	if v, ok := g["tasks"]; ok {
		var list []acpx.Task
		b, _ := json.Marshal(v)
		_ = json.Unmarshal(b, &list)
		s.mu.Lock()
		prev := map[string]string{}
		for _, t := range s.tasks {
			prev[t.ID] = t.Status
		}
		s.tasks = list
		s.mu.Unlock()
		s.h.broadcastSession(s)
		// 刚结束的（之前在跑、或者一出现就已经结束）：记进对话。子 agent 自己起的不记，在它的面板里看。
		for _, t := range list {
			if t.Status == "running" || t.Parent != "" {
				continue
			}
			if st, seen := prev[t.ID]; seen && st != "running" {
				continue
			}
			tb, _ := json.Marshal(t)
			s.emit(&Event{Kind: EvTask, Task: tb})
		}
	}
	if v, ok := g[acpx.AutoTurnKey].(bool); ok {
		s.mu.Lock()
		start := v && !s.running
		if v && s.running && !s.auto {
			s.autoNext = true
		}
		if !v {
			s.autoNext = false
		}
		end := !v && s.auto
		if start {
			s.running, s.auto, s.lastErr = true, true, ""
			s.turnStarted, s.turnTokens = now(), 0
		}
		if end {
			s.running, s.auto = false, false
			for id, p := range s.pending {
				close(p.ch)
				delete(s.pending, id)
			}
		}
		s.mu.Unlock()
		if start {
			s.emit(&Event{Kind: EvTurnStart, Auto: true})
			s.setStatus()
		}
		if end {
			s.mu.Lock()
			dur, tok := now()-s.turnStarted, s.turnTokens
			s.mu.Unlock()
			s.emit(&Event{Kind: EvTurnEnd, StopReason: string(acp.StopReasonEndTurn), Auto: true, DurationMs: dur, OutputTokens: tok})
			s.flush(true)
			s.setStatus()
		}
	}
}

// StopTask / BackgroundTool：后台任务面板上的「停止」、运行中工具上的「转到后台」。
func (s *Session) StopTask(ctx context.Context, taskID string) error {
	c, err := s.ensureAttached(ctx)
	if err != nil {
		return err
	}
	_, err = agenthost.Call[map[string]any](ctx, c, acpx.MethodTaskStop, acpx.TaskStopRequest{SessionId: acp.SessionId(s.agentSID()), TaskId: taskID})
	return err
}

func (s *Session) BackgroundTool(ctx context.Context, toolCallID string) (bool, error) {
	c, err := s.ensureAttached(ctx)
	if err != nil {
		return false, err
	}
	r, err := agenthost.Call[acpx.TaskBackgroundResponse](ctx, c, acpx.MethodTaskBackground, acpx.TaskBackgroundRequest{SessionId: acp.SessionId(s.agentSID()), ToolCallId: toolCallID})
	return r.Backgrounded, err
}

// AllowRules：工具调用上的「允许并重试」—— 把规则交给 agent 加进它的允许名单（重试那句话前端自己发）。
func (s *Session) AllowRules(ctx context.Context, rules []string) error {
	c, err := s.ensureAttached(ctx)
	if err != nil {
		return err
	}
	_, err = agenthost.Call[map[string]any](ctx, c, acpx.MethodAllowRules, acpx.AllowRulesRequest{SessionId: acp.SessionId(s.agentSID()), Rules: rules})
	return err
}

func (s *Session) agentSID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rec.AgentSessionID
}

func (s *Session) askPermission(ctx context.Context, req acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	reqID := newID()
	raw, _ := json.Marshal(req)
	p := &pending{kind: "permission", ch: make(chan json.RawMessage, 1), req: raw, at: now()}
	s.mu.Lock()
	s.pending[reqID] = p
	s.mu.Unlock()
	s.emit(&Event{Kind: EvPermission, ReqID: reqID, Request: raw})
	s.setStatus()
	cancel := func() (acp.RequestPermissionResponse, error) {
		s.resolve(reqID, EvPermissionDone, json.RawMessage(`{"cancelled":true}`))
		return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeCancelled()}, nil
	}
	select {
	case ans, ok := <-p.ch:
		if !ok {
			return cancel()
		}
		var a struct {
			OptionID  string `json:"optionId"`
			Cancelled bool   `json:"cancelled"`
		}
		_ = json.Unmarshal(ans, &a)
		if a.Cancelled || a.OptionID == "" {
			return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeCancelled()}, nil
		}
		return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeSelected(acp.PermissionOptionId(a.OptionID))}, nil
	case <-ctx.Done():
		return cancel()
	}
}

func (s *Session) ask(ctx context.Context, req acpx.AskRequest) (acpx.AskResponse, error) {
	reqID := newID()
	raw, _ := json.Marshal(req)
	p := &pending{kind: "ask", ch: make(chan json.RawMessage, 1), req: raw, at: now()}
	s.mu.Lock()
	s.pending[reqID] = p
	s.mu.Unlock()
	s.emit(&Event{Kind: EvAsk, ReqID: reqID, Request: raw})
	s.setStatus()
	select {
	case ans, ok := <-p.ch:
		if !ok {
			s.resolve(reqID, EvAskDone, json.RawMessage(`{"cancelled":true}`))
			return acpx.AskResponse{Cancelled: true}, nil
		}
		var res acpx.AskResponse
		_ = json.Unmarshal(ans, &res)
		return res, nil
	case <-ctx.Done():
		s.resolve(reqID, EvAskDone, json.RawMessage(`{"cancelled":true}`))
		return acpx.AskResponse{Cancelled: true}, nil
	}
}

// resolve 记一条「已回答 / 已作废」并把它从等待里拿掉（不往等待的那头送东西）。
func (s *Session) resolve(reqID, kind string, resp json.RawMessage) {
	s.mu.Lock()
	_, had := s.pending[reqID]
	delete(s.pending, reqID)
	answered := false
	for _, e := range s.events {
		if (e.Kind == EvPermissionDone || e.Kind == EvAskDone) && e.ReqID == reqID {
			answered = true
		}
	}
	s.mu.Unlock()
	if !answered {
		s.emit(&Event{Kind: kind, ReqID: reqID, Response: resp})
	}
	if had {
		s.setStatus()
	}
}

// Answer 回答一个审批（optionId）或提问（answers）。
func (s *Session) Answer(reqID string, resp json.RawMessage) error {
	s.mu.Lock()
	p := s.pending[reqID]
	if p != nil {
		delete(s.pending, reqID)
	}
	s.mu.Unlock()
	if p == nil {
		return errors.New("这个请求已经过期了")
	}
	kind := EvPermissionDone
	if p.kind == "ask" {
		kind = EvAskDone
	}
	s.emit(&Event{Kind: kind, ReqID: reqID, Response: resp})
	p.ch <- resp
	s.setStatus()
	return nil
}

// ---- 设置 ----

func (s *Session) SetMode(ctx context.Context, mode string) error {
	c, err := s.ensureAttached(ctx)
	if err != nil {
		return err
	}
	// claude 会把模式 / 模型的变化写进记录文件：是我们改的，别让下次打开时当成「别处聊过」（CatchUp）
	defer func() { go s.markSynced(2 * time.Second) }()
	if _, err := c.Conn.SetSessionMode(ctx, acp.SetSessionModeRequest{SessionId: acp.SessionId(s.rec.AgentSessionID), ModeId: acp.SessionModeId(mode)}); err != nil {
		return err
	}
	s.mu.Lock()
	if s.modes != nil {
		s.modes.CurrentModeId = acp.SessionModeId(mode)
	}
	s.mu.Unlock()
	s.syncRecFromState()
	s.h.saveIndex()
	s.h.broadcastSession(s)
	return nil
}

func (s *Session) SetConfig(ctx context.Context, id, value string) error {
	c, err := s.ensureAttached(ctx)
	if err != nil {
		return err
	}
	// claude 会把模式 / 模型的变化写进记录文件：是我们改的，别让下次打开时当成「别处聊过」（CatchUp）
	defer func() { go s.markSynced(2 * time.Second) }()
	res, err := c.Conn.SetSessionConfigOption(ctx, acp.SetSessionConfigOptionRequest{ValueId: &acp.SetSessionConfigOptionValueId{
		SessionId: acp.SessionId(s.rec.AgentSessionID), ConfigId: acp.SessionConfigId(id), Value: acp.SessionConfigValueId(value),
	}})
	if err != nil {
		return err
	}
	s.mu.Lock()
	if len(res.ConfigOptions) > 0 {
		s.config = res.ConfigOptions
		s.h.opts.set(s.rec.Agent, s.modes, s.config)
	}
	s.mu.Unlock()
	s.syncRecFromState()
	s.h.saveIndex()
	s.h.broadcastSession(s)
	return nil
}

func (s *Session) Rename(title string) {
	s.mu.Lock()
	s.rec.Title = strings.TrimSpace(title)
	s.rec.CustomTitle = s.rec.Title != ""
	s.mu.Unlock()
	s.h.saveIndex()
	s.h.broadcastSession(s)
}

// ---- 回退 ----

// EscapeResult：按 Esc 的结果。Content 是撤回的话（前端放回输入框）。
type EscapeResult struct {
	Content    []json.RawMessage `json:"content"`
	RolledBack bool              `json:"rolledBack,omitempty"` // 整条消息撤回了（agent 一个字都还没回）
	Withdrawn  int               `json:"withdrawn,omitempty"`  // 收回了几条还没被处理的插话
}

// Escape：和 Claude Code 里按 Esc 一样 —— 打断这一轮，还没被 agent 用上的话放回输入框：
//   - 这一轮 agent 一个字都还没回：打断，整条消息连同后面的插话撤回（对话回到发之前），原话放回去；
//   - agent 已经在回了：最后那几条它还没处理的插话，先收回（claude 的 cancel_async_message）再打断 ——
//     先打断的话 claude 会把排着的插话留到下一轮去跑；收回的那几条从对话里删掉、放回去。
//     codex 收不回插话（turn/steer 一发就进了这一轮），只打断。
func (s *Session) Escape(ctx context.Context) (EscapeResult, error) {
	out := EscapeResult{Content: []json.RawMessage{}}
	s.escMu.Lock()
	defer s.escMu.Unlock()
	s.mu.Lock()
	running, auto, turnMsg, agentSID := s.running, s.auto, s.turnMsg, s.rec.AgentSessionID
	evs := append([]*Event(nil), s.events...)
	s.mu.Unlock()
	if !running {
		return out, nil
	}
	start := -1
	for i := len(evs) - 1; i >= 0; i-- {
		if evs[i].Kind == EvTurnStart {
			start = i
			break
		}
	}
	lastOut := start
	for i := start + 1; i < len(evs); i++ {
		switch evs[i].Kind {
		case EvUpdate, EvPermission, EvAsk, EvTask:
			lastOut = i
		}
	}
	// 要收回的插话：最后一次 agent 有动静之后的用户消息，加上这一轮里还标着「排队中」（steer_state 没到 started）的插话 ——
	// 插话默认排到下一个空档（priority next），排着的时候 agent 照样在出字，只看「动静之后」会漏掉它们，
	// 打断后被带进下一轮（用户报的）
	state := map[string]string{}
	for i := start + 1; i < len(evs); i++ {
		if evs[i].Kind == EvSteerState {
			state[evs[i].MessageID] = evs[i].Text
		}
	}
	var tail []*Event
	for i := start + 1; i < len(evs); i++ {
		if e := evs[i]; e.Kind == EvUser && (i > lastOut || (e.Steer && state[e.MessageID] == "queued")) {
			tail = append(tail, e)
		}
	}
	untouched := start >= 0 && lastOut == start && !auto && turnMsg != ""

	c, err := s.ensureAttached(ctx)
	if err != nil {
		return out, err
	}
	withdrawn := map[string]bool{}
	if len(tail) > 0 && !untouched {
		var ids []string
		for _, e := range tail {
			ids = append(ids, e.MessageID)
		}
		if r, err := agenthost.Call[acpx.WithdrawResponse](ctx, c, acpx.MethodWithdraw, acpx.WithdrawRequest{SessionId: acp.SessionId(agentSID), MessageIds: ids}); err == nil {
			for _, id := range r.Withdrawn {
				withdrawn[id] = true
			}
		}
	}
	if err := s.Cancel(ctx); err != nil {
		return out, err
	}
	// 等这一轮真停下来（回退要求没在跑）
	for i := 0; i < 200; i++ {
		s.mu.Lock()
		r := s.running
		s.mu.Unlock()
		if !r {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if untouched {
		_, err := s.Rewind(ctx, turnMsg)
		// agent 那边找不到这条：打断得太快，它还没把消息记进会话（claude 还没写进记录 / codex 还没建出那一轮）——
		// 等于它压根没收到，只在 gerdr 这边把这一轮删掉就行（实测：发出去 150ms 就按 Esc 是这种）。
		if err != nil && strings.Contains(err.Error(), "not found") {
			s.truncateFrom(turnMsg)
			err = nil
		}
		if err == nil {
			out.RolledBack = true
			for _, e := range evs {
				if e.Kind == EvUser && (e.MessageID == turnMsg || containsEvent(tail, e)) {
					out.Content = append(out.Content, e.Content...)
				}
			}
			return out, nil
		}
		s.h.log.Warn("escape: rewind failed", "err", err)
	}
	if len(withdrawn) > 0 {
		s.dropUserEvents(withdrawn, &out)
	}
	return out, nil
}

// Warm：把会话的 agent 进程先起来（见 acpx.MethodWarm）。
func (s *Session) Warm(ctx context.Context) error {
	c, err := s.ensureAttached(ctx)
	if err != nil {
		return err
	}
	_, err = c.Conn.CallExtension(ctx, acpx.MethodWarm, map[string]any{"sessionId": s.agentSID()})
	return err
}

// WithdrawLast：输入框里按 ↑ —— 收回最后一条还没被 agent 读到的插话，放回输入框改。
func (s *Session) WithdrawLast(ctx context.Context) (EscapeResult, error) {
	s.mu.Lock()
	id := ""
	if s.running {
		id = s.lastQueuedLocked()
	}
	s.mu.Unlock()
	if id == "" {
		return EscapeResult{Content: []json.RawMessage{}}, nil
	}
	return s.Withdraw(ctx, id)
}

// lastQueuedLocked：最后一条插话还在 agent 队列里没被读到，就回它的 id。
// 看排队状态（steer_state：queued / started）。原来看「它后面 agent 还有没有动静」：插话默认排到下一个空档（priority next）后，
// 排着的时候 agent 照样在出字，那样判断一条都收不回（用户报的）。不报排队状态的 agent 才退回老办法。
func (s *Session) lastQueuedLocked() string {
	state := map[string]string{}
	busy := false
	for i := len(s.events) - 1; i >= 0; i-- {
		e := s.events[i]
		switch e.Kind {
		case EvSteerState:
			if _, ok := state[e.MessageID]; !ok {
				state[e.MessageID] = e.Text // 倒着走，先碰到的是最新的
			}
		case EvUpdate, EvPermission, EvAsk:
			busy = true
		case EvUser:
			if !e.Steer {
				return ""
			}
			if st, ok := state[e.MessageID]; ok {
				if st == "queued" {
					return e.MessageID
				}
				return "" // 最后一条都读到了，前面的更早，也读到了
			}
			if busy {
				return "" // agent 在它之后有动静：已经用上了
			}
			return e.MessageID
		}
	}
	return ""
}

// Withdraw：收回一条还没被 agent 读到的插话（「排队中」那行的撤回 / 立即发、输入框里按 ↑）。
// 已经被读到的收不回，agent 回空，这里什么也不动（Withdrawn = 0）。
func (s *Session) Withdraw(ctx context.Context, msgID string) (EscapeResult, error) {
	out := EscapeResult{Content: []json.RawMessage{}}
	s.mu.Lock()
	agentSID := s.rec.AgentSessionID
	found := false
	for _, e := range s.events {
		if e.Kind == EvUser && e.MessageID == msgID && e.Steer {
			found = true
			break
		}
	}
	s.mu.Unlock()
	if !found {
		return out, nil
	}
	c, err := s.ensureAttached(ctx)
	if err != nil {
		return out, err
	}
	r, err := agenthost.Call[acpx.WithdrawResponse](ctx, c, acpx.MethodWithdraw, acpx.WithdrawRequest{SessionId: acp.SessionId(agentSID), MessageIds: []string{msgID}})
	if err != nil || len(r.Withdrawn) == 0 {
		return out, nil
	}
	s.dropUserEvents(map[string]bool{msgID: true}, &out)
	return out, nil
}

// truncateFrom：日志从这条用户消息起全部删掉（agent 那边没有它，不用回退 agent）。
func (s *Session) truncateFrom(msgID string) {
	s.mu.Lock()
	idx := -1
	for i, e := range s.events {
		if e.Kind == EvUser && e.MessageID == msgID {
			idx = i
			break
		}
	}
	if idx < 0 {
		s.mu.Unlock()
		return
	}
	s.events = s.events[:idx]
	s.lastErr = ""
	all := append([]*Event(nil), s.events...)
	s.mu.Unlock()
	if err := s.lf.rewrite(all); err != nil {
		s.h.log.Warn("rewrite log", "err", err)
	}
	s.h.broadcastEvents(s.rec.ID, all, true)
	s.setStatus()
}

// dropUserEvents：把收回的那几条用户消息从日志里删掉（内容收进 out），整份重推。
func (s *Session) dropUserEvents(ids map[string]bool, out *EscapeResult) {
	s.mu.Lock()
	kept := s.events[:0:0]
	for _, e := range s.events {
		if e.Kind == EvUser && ids[e.MessageID] {
			out.Content = append(out.Content, e.Content...)
			out.Withdrawn++
			continue
		}
		kept = append(kept, e)
	}
	s.events = kept
	all := append([]*Event(nil), kept...)
	s.mu.Unlock()
	if err := s.lf.rewrite(all); err != nil {
		s.h.log.Warn("rewrite log", "err", err)
	}
	s.h.broadcastEvents(s.rec.ID, all, true)
}

func containsEvent(list []*Event, e *Event) bool {
	for _, x := range list {
		if x == e {
			return true
		}
	}
	return false
}

type RewindResult struct {
	// Content 是被回退掉的那条消息（前端放回输入框，人改两个字再发）。
	Content []json.RawMessage `json:"content"`
}

// Rewind 回到 messageID 那条消息之前：只回退对话（交给 agent 截断），不动文件。
// 以前用影子 git 仓库给 cwd 打快照、一起恢复文件，但 cwd 以外改的文件管不到、同目录多个会话的快照
// 互相覆盖，坑太多，删掉了。
func (s *Session) Rewind(ctx context.Context, messageID string) (RewindResult, error) {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return RewindResult{}, errors.New("先停下这一轮再回退")
	}
	// 两种用户消息：gerdr 自己发的（EvUser），和导入 / 追上时从 agent 记录重放的（user_message_chunk，
	// messageId 就是记录里那条的 uuid，一条带图的消息拆成几块、同一个 id）。只认前一种的话，
	// 重新载入过的会话上面那些消息全都「找不到这条消息」（踩过）。
	idx := -1
	var content []json.RawMessage
	steer := false
	for i, e := range s.events {
		if e.Kind == EvUser && e.MessageID == messageID {
			idx, content, steer = i, e.Content, e.Steer
			break
		}
		if id, c, ok := userChunk(e); ok && id == messageID {
			if idx < 0 {
				idx = i
			}
			content = append(content, c)
		} else if idx >= 0 {
			break
		}
	}
	if idx < 0 {
		s.mu.Unlock()
		return RewindResult{}, errors.New("找不到这条消息")
	}
	// /clear 之后 claude 换了一个新会话，这条之前的消息都在旧会话里 —— 回不去
	for _, e := range s.events[idx:] {
		if e.Kind == EvNotice && e.Level == "clear" {
			s.mu.Unlock()
			return RewindResult{}, errors.New("这条消息在 /clear 之前：清空之后只能回退到清空以后发的消息")
		}
	}
	agentSID := s.rec.AgentSessionID
	s.mu.Unlock()

	c, err := s.ensureAttached(ctx)
	if err != nil {
		return RewindResult{}, err
	}
	if steer && !c.SteerRewind() {
		return RewindResult{}, errors.New("插话的消息不能单独回退，回退它所在那一轮开头的那条")
	}
	res, err := agenthost.Call[acpx.RewindResponse](ctx, c, acpx.MethodRewind, acpx.RewindRequest{SessionId: acp.SessionId(agentSID), MessageId: messageID})
	if err != nil {
		return RewindResult{}, fmt.Errorf("回退对话失败：%s", errText(err))
	}
	if res.SessionId != "" && string(res.SessionId) != agentSID {
		s.h.rekey(s, string(res.SessionId))
	}
	if res.Fork != nil {
		s.mu.Lock()
		s.rec.Fork = res.Fork
		s.mu.Unlock()
	}
	out := RewindResult{Content: content}
	s.mu.Lock()
	s.events = s.events[:idx]
	if steer {
		// Cut mid-turn: the turn_end went with the rest. Close the turn so a reload doesn't leave it open.
		s.events = append(s.events, &Event{Seq: s.h.nextSeqLocked(), TS: now(), Kind: EvTurnEnd, StopReason: string(acp.StopReasonEndTurn)})
	}
	s.lastErr = ""
	evs := append([]*Event(nil), s.events...)
	s.mu.Unlock()
	if err := s.lf.rewrite(evs); err != nil {
		s.h.log.Warn("rewrite log", "err", err)
	}
	s.h.broadcastEvents(s.rec.ID, evs, true)
	s.setStatus()
	s.h.saveIndex()
	return out, nil
}

func (s *Session) close(ctx context.Context) {
	_ = s.Cancel(ctx)
	s.stopShells()
	s.mu.Lock()
	for id, p := range s.pending {
		close(p.ch)
		delete(s.pending, id)
	}
	attached := s.attached
	sid := s.rec.AgentSessionID
	s.mu.Unlock()
	if attached {
		if c, err := s.h.host(ctx, s.rec.Agent); err == nil {
			_, _ = c.Conn.CloseSession(ctx, acp.CloseSessionRequest{SessionId: acp.SessionId(sid)})
		}
	}
}

// HeldError：这个会话正被别的进程开着（见 acpx.MethodHolders）。server 把它回成 409 + holders，界面弹框让人挑。
type HeldError struct{ Holders []acpx.Holder }

func (e *HeldError) Error() string {
	h := e.Holders[0]
	if h.Pid == 0 {
		return "这个会话正在" + h.Name + "里开着，这里接不上"
	}
	where := "别的终端"
	if h.Name != "" {
		where = "「" + h.Name + "」"
	}
	return fmt.Sprintf("这个会话还在%s里开着（claude，pid %d）。两边同时写会分叉，谁也看不见对方说的话", where, h.Pid)
}

func (s *Session) heldElsewhere(ctx context.Context, c *agenthost.Host) []acpx.Holder {
	s.mu.Lock()
	sid := s.rec.AgentSessionID
	s.mu.Unlock()
	res, err := agenthost.Call[acpx.HoldersResponse](ctx, c, acpx.MethodHolders, acpx.HoldersRequest{SessionId: acp.SessionId(sid)})
	if err != nil {
		return nil // 不支持的 agent（codex、外部 agent）= 没法判断，照常发
	}
	return res.Holders
}

// Takeover：处理「别处开着」。fork 之后 agent 那边是一个新会话（rekey）；对话日志在 gerdr 这边照旧连着。
func (s *Session) Takeover(ctx context.Context, action string) error {
	// 不先接回会话：codex 那种情况正是接不回来（别处占着），直接拿 agent 会话 id 去 fork
	c, err := s.h.host(ctx, s.rec.Agent)
	if err != nil {
		return err
	}
	s.mu.Lock()
	sid := s.rec.AgentSessionID
	s.mu.Unlock()
	res, err := agenthost.Call[acpx.TakeoverResponse](ctx, c, acpx.MethodTakeover, acpx.TakeoverRequest{SessionId: acp.SessionId(sid), Action: action})
	if err != nil {
		return fmt.Errorf("处理失败：%s", errText(err))
	}
	if res.SessionId != "" && string(res.SessionId) != sid {
		s.h.rekey(s, string(res.SessionId))
		s.emit(&Event{Kind: EvNotice, Level: "info", Text: "另起了一份接着聊：从这里往下只在 gerdr 里，别处那个会话不受影响"})
	}
	return nil
}

// ---- 追上别处的进度 ----
//
// gerdr 的对话日志只记它自己经手的事（以及导入那一刻从 agent 记录里重放的）。会话在别处（herdr / 终端里的 claude）
// 接着聊过，agent 的记录文件长了，gerdr 这边却还停在原地 —— 人看到的是一段过时的对话（用户报的「追不上 herdr」）。
// 打开会话时（client 读日志那一下）查一次：文件比上次对齐时大、而我们这边没在跑它，就从记录整份重新载入。

func (s *Session) transcriptStat(ctx context.Context, since int64) (acpx.TranscriptStatResponse, bool) {
	c, err := s.h.host(ctx, s.rec.Agent)
	if err != nil {
		return acpx.TranscriptStatResponse{}, false
	}
	s.mu.Lock()
	sid, cwd := s.rec.AgentSessionID, s.rec.Cwd
	s.mu.Unlock()
	st, err := agenthost.Call[acpx.TranscriptStatResponse](ctx, c, acpx.MethodTranscriptStat, acpx.TranscriptStatRequest{SessionId: acp.SessionId(sid), Cwd: cwd, Since: since})
	return st, err == nil
}

func (s *Session) markSynced(delay time.Duration) {
	time.Sleep(delay)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if st, ok := s.transcriptStat(ctx, -1); ok {
		s.mu.Lock()
		changed := s.rec.SyncedSize != st.Size
		s.rec.SyncedSize = st.Size
		s.mu.Unlock()
		if changed {
			s.h.saveIndex()
		}
	}
}

// CatchUp：别处接着聊过就重新载入。打开会话时调（异步，不挡读日志）。
//
// 判据不是「文件变大了」，而是「文件里多出了 gerdr 没发过的人话」：文件变大还有一堆自己的原因 —— 自己一轮刚完、
// 还没来得及记大小就重启了（用户报的误报：明明没在别处聊过，却提示「别处接着聊过」）、claude 一轮之后补写的收尾、
// 切模式写的那一行。所以让 agent 把上次对齐之后记录里人说的话列出来，和自己日志里的比：全是自己发过的，只更新位置。
func (s *Session) CatchUp(ctx context.Context) {
	s.mu.Lock()
	// 同一个会话半分钟内只查一次：切 tab、几台设备同时开着，都会触发
	busy := s.running || s.catchingUp || s.adopting || time.Since(s.lastCatch) < 30*time.Second
	if !busy {
		s.lastCatch = time.Now()
	}
	synced := s.rec.SyncedSize
	s.mu.Unlock()
	if busy {
		return
	}
	st, ok := s.transcriptStat(ctx, synced)
	if !ok {
		return // codex 之外的外部 agent：没这个口，不管
	}
	if synced > 0 && st.Size == synced {
		return
	}
	mine := s.userTextSet()
	foreign := 0
	for _, t := range st.NewUserTexts {
		if !mine[normText(t)] {
			foreign++
		}
	}
	s.h.log.Debug("catch up", "session", s.rec.ID, "size", st.Size, "synced", synced, "new", len(st.NewUserTexts), "foreign", foreign)
	if foreign == 0 {
		s.mu.Lock()
		s.rec.SyncedSize = st.Size
		s.mu.Unlock()
		s.h.saveIndex()
		return
	}
	if err := s.Reload(ctx); err != nil {
		s.h.log.Warn("catch up", "session", s.rec.ID, "err", err)
		return
	}
	s.emit(&Event{Kind: EvNotice, Level: "info", Text: fmt.Sprintf("这个会话在别处（herdr / 终端）接着聊过（%d 条消息），已经从 agent 自己的记录同步到最新", foreign)})
}

// adoptOrphanTurn：gerdr 重启时日志里有一轮没收尾（recoverAfterRestart 补了「中断」），核对它是不是真断了。
//
// gerdr 被强杀 / 崩溃 / App 退出时等不及（Electron 3 秒就 SIGKILL，claude 的 stop 也要等 3 秒）时，
// 它起的 claude 成了孤儿：stdin 断了，但会把这一轮跑完、写进自己的记录才退。这时 gerdr 的日志停在半截。
// CatchUp 救不了：它只认「多出了 gerdr 没发过的人话」，这里多出来的全是 agent 的回复，它只会把位置挪过去。
//
// 做法：孤儿还活着（stream-json 进程也登记 <配置目录>/sessions/<pid>.json，holders 认得出，重启后已不算「自己起的」）
// 就等它退；之后记录文件在 gerdr 最后一条日志之后还被写过（超出日志落盘的那 2 秒间隔），就从记录整份重放。
// 没写过 = 真断了，「中断」留着。
func (s *Session) adoptOrphanTurn(lastTS int64) {
	s.mu.Lock()
	s.adopting = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.adopting = false
		s.mu.Unlock()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	defer cancel()
	c, err := s.h.host(ctx, s.rec.Agent)
	if err != nil {
		return
	}
	told := false
	for {
		hs := s.heldElsewhere(ctx, c)
		if len(hs) == 0 {
			break
		}
		if !told {
			told = true
			s.emit(&Event{Kind: EvNotice, Level: "info", Text: fmt.Sprintf("gerdr 重启前的这一轮还在后台跑（claude，pid %d），跑完会自动同步过来", hs[0].Pid)})
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
	st, ok := s.transcriptStat(ctx, -1)
	// 日志每 2 秒落一次盘（flusher），被强杀时最后这一段本来就丢；只差这么点的不算「接着跑过」
	if !ok || st.ModTime <= lastTS+5000 {
		return
	}
	s.mu.Lock()
	running := s.running
	s.mu.Unlock()
	if running {
		// 人已经在新的一轮里了（比如在「别处开着」里把孤儿关掉、接着说话）：这时重放会丢掉正在跑的这一轮，不动
		s.h.log.Warn("adopt orphan turn: session running, skip reload", "session", s.rec.ID)
		return
	}
	if err := s.Reload(ctx); err != nil {
		s.h.log.Warn("adopt orphan turn", "session", s.rec.ID, "err", err)
		return
	}
	// 没见到孤儿也可能走到这儿：这一轮在 gerdr 死之前已经跑完，只是日志还没落盘（或者 gerdr 停了很久才起来）
	text := "gerdr 重启前这一轮有一段没记下来，已经从 agent 自己的记录补上"
	if told {
		text = "gerdr 重启后这一轮在后台跑完了，已经从 agent 自己的记录同步到最新"
	}
	s.emit(&Event{Kind: EvNotice, Level: "info", Text: text})
}

// userChunk：从 agent 记录重放的一块用户消息（user_message_chunk），取出它的 messageId 和内容块。
func userChunk(e *Event) (id string, content json.RawMessage, ok bool) {
	if e.Kind != EvUpdate {
		return "", nil, false
	}
	var u struct {
		SessionUpdate string          `json:"sessionUpdate"`
		MessageID     string          `json:"messageId"`
		Content       json.RawMessage `json:"content"`
	}
	if json.Unmarshal(e.Update, &u) != nil || u.SessionUpdate != "user_message_chunk" || u.MessageID == "" {
		return "", nil, false
	}
	return u.MessageID, u.Content, true
}

// userTextSet：gerdr 日志里人说过的话（自己发的 + 导入 / 重新载入时从记录重放的），归一化过。
func (s *Session) userTextSet() map[string]bool {
	s.mu.Lock()
	evs := append([]*Event(nil), s.events...)
	s.mu.Unlock()
	out := map[string]bool{}
	for _, e := range evs {
		switch e.Kind {
		case EvUser:
			var parts []string
			for _, c := range e.Content {
				var b struct {
					Type string `json:"type"`
					Text string `json:"text"`
				}
				if json.Unmarshal(c, &b) == nil && b.Type == "text" {
					parts = append(parts, b.Text)
				}
			}
			out[normText(strings.Join(parts, "\n"))] = true
			for _, p := range parts { // 带图的消息，记录里可能只剩文字那块
				out[normText(p)] = true
			}
		case EvUpdate:
			var u struct {
				SessionUpdate string `json:"sessionUpdate"`
				Content       struct {
					Text string `json:"text"`
				} `json:"content"`
			}
			if json.Unmarshal(e.Update, &u) == nil && u.SessionUpdate == "user_message_chunk" {
				out[normText(u.Content.Text)] = true
			}
		}
	}
	return out
}

// normText：比对用。空白折成一个空格、只看前 200 个字（两边对长消息的存法可能差在末尾的换行 / 截断）。
func normText(t string) string {
	t = strings.Join(strings.Fields(t), " ")
	if r := []rune(t); len(r) > 200 {
		t = string(r[:200])
	}
	return t
}

// Reload：丢掉 gerdr 这边的对话日志，从 agent 的记录整份重放（LoadSession）。
func (s *Session) Reload(ctx context.Context) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return errors.New("先停下这一轮再重新载入")
	}
	if s.catchingUp {
		s.mu.Unlock()
		return nil
	}
	s.catchingUp = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.catchingUp = false
		s.mu.Unlock()
	}()
	c, err := s.h.host(ctx, s.rec.Agent)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.events = nil
	s.lastErr = ""
	rec := s.rec
	s.mu.Unlock()
	if err := s.lf.rewrite(nil); err != nil {
		s.h.log.Warn("rewrite log", "err", err)
	}
	s.h.broadcastEvents(rec.ID, []*Event{}, true)
	meta := map[string]any{}
	if rec.Mode != "" {
		meta["mode"] = rec.Mode
	}
	if rec.Fork != nil {
		meta["fork"] = rec.Fork
	}
	res, err := c.Conn.LoadSession(ctx, acp.LoadSessionRequest{SessionId: acp.SessionId(rec.AgentSessionID), Cwd: rec.Cwd, McpServers: []acp.McpServer{}, Meta: acpx.Meta(meta)})
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.attached, s.shut = true, false
	if res.Modes != nil {
		s.modes = res.Modes
	}
	if len(res.ConfigOptions) > 0 {
		s.config = res.ConfigOptions
	}
	s.mu.Unlock()
	go s.markSynced(0)
	s.setStatus()
	return nil
}

// isSteer：这条消息是不是一条插话（只有插话才有「排队中」，正常开一轮的那句不显示）。
func (s *Session) isSteer(msgID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.events) - 1; i >= 0; i-- {
		if e := s.events[i]; e.Kind == EvUser && e.MessageID == msgID {
			return e.Steer
		}
	}
	return false
}

// num reads a number out of _meta: float64 once it has been through JSON, the Go type when an adapter runs in-process.
func num(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int:
		return int64(n)
	case int64:
		return n
	}
	return 0
}
