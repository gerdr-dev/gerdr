package chatlog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/coder/acp-go-sdk"
	"github.com/google/uuid"

	"github.com/gerdr-dev/gerdr/acpx"
)

// Session drives one ACP session from the client side and records it as an event log that @gerdr/chat renders:
// sending (a new turn, or an interjection while one runs), stop, withdraw, rewind, permission and question
// requests, retries after network errors, background tasks, notices. It is what gerdr's hub does per session,
// minus gerdr's own concerns (titles, the session list, reattaching after a restart, catching up with other
// clients): an embedder (Annulo) runs an agent in-process, routes the ACP client callbacks here and pushes
// OnEvents / OnState to its UI over its own transport.
//
// Wiring: implement acp.Client by forwarding SessionUpdate to Update, RequestPermission to RequestPermission and
// the _gerdr/ask extension request to Ask; pass the client-side connection as Agent.
type Session struct {
	agent Agent
	o     Options
	log   *slog.Logger
	file  *File

	mu       sync.Mutex
	escMu    sync.Mutex
	events   []*Event
	seq      int64
	st       State
	running  bool
	auto     bool // the running turn was started by the agent itself (autoTurn), nobody waits on a Prompt
	autoNext bool // autoTurn true arrived before the previous Prompt returned
	turnMsg  string
	pending  map[string]*pendingReq
	steering map[string]string // interjections being sent → early queue state (see Send)
	retry    chan struct{}     // closed by Stop while a retry waits
	closed   bool
}

// Agent is the part of the ACP client-side connection Session uses (*acp.ClientSideConnection has it).
type Agent interface {
	Prompt(ctx context.Context, p acp.PromptRequest) (acp.PromptResponse, error)
	Cancel(ctx context.Context, p acp.CancelNotification) error
	CallExtension(ctx context.Context, method string, params any) (json.RawMessage, error)
}

type Options struct {
	// File is the log on disk (JSONL, loaded by New; "" = memory only). Events are written at the end of each turn
	// and on every rewrite.
	File string
	// OnEvents gets new events as they happen (replace = false; consecutive text chunks of one message arrive
	// separately and the UI merges them), or the whole log after it was rewritten (replace = true: rewind,
	// withdraw, stop). Called without locks held, in order.
	OnEvents func(events []*Event, replace bool)
	// OnState is called after the state changed (status, tasks, commands, context size…).
	OnState func(State)
	// Steering: the agent takes interjections (_session/steering), as InitializeResponse._meta.steering.supported
	// says. Without it Send refuses while a turn runs.
	Steering bool
	// SteerRewind: the agent can rewind to an interjection (native and codex can; claude can't).
	SteerRewind bool
	// RetryWaits: how long to wait before each automatic retry of a turn that failed with a network-type error
	// (Retryable). nil = RetryWaits; an empty slice turns retries off.
	RetryWaits []time.Duration
	// RetryPrompt is sent to the agent to resume after such an error (not logged as a user message).
	RetryPrompt string
	Log         *slog.Logger
}

// State is the session's live state besides the log (the composer bar, the tasks bar, the status dot).
type State struct {
	SessionID     string                    `json:"sessionId"` // the agent's session id: changes after /clear, and on claude's rewind
	Status        string                    `json:"status"`    // idle | running | waiting (a permission / question is open) | error
	Error         string                    `json:"error,omitempty"`
	Tasks         []acpx.Task               `json:"tasks,omitempty"`
	Commands      []acp.AvailableCommand    `json:"commands,omitempty"`
	Modes         *acp.SessionModeState     `json:"modes,omitempty"`
	Config        []acp.SessionConfigOption `json:"configOptions,omitempty"`
	ContextTokens int                       `json:"ctxTokens,omitempty"`
	ContextWindow int                       `json:"ctxWindow,omitempty"`
	TurnStarted   int64                     `json:"turnStarted,omitempty"` // unix ms, while running
	TurnTokens    int                       `json:"turnTokens,omitempty"`  // output tokens so far this turn
}

const (
	StatusIdle    = "idle"
	StatusRunning = "running"
	StatusWaiting = "waiting"
	StatusError   = "error"
)

type pendingReq struct {
	kind string // permission | ask
	ch   chan json.RawMessage
	req  json.RawMessage
	at   int64
}

