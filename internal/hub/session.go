package hub

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/coder/acp-go-sdk"
	"github.com/google/uuid"

	"github.com/zbysir/gerdr/internal/acpx"
	"github.com/zbysir/gerdr/internal/agenthost"
	"github.com/zbysir/gerdr/internal/snapshot"
)

// Record 是落盘的那部分（sessions/index.json 里一条）。
type Record struct {
	ID             string            `json:"id"`
	Agent          string            `json:"agent"`
	AgentSessionID string            `json:"agentSessionId"`
	Cwd            string            `json:"cwd"`
	Title          string            `json:"title,omitempty"`
	CustomTitle    bool              `json:"customTitle,omitempty"`
	CreatedAt      int64             `json:"createdAt"`
	UpdatedAt      int64             `json:"updatedAt"`
	Mode           string            `json:"mode,omitempty"`
	Config         map[string]string `json:"config,omitempty"`
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

	mu       sync.Mutex
	rec      Record
	events   []*Event
	attached bool // agent 那边接上了（gerdr 重启后要先 ResumeSession）
	status   string
	running  bool
	lastErr  string
	modes    *acp.SessionModeState
	config   []acp.SessionConfigOption
	commands []acp.AvailableCommand
	pending  map[string]*pending
	turnMsg  string // 正在跑的那一轮是哪条消息开的
	auto     bool   // 正在跑的这一轮是 agent 自己开的（后台任务跑完了在汇报），没有 runTurn 在等
	// autoNext：「agent 自己开了一轮」先于上一轮 session/prompt 的响应到了（两条路，顺序不保证）。
	// runTurn 收尾时看到它就直接接上一个 auto 的轮次，不然这一轮在界面上是「闲着」。
	autoNext    bool
	tasks       []acpx.Task
	ctxTokens   int // 上下文现在用了多少 token、窗口多大（输入框下面的「上下文 37%」）
	ctxWindow   int
	turnStarted int64           // 这一轮几点开始的（状态行：跑了多久）
	turnTokens  int             // 这一轮输出了多少 token（状态行）
	sending     map[string]bool // 正在处理的 client 消息 id（同一条重发时挡住并发的那次）
	retryCancel chan struct{}   // 自动重试正在等：关掉它 = 人按了停止
	attachMu    sync.Mutex
}

type pending struct {
	kind string // permission | ask
	ch   chan json.RawMessage
}

func newSession(h *Hub, r Record) *Session {
	return &Session{h: h, rec: r, status: StIdle, pending: map[string]*pending{},
		lf: logFile{path: filepath.Join(h.opt.Dir, "sessions", r.ID+".jsonl")}}
}

