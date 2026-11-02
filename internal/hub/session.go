package hub

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/coder/acp-go-sdk"

	"github.com/gerdr-dev/gerdr/acpx"
	"github.com/gerdr-dev/gerdr/chatlog"
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
	// Handoff: this session carries on another agent's conversation (handoff.go).
	Handoff *Handoff `json:"handoff,omitempty"`
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

	mu     sync.Mutex
	escMu  sync.Mutex // Escape 一次只跑一个：连按几下 Esc 的话，并发的几次会各自把同一句话还回去（输入框里叠好几遍，踩过）
	rec    Record
	events []*Event
	// loaded: events holds the log. False = it's only on disk (memory.go): idle sessions don't keep their history in
	// memory, every access goes through loadLocked first.
	loaded      bool
	lastSeq     int64     // Seq of the log's last event, kept while unloaded (Summary.LastSeq)
	lastUse     time.Time // last loadLocked: idle sessions get unloaded (Hub.unloadIdle)
	attached    bool      // agent 那边接上了（gerdr 重启后要先 ResumeSession）
	status      string
	running     bool
	catchingUp  bool
	adopting    bool              // 在等 gerdr 重启前没收尾的那一轮（adoptOrphanTurn），CatchUp 别插手
	orphanSince int64             // 载入时日志里有一轮没收尾：最后一条日志的时间（只在 New 里用一次）
	steering    map[string]string // 正在插的话（msgID → 早到的队列状态），见 SendFrom
	deferred    []*deferredMsg    // slash commands sent mid-turn, waiting for the turn to end (deferred.go)
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
	autoWake json.RawMessage // what woke that auto turn (acpx.AutoTurnWakeKey), for its turn_start
	// closeAfter：会话自己 `gerdr session close` 了自己（用户叫它「做完就关掉 pane」）。当场关会把它正在跑的这一轮
	// （就是发出这条命令的这一轮）掐掉，收尾的话说不出来；所以记下来，这一轮跑完再关（Hub.CloseAfterTurn）
	closeAfter  bool
	tasks       []acpx.Task
	turnStarted int64                // 这一轮几点开始的（状态行：跑了多久）
	turnTokens  int                  // 这一轮输出了多少 token（状态行）
	turnCost    float64              // what the agent reported spending since the last turn_end (emit puts it on the next one)
	turnUse     []acpx.Tokens        // the token reports since the last turn_end, one entry per model (same as turnCost)
	sending     map[string]bool      // 正在处理的 client 消息 id（同一条重发时挡住并发的那次）
	retryCancel chan struct{}        // 自动重试正在等：关掉它 = 人按了停止
	shut        bool                 // Hub.Shutdown 关掉的（关了 pane）：这一轮因为进程被杀报的错不算错、不自动重试；再接上时清掉
	shells      map[string]*shellRun // 输入框里 `!命令` 还在跑的（shell.go）
	attachMu    sync.Mutex
	// wmu orders the writes to the log file (flush's appends, rewriteLog's rewrites): taken before mu, held through
	// the IO, which runs outside mu. Without it a rewrite (withdraw, rewind) ran unlocked next to flush and emit,
	// which read and set lf.Flushed under mu: a race, and an append could land in the file just replaced.
	wmu sync.Mutex
	// writing: a write is under way, so events counted as flushed may not be on disk yet: don't unload (memory.go).
	writing int
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
	return &Session{h: h, rec: r, status: StIdle, pending: map[string]*pending{}, steering: map[string]string{}, loaded: true,
		lf: logFile{Path: filepath.Join(h.opt.Dir, "sessions", r.ID+".jsonl")}}
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
// It works from a scan of the log (scanLog) and appends straight to the file: startup doesn't load any log (memory.go).
func (s *Session) recoverAfterRestart(sc logScan) (openSince int64) {
	var add []*Event
	if sc.open {
		openSince = sc.lastTS
		add = append(add, &Event{Seq: s.h.nextSeqLocked(), TS: now(), Kind: EvTurnEnd, StopReason: "cancelled", Error: "Gerdr 重启，这一轮被中断了"})
	}
	// 还在跑的 `!命令`：进程跟着 gerdr 没了（或者成了没人管的孤儿），补一个结束，卡片别一直转
	for _, id := range sc.shells {
		add = append(add, &Event{Seq: s.h.nextSeqLocked(), TS: now(), Kind: EvShellDone, MessageID: id, Level: "lost"})
	}
	// 没回答的审批 / 提问也一样作废。
	for _, r := range sc.unanswered {
		kind := EvPermissionDone
		if r.Kind == EvAsk {
			kind = EvAskDone
		}
		add = append(add, &Event{Seq: s.h.nextSeqLocked(), TS: now(), Kind: kind, ReqID: r.ReqID, Response: json.RawMessage(`{"cancelled":true}`)})
	}
	s.lastSeq = sc.lastSeq
	if len(add) > 0 {
		if err := s.lf.Append(add); err != nil {
			s.h.log.Warn("write session log", "id", s.rec.ID, "err", err)
		}
		s.lastSeq = add[len(add)-1].Seq
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
	Status string                    `json:"status"`
	Error  string                    `json:"error,omitempty"`
	Modes  *acp.SessionModeState     `json:"modes,omitempty"`
	Config []acp.SessionConfigOption `json:"configOptions,omitempty"`
	// Commands goes out as CommandsKey (fetched from /api/commands/<key>, see cmdSets): the list was ~60 KB in every
	// Summary of every session, on every page load and every streamed chunk.
	Commands    []acp.AvailableCommand `json:"-"`
	CommandsKey string                 `json:"commandsKey,omitempty"`
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
	last := s.lastSeq
	if n := len(s.events); s.loaded && n > 0 {
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
	return Summary{Record: s.rec, Status: s.status, Error: s.lastErr, Modes: modes, Config: config, Commands: cmds, CommandsKey: s.h.cmdSets.key(cmds), CommandsCached: cached && len(cmds) > 0, Pending: len(s.pending), LastSeq: last, Tasks: s.tasks, Turn: s.turnInfoLocked(), Context: s.contextLocked(), Base: s.h.Base(s.rec.Agent), Branch: gitBranch(s.rec.Cwd), Waiting: s.waitingLocked()}
}

func (s *Session) ID() string { return s.rec.ID }

// Events 返回整份日志的拷贝。
func (s *Session) Events() []*Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadLocked()
	out := make([]*Event, len(s.events))
	copy(out, s.events)
	return out
}

// emit 记一条日志并广播（流式片段并进上一条还没落盘的，见 chatlog.Add）。
func (s *Session) emit(e *Event) {
	e.Seq = s.h.nextSeq()
	e.TS = now()
	var use []acpx.Tokens
	if e.Kind == EvTurnEnd {
		// The cost and token reports that came in since the last turn ended (the agent sends its turn's before the
		// turn ends: acp-go-sdk processes notifications before the response that follows them)
		s.mu.Lock()
		e.CostUSD, s.turnCost = s.turnCost, 0
		use, s.turnUse = s.turnUse, nil
		s.mu.Unlock()
	}
	if s.h.opt.Dir != "" {
		e = s.h.slim(e) // pasted images go to blobs/ before the event is kept (memory.go)
	}
	s.mu.Lock()
	s.loadLocked()
	s.events, _ = chatlog.Add(s.events, s.lf.Flushed, e)
	s.rec.UpdatedAt = e.TS
	s.mu.Unlock()
	s.h.broadcastEvents(s.rec.ID, []*Event{e}, false)
	if e.Kind == EvTurnEnd {
		s.mu.Lock()
		r := TurnRecord{TS: e.TS, SessionID: s.rec.ID, Agent: s.rec.Agent, Cwd: s.rec.Cwd, StopReason: e.StopReason, Auto: e.Auto,
			DurationMs: e.DurationMs, OutputTokens: e.OutputTokens, CostUSD: e.CostUSD, Tokens: use}
		s.mu.Unlock()
		s.h.recordTurn(r)
	}
}

// flush 把还没落盘的日志写下去。force=false 时最后一条流式片段先不写（它可能还会长）。
func (s *Session) flush(force bool) {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	s.mu.Lock()
	if !s.loaded {
		s.mu.Unlock()
		return
	}
	n := len(s.events)
	if !force && n > 0 && s.events[n-1].Kind == EvUpdate {
		if chatlog.IsChunk(s.events[n-1].Update) {
			n--
		}
	}
	if n <= s.lf.Flushed {
		s.mu.Unlock()
		return
	}
	batch := append([]*Event(nil), s.events[s.lf.Flushed:n]...)
	from := s.lf.Flushed
	s.lf.Flushed = n
	s.writing++
	s.mu.Unlock()
	err := s.lf.Append(batch)
	s.mu.Lock()
	s.writing--
	if err != nil {
		s.h.log.Warn("write session log", "id", s.rec.ID, "err", err)
		if s.lf.Flushed == n {
			s.lf.Flushed = from
		}
	}
	s.mu.Unlock()
}

// rewriteLog replaces the log file with the events after edit (run under mu, may be nil) changed them, and returns
// them. The file is written outside mu (a long log takes tens of ms), with the events already counted as flushed so
// emit doesn't merge a chunk into one being written; events emitted meanwhile go after them with the next flush.
func (s *Session) rewriteLog(edit func()) []*Event {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	s.mu.Lock()
	s.loadLocked()
	if edit != nil {
		edit()
	}
	all := append([]*Event(nil), s.events...)
	s.lf.Flushed = len(all)
	s.writing++
	s.mu.Unlock()
	err := s.lf.WriteAll(all)
	s.mu.Lock()
	s.writing--
	s.mu.Unlock()
	if err != nil {
		s.h.log.Warn("rewrite log", "id", s.rec.ID, "err", err)
	}
	return all
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
	s.loadLocked()
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
		s.emit(&Event{Kind: EvNotice, Level: "info", Text: "另起了一份接着聊：从这里往下只在 Gerdr 里，别处那个会话不受影响"})
	}
	return nil
}