// Pending is an open permission request or question.
type Pending struct {
	ReqID   string          `json:"reqId"`
	Kind    string          `json:"kind"` // permission | ask
	Request json.RawMessage `json:"request"`
	At      int64           `json:"at"`
}

// New starts recording sessionID (the agent's id from session/new, load or resume), loading File if there is one.
// Updates arrive during session/new itself (the command list), so a new session's recorder can be created first
// with sessionID "" and given the id afterwards (SetSessionID).
func New(agent Agent, sessionID string, o Options) (*Session, error) {
	if o.RetryWaits == nil {
		o.RetryWaits = RetryWaits
	}
	if o.RetryPrompt == "" {
		o.RetryPrompt = "继续。上一次请求因为网络错误中断了，请从中断的地方接着做。"
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	s := &Session{agent: agent, o: o, log: o.Log, pending: map[string]*pendingReq{}, steering: map[string]string{}}
	s.st = State{SessionID: sessionID, Status: StatusIdle}
	if o.File != "" {
		s.file = &File{Path: o.File}
		evs, err := ReadFile(o.File)
		if err != nil {
			return nil, err
		}
		s.events, s.file.Flushed = evs, len(evs)
		for _, e := range evs {
			s.seq = max(s.seq, e.Seq)
		}
		// A turn left open by a crash: close it, or the UI shows it running forever.
		if open := openTurn(evs); open {
			s.emit(&Event{Kind: EvTurnEnd, StopReason: "cancelled", Error: "interrupted"})
			s.flush(true)
		}
	}
	return s, nil
}

func openTurn(evs []*Event) bool {
	for i := len(evs) - 1; i >= 0; i-- {
		switch evs[i].Kind {
		case EvTurnEnd:
			return false
		case EvTurnStart:
			return true
		}
	}
	return false
}

// Events is a copy of the log.
func (s *Session) Events() []*Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*Event(nil), s.events...)
}

// State is a copy of the live state.
func (s *Session) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stateLocked()
}

func (s *Session) stateLocked() State {
	st := s.st
	st.Tasks = append([]acpx.Task(nil), st.Tasks...)
	switch {
	case len(s.pending) > 0:
		st.Status = StatusWaiting
	case s.running:
		st.Status = StatusRunning
	case st.Error != "":
		st.Status = StatusError
	default:
		st.Status = StatusIdle
	}
	if !s.running {
		st.TurnStarted, st.TurnTokens = 0, 0
	}
	return st
}

// SetSessionID sets the agent's session id (after session/new returned).
func (s *Session) SetSessionID(id string) {
	s.mu.Lock()
	s.st.SessionID = id
	s.mu.Unlock()
	s.changed()
}

// SetOptions records the modes and config options from session/new (load, resume, set_config_option) responses.
func (s *Session) SetOptions(modes *acp.SessionModeState, config []acp.SessionConfigOption) {
	s.mu.Lock()
	if modes != nil {
		s.st.Modes = modes
	}
	if config != nil {
		s.st.Config = config
	}
	s.mu.Unlock()
	s.changed()
}