func newID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// recoverAfterRestart：上次 gerdr 退出时还在跑的那一轮，日志里没有结尾 —— 补一个。
// 那一轮在 agent 那边已经跟着进程一起没了。
func (s *Session) recoverAfterRestart() {
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
		s.events = append(s.events, &Event{Seq: s.h.nextSeqLocked(), TS: now(), Kind: EvTurnEnd, StopReason: "cancelled", Error: "gerdr 重启，这一轮被中断了"})
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
	Pending  int                       `json:"pending"`
	LastSeq  int64                     `json:"lastSeq"`
	Tasks    []acpx.Task               `json:"tasks,omitempty"`
	Turn     *TurnInfo                 `json:"turn,omitempty"`    // 在跑的这一轮（没在跑是 nil）
	Context  *ContextInfo              `json:"context,omitempty"` // 上下文用量（agent 报过才有）
	Base     string                    `json:"base,omitempty"`    // agent 实际是哪一种（变体的底子；界面上按它认图标和功能）
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
	if s.ctxTokens == 0 || s.ctxWindow == 0 {
		return nil
	}
	return &ContextInfo{Tokens: s.ctxTokens, Window: s.ctxWindow}
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
	cmds := s.commands
	if len(cmds) == 0 && s.h.cmds != nil {
		cmds = s.h.cmds.get(s.rec.Agent) // 这个会话的进程还没起：用这个 agent 上次报的
		if len(cmds) == 0 {
			cmds = s.h.cmds.get(s.h.Base(s.rec.Agent)) // 新建的变体还没报过：先借原版的
		}
	}
	return Summary{Record: s.rec, Status: s.status, Error: s.lastErr, Modes: s.modes, Config: s.config, Commands: cmds, Pending: len(s.pending), LastSeq: last, Tasks: s.tasks, Turn: s.turnInfoLocked(), Context: s.contextLocked(), Base: s.h.Base(s.rec.Agent)}
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
	res, err := c.Conn.ResumeSession(ctx, acp.ResumeSessionRequest{SessionId: acp.SessionId(rec.AgentSessionID), Cwd: rec.Cwd, McpServers: []acp.McpServer{}, Meta: acpx.Meta(meta)})
	if err != nil {
		// 一句话都没说过的会话，agent 那边多半压根没落盘（claude / codex 都是第一条消息才写），
		// 接不回来很正常：换一个新的 agent 会话顶上，人看不出区别。
		if !s.hasUserMessages() {
			nres, nerr := c.Conn.NewSession(ctx, acp.NewSessionRequest{Cwd: rec.Cwd, McpServers: []acp.McpServer{}, Meta: acpx.Meta(meta)})
			if nerr == nil {
				s.h.rekey(s, string(nres.SessionId))
				s.mu.Lock()
				s.attached = true
				s.modes, s.config = nres.Modes, nres.ConfigOptions
				s.mu.Unlock()
				s.syncRecFromState()
				s.h.broadcastSession(s)
				return c, nil
			}
		}
		return nil, fmt.Errorf("接回会话失败：%s", errText(err))
	}
	s.mu.Lock()
	s.attached = true
	s.modes, s.config = res.Modes, res.ConfigOptions
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
	return s.SendFrom(ctx, content, clientID, "")
}