// Pending lists the open requests, oldest first.
func (s *Session) Pending() []Pending {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Pending
	for id, p := range s.pending {
		out = append(out, Pending{ReqID: id, Kind: p.kind, Request: p.req, At: p.at})
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].At < out[j-1].At; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func now() int64 { return time.Now().UnixMilli() }

// emit logs an event and hands it to OnEvents.
func (s *Session) emit(e *Event) {
	s.mu.Lock()
	s.seq++
	e.Seq, e.TS = s.seq, now()
	flushed := 0
	if s.file != nil {
		flushed = s.file.Flushed
	}
	s.events, _ = Add(s.events, flushed, e)
	s.mu.Unlock()
	if s.o.OnEvents != nil {
		s.o.OnEvents([]*Event{e}, false)
	}
}

func (s *Session) replaced() {
	s.mu.Lock()
	all := append([]*Event(nil), s.events...)
	s.mu.Unlock()
	if s.file != nil {
		if err := s.file.Rewrite(all); err != nil {
			s.log.Warn("rewrite chat log", "err", err)
		}
	}
	if s.o.OnEvents != nil {
		s.o.OnEvents(all, true)
	}
}

func (s *Session) changed() {
	if s.o.OnState != nil {
		s.o.OnState(s.State())
	}
}

// flush writes what isn't on disk yet; without force the last streaming chunk waits (it may still grow).
func (s *Session) flush(force bool) {
	if s.file == nil {
		return
	}
	s.mu.Lock()
	n := len(s.events)
	if !force && n > 0 && s.events[n-1].Kind == EvUpdate && IsChunk(s.events[n-1].Update) {
		n--
	}
	if n <= s.file.Flushed {
		s.mu.Unlock()
		return
	}
	batch := append([]*Event(nil), s.events[s.file.Flushed:n]...)
	from := s.file.Flushed
	s.file.Flushed = n
	s.mu.Unlock()
	if err := s.file.Append(batch); err != nil {
		s.log.Warn("write chat log", "err", err)
		s.mu.Lock()
		if s.file.Flushed == n {
			s.file.Flushed = from
		}
		s.mu.Unlock()
	}
}

func (s *Session) sid() acp.SessionId {
	s.mu.Lock()
	defer s.mu.Unlock()
	return acp.SessionId(s.st.SessionID)
}

// ---- sending ----

// SendResult: the message id used, and whether it went into the running turn as an interjection.
type SendResult struct {
	MessageID string `json:"messageId"`
	Steered   bool   `json:"steered,omitempty"`
}

// Send sends a user message: a new turn when idle, an interjection (queued until the agent's next gap; now = cut the
// current generation short) while a turn runs. messageID ("" = a new one) makes a resend idempotent: a message
// already in the log isn't sent again. Typing while a question is open answers it: the question is closed as
// "replied" with the text, then the message goes in.
func (s *Session) Send(ctx context.Context, content []acp.ContentBlock, messageID string, now_ bool) (SendResult, error) {
	if messageID == "" {
		messageID = uuid.NewString()
	}
	s.mu.Lock()
	for _, e := range s.events {
		if e.Kind == EvUser && e.MessageID == messageID {
			s.mu.Unlock()
			return SendResult{MessageID: messageID, Steered: e.Steer}, nil
		}
	}
	running := s.running
	var asks []string
	for id, p := range s.pending {
		if p.kind == "ask" {
			asks = append(asks, id)
		}
	}
	s.mu.Unlock()
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
	raw := make([]json.RawMessage, 0, len(content))
	for _, b := range content {
		j, _ := json.Marshal(b)
		raw = append(raw, j)
	}

	if running && s.o.Steering {
		// The queue state (steer_state) often arrives before the steering call returns, when the message isn't in
		// the log yet: note it here and replay it after logging the message.
		s.mu.Lock()
		s.steering[messageID] = ""
		s.mu.Unlock()
		defer func() {
			s.mu.Lock()
			delete(s.steering, messageID)
			s.mu.Unlock()
		}()
		// promptRequired: either the turn just ended (wait for running to drop, then start a new one) or it hasn't
		// really started on the agent's side yet (try again shortly).
		for i := 0; i < 100; i++ {
			rawRes, err := s.agent.CallExtension(ctx, acpx.MethodSteer, acpx.SteerRequest{SessionId: s.sid(), Prompt: content, MessageId: messageID, Now: now_})
			if err != nil {
				return SendResult{}, err
			}
			var res acpx.SteerResponse
			_ = json.Unmarshal(rawRes, &res)
			if res.Outcome == acpx.SteerInjected {
				s.emit(&Event{Kind: EvUser, MessageID: messageID, Content: raw, Steer: true})
				s.mu.Lock()
				early := s.steering[messageID]
				delete(s.steering, messageID)
				s.mu.Unlock()
				if early != "" {
					s.emit(&Event{Kind: EvSteerState, MessageID: messageID, Text: early})
				}
				return SendResult{MessageID: messageID, Steered: true}, nil
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
		return SendResult{}, errors.New("this agent can't take a message mid-turn: wait for the turn to end")
	}

	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return SendResult{}, errors.New("a turn is already running")
	}
	s.running, s.auto, s.turnMsg = true, false, messageID
	s.st.Error = ""
	s.st.TurnStarted, s.st.TurnTokens = now(), 0
	s.mu.Unlock()
	s.emit(&Event{Kind: EvUser, MessageID: messageID, Content: raw})
	s.emit(&Event{Kind: EvTurnStart, MessageID: messageID})
	s.changed()
	go s.runTurn(messageID, content)
	return SendResult{MessageID: messageID}, nil
}

func (s *Session) runTurn(msgID string, content []acp.ContentBlock) {
	res, err := s.agent.Prompt(context.Background(), acp.PromptRequest{SessionId: s.sid(), MessageId: &msgID, Prompt: content})
	waits := s.o.RetryWaits
	for attempt := 0; err != nil && attempt < len(waits) && Retryable(errText(err)) && !s.isClosed(); attempt++ {
		wait := waits[attempt]
		s.emit(&Event{Kind: EvNotice, Level: "retry", Until: now() + wait.Milliseconds(),
			Text: fmt.Sprintf("出错了：%s。%d 秒后自动重试（第 %d/%d 次），按停止可以取消。", oneLine(errText(err), 200), int(wait.Seconds()), attempt+1, len(waits))})
		if !s.sleepUnlessStopped(wait) {
			break
		}
		rid := uuid.NewString()
		res, err = s.agent.Prompt(context.Background(), acp.PromptRequest{SessionId: s.sid(), MessageId: &rid, Prompt: []acp.ContentBlock{acp.TextBlock(s.o.RetryPrompt)}})
	}
	ev := &Event{Kind: EvTurnEnd, MessageID: msgID}
	s.mu.Lock()
	ev.DurationMs, ev.OutputTokens = now()-s.st.TurnStarted, s.st.TurnTokens
	if err != nil {
		ev.Error, ev.StopReason = errText(err), "error"
		s.st.Error = ev.Error
	} else {
		ev.StopReason = string(res.StopReason)
	}
	// The turn is over: open requests are void (the agent isn't waiting any more).
	for id, p := range s.pending {
		close(p.ch)
		delete(s.pending, id)
	}
	s.running, s.turnMsg = false, ""
	next := s.autoNext && err == nil
	s.autoNext = false
	if next {
		s.running, s.auto = true, true
		s.st.TurnStarted, s.st.TurnTokens = now(), 0
	}
	s.mu.Unlock()
	s.emit(ev)
	if next {
		s.emit(&Event{Kind: EvTurnStart, Auto: true})
	}
	s.flush(true)
	s.changed()
}

func (s *Session) sleepUnlessStopped(d time.Duration) bool {
	s.mu.Lock()
	s.retry = make(chan struct{})
	ch := s.retry
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.retry = nil
		s.mu.Unlock()
	}()
	select {
	case <-time.After(d):
		return true
	case <-ch:
		return false
	}
}

func (s *Session) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// ---- stop, withdraw, rewind ----

// StopResult: what went back into the composer — the turn's message when stopping rolled it back entirely
// (RolledBack), or withdrawn interjections (Withdrawn of them).
type StopResult struct {
	Content    []json.RawMessage `json:"content"`
	RolledBack bool              `json:"rolledBack,omitempty"`
	Withdrawn  int               `json:"withdrawn,omitempty"`
}

// Stop cancels the running turn. Interjections the agent hasn't read are withdrawn first and handed back; a turn
// stopped before the agent did anything is rewound away and its message handed back, as if never sent.
func (s *Session) Stop(ctx context.Context) (StopResult, error) {
	out := StopResult{Content: []json.RawMessage{}}
	s.escMu.Lock()
	defer s.escMu.Unlock()
	s.mu.Lock()
	if s.retry != nil {
		close(s.retry)
		s.retry = nil
	}
	running, auto, turnMsg := s.running, s.auto, s.turnMsg
	evs := append([]*Event(nil), s.events...)
	s.mu.Unlock()
	if !running {
		return out, nil
	}
	tail, quiet := StopPlan(evs)
	untouched := quiet && !auto && turnMsg != ""
	withdrawn := map[string]bool{}
	if len(tail) > 0 && !untouched {
		var ids []string
		for _, e := range tail {
			ids = append(ids, e.MessageID)
		}
		withdrawn = s.withdraw(ctx, ids)
	}
	if err := s.agent.Cancel(ctx, acp.CancelNotification{SessionId: s.sid()}); err != nil {
		return out, err
	}
	for i := 0; i < 200; i++ { // wait for the turn to really end (rewind needs it idle)
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
		if err != nil && strings.Contains(err.Error(), "not found") {
			// Stopped so early the agent never recorded the message: drop it here only.
			s.truncateFrom(turnMsg)
			err = nil
		}
		if err == nil {
			out.RolledBack = true
			for _, e := range evs {
				if e.Kind == EvUser && (e.MessageID == turnMsg || contains(tail, e)) {
					out.Content = append(out.Content, e.Content...)
				}
			}
			return out, nil
		}
		s.log.Warn("stop: rewind failed", "err", err)
	}
	if len(withdrawn) > 0 {
		s.dropUsers(withdrawn, &out)
	}
	return out, nil
}

func contains(list []*Event, e *Event) bool {
	for _, x := range list {
		if x == e {
			return true
		}
	}
	return false
}

func (s *Session) withdraw(ctx context.Context, ids []string) map[string]bool {
	out := map[string]bool{}
	raw, err := s.agent.CallExtension(ctx, acpx.MethodWithdraw, acpx.WithdrawRequest{SessionId: s.sid(), MessageIds: ids})
	if err != nil {
		return out
	}
	var r acpx.WithdrawResponse
	_ = json.Unmarshal(raw, &r)
	for _, id := range r.Withdrawn {
		out[id] = true
	}
	return out
}

// Withdraw takes back an interjection the agent hasn't read yet ("queued" in the UI). One already read can't be:
// the result is empty.
func (s *Session) Withdraw(ctx context.Context, messageID string) (StopResult, error) {
	out := StopResult{Content: []json.RawMessage{}}
	s.mu.Lock()
	found := false
	for _, e := range s.events {
		if e.Kind == EvUser && e.MessageID == messageID && e.Steer {
			found = true
			break
		}
	}
	s.mu.Unlock()
	if !found {
		return out, nil
	}
	if w := s.withdraw(ctx, []string{messageID}); len(w) > 0 {
		s.dropUsers(w, &out)
	}
	return out, nil
}

// WithdrawLast takes back the last unread interjection (↑ in an empty composer).
func (s *Session) WithdrawLast(ctx context.Context) (StopResult, error) {
	s.mu.Lock()
	id := ""
	if s.running {
		id = LastQueued(s.events)
	}
	s.mu.Unlock()
	if id == "" {
		return StopResult{Content: []json.RawMessage{}}, nil
	}
	return s.Withdraw(ctx, id)
}

func (s *Session) dropUsers(ids map[string]bool, out *StopResult) {
	s.mu.Lock()
	kept, content, n := DropUsers(s.events, ids)
	s.events = kept
	s.mu.Unlock()
	out.Content = append(out.Content, content...)
	out.Withdrawn += n
	s.replaced()
}

func (s *Session) truncateFrom(msgID string) {
	s.mu.Lock()
	for i, e := range s.events {
		if e.Kind == EvUser && e.MessageID == msgID {
			s.events = s.events[:i]
			break
		}
	}
	s.st.Error = ""
	s.mu.Unlock()
	s.replaced()
	s.changed()
}

// Rewind drops user message messageID and everything after it, in the agent (_gerdr/rewind) and in the log, and
// returns the message's content (back into the composer). Only while idle. Files aren't touched.
func (s *Session) Rewind(ctx context.Context, messageID string) ([]json.RawMessage, error) {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return nil, errors.New("stop the turn before rewinding")
	}
	_, content, steer, err := RewindPoint(s.events, messageID)
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if steer && !s.o.SteerRewind {
		return nil, errors.New("插话的消息不能单独回退，回退它所在那一轮开头的那条")
	}
	raw, err := s.agent.CallExtension(ctx, acpx.MethodRewind, acpx.RewindRequest{SessionId: s.sid(), MessageId: messageID})
	if err != nil {
		return nil, fmt.Errorf("回退对话失败：%s", errText(err))
	}
	var res acpx.RewindResponse
	_ = json.Unmarshal(raw, &res)
	s.mu.Lock()
	if res.SessionId != "" {
		s.st.SessionID = string(res.SessionId)
	}
	idx, _, _, err := RewindPoint(s.events, messageID) // the log may have grown meanwhile
	if err == nil {
		s.events = s.events[:idx]
		if steer {
			// Cut mid-turn: the turn_end went with the rest. Close the turn so a reload doesn't leave it open.
			s.seq++
			s.events = append(s.events, &Event{Seq: s.seq, TS: now(), Kind: EvTurnEnd, StopReason: string(acp.StopReasonEndTurn)})
		}
	}
	s.st.Error = ""
	s.mu.Unlock()
	s.replaced()
	s.changed()
	return content, nil
}

// StopTask stops a background task; BackgroundTool moves a running tool call to the background.
func (s *Session) StopTask(ctx context.Context, taskID string) error {
	_, err := s.agent.CallExtension(ctx, acpx.MethodTaskStop, acpx.TaskStopRequest{SessionId: s.sid(), TaskId: taskID})
	return err
}

func (s *Session) BackgroundTool(ctx context.Context, toolCallID string) (bool, error) {
	raw, err := s.agent.CallExtension(ctx, acpx.MethodTaskBackground, acpx.TaskBackgroundRequest{SessionId: s.sid(), ToolCallId: toolCallID})
	if err != nil {
		return false, err
	}
	var r acpx.TaskBackgroundResponse
	_ = json.Unmarshal(raw, &r)
	return r.Backgrounded, nil
}

// Close voids open requests and stops automatic retries. It doesn't cancel the agent's turn (Stop does).
func (s *Session) Close() {
	s.mu.Lock()
	s.closed = true
	if s.retry != nil {
		close(s.retry)
		s.retry = nil
	}
	for id, p := range s.pending {
		close(p.ch)
		delete(s.pending, id)
	}
	s.mu.Unlock()
	s.flush(true)
}

// ---- from the agent ----

// Update records a session/update notification (the client's SessionUpdate callback).
func (s *Session) Update(u acp.SessionUpdate) {
	switch {
	case u.CurrentModeUpdate != nil:
		s.mu.Lock()
		if s.st.Modes != nil {
			s.st.Modes.CurrentModeId = u.CurrentModeUpdate.CurrentModeId
		}
		s.mu.Unlock()
		s.changed()
	case u.AvailableCommandsUpdate != nil:
		s.mu.Lock()
		s.st.Commands = u.AvailableCommandsUpdate.AvailableCommands
		s.mu.Unlock()
		s.changed()
		return // not logged: dozens of commands, re-sent whenever the agent restarts
	case u.ConfigOptionUpdate != nil:
		s.mu.Lock()
		s.st.Config = u.ConfigOptionUpdate.ConfigOptions
		s.mu.Unlock()
		s.changed()
		return
	case u.SessionInfoUpdate != nil:
		if g, ok := u.SessionInfoUpdate.Meta[acpx.MetaKey].(map[string]any); ok {
			s.onInfo(g)
		}
		return
	case u.UsageUpdate != nil:
		return
	}
	raw, err := json.Marshal(u)
	if err != nil {
		return
	}
	s.emit(&Event{Kind: EvUpdate, Update: raw})
	if u.AgentMessageChunk != nil || u.ToolCall != nil {
		s.flush(false)
	}
}