// SendFrom：from 是发送方的会话 id（另一个 agent 经 `gerdr session send` 发来的），记在日志里给界面标出来。
func (s *Session) SendFrom(ctx context.Context, content []acp.ContentBlock, clientID, from string) (SendResult, error) {
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
		return SendResult{}, err
	}
	raw := make([]json.RawMessage, 0, len(content))
	for _, b := range content {
		j, _ := json.Marshal(b)
		raw = append(raw, j)
	}
	s.mu.Lock()
	running := s.running
	agentSID := s.rec.AgentSessionID
	s.mu.Unlock()

	if running && c.Steering() {
		// promptRequired 有两种可能：这一轮刚好结束了（等 running 落下来，按新一轮发），或者
		// 这一轮还没在 agent 那边真正开始（比如 claude 进程还在启动）——那就隔一会儿再插一次。
		for i := 0; i < 100; i++ {
			res, err := agenthost.Call[acpx.SteerResponse](ctx, c, acpx.MethodSteer, acpx.SteerRequest{SessionId: acp.SessionId(agentSID), Prompt: content, MessageId: msgID})
			if err != nil {
				return SendResult{}, err
			}
			if res.Outcome == acpx.SteerInjected {
				s.emit(&Event{Kind: EvUser, MessageID: msgID, Content: raw, Steer: true, From: from})
				s.touchTitle(content)
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

	snap := s.snapshot(msgID)
	s.emit(&Event{Kind: EvUser, MessageID: msgID, Content: raw, Snapshot: snap, From: from})
	s.emit(&Event{Kind: EvTurnStart, MessageID: msgID})
	s.touchTitle(content)
	s.setStatus()

	go s.runTurn(c, agentSID, msgID, content)
	return SendResult{MessageID: msgID}, nil
}

func (s *Session) snapshot(msgID string) string {
	if s.h.opt.NoSnapshots {
		return ""
	}
	commit, err := s.h.snaps.Take(s.rec.Cwd, "before "+msgID)
	if err != nil {
		if !errors.Is(err, snapshot.ErrSkipped) {
			s.h.log.Warn("snapshot", "cwd", s.rec.Cwd, "err", err)
		}
		return ""
	}
	return commit
}

// retryWaits：一轮因为网络出错失败时自动重试的间隔（agent 自己的重试已经用完了才会走到这里）。
var retryWaits = []time.Duration{5 * time.Second, 15 * time.Second, 30 * time.Second}

// retryable：值得自动重来的错误 —— 网络 / 过载 / agent 进程半路死了。额度用完、参数错这类不重试
// （重试也一样，还白白烧钱）。
func retryable(msg string) bool {
	m := strings.ToLower(msg)
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
	for attempt := 0; err != nil && attempt < len(retryWaits) && retryable(errText(err)); attempt++ {
		wait := retryWaits[attempt]
		s.emit(&Event{Kind: EvNotice, Level: "warn", Text: fmt.Sprintf("出错了：%s。%d 秒后自动重试（第 %d/%d 次），按停止可以取消。", oneLine(errText(err), 200), int(wait.Seconds()), attempt+1, len(retryWaits))})
		if !s.sleepUnlessCancelled(wait) {
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
	if err != nil {
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
	if err != nil {
		s.lastErr = ev.Error
	}
	next := s.autoNext && err == nil
	s.autoNext = false
	if next {
		s.running, s.auto = true, true
		s.turnStarted, s.turnTokens = now(), 0
	}
	s.mu.Unlock()
	s.emit(ev)
	if next {
		s.emit(&Event{Kind: EvTurnStart, Auto: true})
	}
	s.flush(true)
	s.setStatus()
	s.h.saveIndex()
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
	if s.rec.Title != "" {
		s.mu.Unlock()
		return
	}
	for _, b := range content {
		if b.Text != nil && strings.TrimSpace(b.Text.Text) != "" {
			t := strings.Join(strings.Fields(b.Text.Text), " ")
			if utf8.RuneCountInString(t) > 60 {
				t = string([]rune(t)[:60]) + "…"
			}
			s.rec.Title = t
			break
		}
	}
	s.mu.Unlock()
	s.h.broadcastSession(s)
	s.h.saveIndex()
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
				s.emit(&Event{Kind: EvNotice, Level: "warn", Text: n})
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
			s.ctxTokens = int(n)
		}
		if n, ok := p["contextWindow"].(float64); ok && n > 0 {
			s.ctxWindow = int(n)
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
	p := &pending{kind: "permission", ch: make(chan json.RawMessage, 1)}
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
	p := &pending{kind: "ask", ch: make(chan json.RawMessage, 1)}
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
	res, err := c.Conn.SetSessionConfigOption(ctx, acp.SetSessionConfigOptionRequest{ValueId: &acp.SetSessionConfigOptionValueId{
		SessionId: acp.SessionId(s.rec.AgentSessionID), ConfigId: acp.SessionConfigId(id), Value: acp.SessionConfigValueId(value),
	}})
	if err != nil {
		return err
	}
	s.mu.Lock()
	if len(res.ConfigOptions) > 0 {
		s.config = res.ConfigOptions
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
	var tail []*Event // 最后一次 agent 有动静之后的用户消息（插话）
	for i := lastOut + 1; i < len(evs); i++ {
		if evs[i].Kind == EvUser {
			tail = append(tail, evs[i])
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
		_, err := s.Rewind(ctx, turnMsg, false)
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

// WithdrawLast：按 ↑ —— 收回最后一条还在排队、agent 还没用上的插话，放回输入框改（和 Claude Code 一样）。
// 收不回（已经被用上了 / agent 不支持）返回空。
func (s *Session) WithdrawLast(ctx context.Context) (EscapeResult, error) {
	out := EscapeResult{Content: []json.RawMessage{}}
	s.mu.Lock()
	running, agentSID := s.running, s.rec.AgentSessionID
	var last *Event
	if running {
		for i := len(s.events) - 1; i >= 0; i-- {
			e := s.events[i]
			if e.Kind == EvUser {
				if e.Steer {
					last = e
				}
				break
			}
			if e.Kind == EvUpdate || e.Kind == EvPermission || e.Kind == EvAsk {
				break // agent 在它之后有动静：已经用上了
			}
		}
	}
	s.mu.Unlock()
	if last == nil {
		return out, nil
	}
	c, err := s.ensureAttached(ctx)
	if err != nil {
		return out, err
	}
	r, err := agenthost.Call[acpx.WithdrawResponse](ctx, c, acpx.MethodWithdraw, acpx.WithdrawRequest{SessionId: acp.SessionId(agentSID), MessageIds: []string{last.MessageID}})
	if err != nil || len(r.Withdrawn) == 0 {
		return out, nil
	}
	s.dropUserEvents(map[string]bool{last.MessageID: true}, &out)
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
	Content      []json.RawMessage `json:"content"`
	RestoredFrom string            `json:"restoredFrom,omitempty"`
	SafetyCommit string            `json:"safetyCommit,omitempty"`
}

// Rewind 回到 messageID 那条消息之前：对话历史交给 agent 截断，文件按那条消息发出之前打的
// 快照恢复（restoreFiles 时）。
func (s *Session) Rewind(ctx context.Context, messageID string, restoreFiles bool) (RewindResult, error) {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return RewindResult{}, errors.New("先停下这一轮再回退")
	}
	idx := -1
	for i, e := range s.events {
		if e.Kind == EvUser && e.MessageID == messageID {
			idx = i
			break
		}
	}
	if idx < 0 {
		s.mu.Unlock()
		return RewindResult{}, errors.New("找不到这条消息")
	}
	target := s.events[idx]
	if target.Steer {
		s.mu.Unlock()
		return RewindResult{}, errors.New("插话的消息不能单独回退，回退它所在那一轮开头的那条")
	}
	agentSID := s.rec.AgentSessionID
	s.mu.Unlock()

	c, err := s.ensureAttached(ctx)
	if err != nil {
		return RewindResult{}, err
	}
	res, err := agenthost.Call[acpx.RewindResponse](ctx, c, acpx.MethodRewind, acpx.RewindRequest{SessionId: acp.SessionId(agentSID), MessageId: messageID})
	if err != nil {
		return RewindResult{}, fmt.Errorf("回退对话失败：%s", errText(err))
	}
	if res.SessionId != "" && string(res.SessionId) != agentSID {
		s.h.rekey(s, string(res.SessionId))
	}
	out := RewindResult{Content: target.Content}
	if restoreFiles && target.Snapshot != "" {
		safety, err := s.h.snaps.Restore(s.rec.Cwd, target.Snapshot)
		if err != nil {
			s.emit(&Event{Kind: EvNotice, Level: "error", Text: "对话已回退，但文件没恢复成功：" + err.Error()})
		} else {
			out.RestoredFrom, out.SafetyCommit = target.Snapshot, safety
		}
	}
	s.mu.Lock()
	s.events = s.events[:idx]
	s.lastErr = ""
	evs := append([]*Event(nil), s.events...)
	s.mu.Unlock()
	if err := s.lf.rewrite(evs); err != nil {
		s.h.log.Warn("rewrite log", "err", err)
	}
	s.h.broadcastEvents(s.rec.ID, evs, true)
	if out.SafetyCommit != "" {
		s.emit(&Event{Kind: EvNotice, Level: "info", Text: "文件已恢复到这条消息发出之前。恢复前的状态存在快照 " + out.SafetyCommit[:12] + "。"})
	}
	s.setStatus()
	s.h.saveIndex()
	return out, nil
}

// RewindPreview 告诉人「回退会动到哪些文件」。
func (s *Session) RewindPreview(messageID string) ([]string, error) {
	s.mu.Lock()
	var snap string
	for _, e := range s.events {
		if e.Kind == EvUser && e.MessageID == messageID {
			snap = e.Snapshot
		}
	}
	cwd := s.rec.Cwd
	s.mu.Unlock()
	if snap == "" {
		return nil, nil
	}
	return s.h.snaps.Changed(cwd, snap)
}

func (s *Session) close(ctx context.Context) {
	_ = s.Cancel(ctx)
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