func (s *Session) onInfo(g map[string]any) {
	changed := false
	if p, ok := g[acpx.ProgressKey].(map[string]any); ok {
		s.mu.Lock()
		if n := num(p["outputTokens"]); n > 0 {
			s.st.TurnTokens = int(n)
		}
		if n := num(p["contextTokens"]); n > 0 {
			s.st.ContextTokens = int(n)
		}
		if n := num(p["contextWindow"]); n > 0 {
			s.st.ContextWindow = int(n)
		}
		s.mu.Unlock()
		changed = true
	}
	if ev := NoticeFromInfo(g, now()); ev != nil {
		s.emit(ev)
	}
	if id, state, ok := SteerFromInfo(g); ok {
		s.mu.Lock()
		_, early := s.steering[id]
		if early {
			s.steering[id] = state
		}
		steer := false
		for _, e := range s.events {
			if e.Kind == EvUser && e.MessageID == id {
				steer = e.Steer
			}
		}
		s.mu.Unlock()
		if !early && steer {
			s.emit(&Event{Kind: EvSteerState, MessageID: id, Text: state})
		}
	}
	if id, _ := g["sessionId"].(string); id != "" {
		s.mu.Lock()
		s.st.SessionID = id
		cleared, _ := g["cleared"].(bool)
		if cleared {
			s.st.ContextTokens = 0
		}
		s.mu.Unlock()
		if cleared {
			s.emit(&Event{Kind: EvNotice, Level: "clear", Text: "上下文已清空"})
		}
		changed = true
	}
	if list, ok := TasksFromInfo(g); ok {
		s.mu.Lock()
		prev := s.st.Tasks
		s.st.Tasks = list
		s.mu.Unlock()
		for _, ev := range FinishedTasks(prev, list) {
			s.emit(ev)
		}
		changed = true
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
			s.running, s.auto = true, true
			s.st.Error = ""
			s.st.TurnStarted, s.st.TurnTokens = now(), 0
		}
		var dur int64
		var tok int
		if end {
			dur, tok = now()-s.st.TurnStarted, s.st.TurnTokens
			s.running, s.auto = false, false
			for id, p := range s.pending {
				close(p.ch)
				delete(s.pending, id)
			}
		}
		s.mu.Unlock()
		if start {
			s.emit(&Event{Kind: EvTurnStart, Auto: true})
		}
		if end {
			s.emit(&Event{Kind: EvTurnEnd, StopReason: string(acp.StopReasonEndTurn), Auto: true, DurationMs: dur, OutputTokens: tok})
			s.flush(true)
		}
		changed = changed || start || end
	}
	if changed {
		s.changed()
	}
}

// RequestPermission records a permission request and waits for Answer ({"optionId": …} or {"cancelled": true}).
// Wire it as the client's RequestPermission.
func (s *Session) RequestPermission(ctx context.Context, req acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	cancelled := acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeCancelled()}
	reqID, p := s.open("permission", req)
	select {
	case ans, ok := <-p.ch:
		if !ok {
			s.resolve(reqID, EvPermissionDone)
			return cancelled, nil
		}
		var a struct {
			OptionID  string `json:"optionId"`
			Cancelled bool   `json:"cancelled"`
		}
		_ = json.Unmarshal(ans, &a)
		if a.Cancelled || a.OptionID == "" {
			return cancelled, nil
		}
		return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeSelected(acp.PermissionOptionId(a.OptionID))}, nil
	case <-ctx.Done():
		s.resolve(reqID, EvPermissionDone)
		return cancelled, nil
	}
}

// Ask records a question (the agent's _gerdr/ask request) and waits for Answer (an acpx.AskResponse).
func (s *Session) Ask(ctx context.Context, req acpx.AskRequest) (acpx.AskResponse, error) {
	reqID, p := s.open("ask", req)
	select {
	case ans, ok := <-p.ch:
		if !ok {
			s.resolve(reqID, EvAskDone)
			return acpx.AskResponse{Cancelled: true}, nil
		}
		var res acpx.AskResponse
		_ = json.Unmarshal(ans, &res)
		return res, nil
	case <-ctx.Done():
		s.resolve(reqID, EvAskDone)
		return acpx.AskResponse{Cancelled: true}, nil
	}
}

func (s *Session) open(kind string, req any) (string, *pendingReq) {
	reqID := uuid.NewString()
	raw, _ := json.Marshal(req)
	p := &pendingReq{kind: kind, ch: make(chan json.RawMessage, 1), req: raw, at: now()}
	s.mu.Lock()
	s.pending[reqID] = p
	s.mu.Unlock()
	ev := EvPermission
	if kind == "ask" {
		ev = EvAsk
	}
	s.emit(&Event{Kind: ev, ReqID: reqID, Request: raw})
	s.changed()
	return reqID, p
}

// resolve logs a request as void (unless it was answered) and forgets it.
func (s *Session) resolve(reqID, kind string) {
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
		s.emit(&Event{Kind: kind, ReqID: reqID, Response: json.RawMessage(`{"cancelled":true}`)})
	}
	if had {
		s.changed()
	}
}

// Answer answers an open permission request ({"optionId": "allow_once"} …) or question (acpx.AskResponse JSON).
func (s *Session) Answer(reqID string, resp json.RawMessage) error {
	s.mu.Lock()
	p := s.pending[reqID]
	delete(s.pending, reqID)
	s.mu.Unlock()
	if p == nil {
		return errors.New("this request is no longer open")
	}
	kind := EvPermissionDone
	if p.kind == "ask" {
		kind = EvAskDone
	}
	s.emit(&Event{Kind: kind, ReqID: reqID, Response: resp})
	p.ch <- resp
	s.changed()
	return nil
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

func oneLine(t string, n int) string {
	t = strings.Join(strings.Fields(t), " ")
	if r := []rune(t); len(r) > n {
		t = string(r[:n]) + "…"
	}
	return t
}
