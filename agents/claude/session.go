package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/coder/acp-go-sdk"
	"github.com/google/uuid"

	"github.com/gerdr-dev/gerdr/acpx"
)

type session struct {
	a *Agent

	meta map[string]any  // session/new's _meta (Options.AppendPrompt / SessionEnv)
	mcp  []acp.McpServer // session/new's mcpServers, passed to claude with --mcp-config

	mu   sync.Mutex
	id   string // 当前 claude 会话 id（= ACP sessionId；回退分叉后会变）
	cwd  string
	cmds []cliCommand // 这个会话的进程 initialize 报的命令（learnSkills 重推时用）

	mode    string
	model   string
	effort  string
	respawn bool   // 下一轮前换个进程（改了只能在启动参数里给的东西）
	extFP   string // 起进程时 skill / MCP 设置的指纹；对不上就换进程
	// 起进程时 claude 自己配的 MCP 的指纹（ext.ClaudeMCPFingerprint）。对不上只在 /clear 时换进程：
	// 中途换的话工具列表变了，提示缓存全废，长对话下一轮整段重新计费；清空之后上下文是空的，换了不亏。
	mcpFP string

	// 起进程时怎么接上对话：
	fresh    bool   // 全新：--session-id id
	forkFrom string // 回退：--resume forkFrom --resume-session-at forkAt --fork-session --session-id id
	forkAt   string

	p          *proc
	exiting    *proc // 回退时放到后台退的旧进程（就是 p，还没退完）：别再往它发东西，下一轮先等它退完再起新的
	spawning   *proc // 起了、还没 initialize 完的进程（还没挂到 p 上）：它已经写了登记文件，ownPids 要认它，见 holders.go
	starting   chan struct{}
	abortWait  context.CancelFunc // 有一句话在等进程起来（还没发给 claude）时：打断就调它，这句作废
	turn       *turnState
	openInputs int // 正在等人回答的审批 / 提问数（插话的优先级看它）
	lastUsed   time.Time
	sawState   bool // 这个进程报过 session_state_changed（老版本 CLI 没有，退回看 result）

	tasks                map[string]*acpx.Task // 后台任务 / 子 agent（tasks.go）
	auto                 bool                  // CLI 自己开的一轮正在跑（没有 session/prompt 对应）
	tok                  acpx.TokenMeter       // 这一轮输出了多少 token（状态行）
	ctxTokens, ctxWindow int                   // 上下文用量（最近一次请求的输入、模型窗口）

	tools    map[string]*toolState
	msgDelta map[string]bool // 哪些 API 消息已经按 delta 推过正文了

	emitHook  func(acp.SessionUpdate) // 重放会话记录时把 update 改道
	lastTitle string                  // 上次报给 hub 的标题（syncTitle 去重）
}

func (s *session) withEmit(emit func(acp.SessionUpdate), fn func()) {
	s.emitHook = emit
	defer func() { s.emitHook = nil }()
	fn()
}

type turnState struct {
	done       chan turnResult
	id         string // 这一轮那句话的 uuid（msgID 是 UUID 才有）：认 command_lifecycle 里哪条是它
	sawRunning bool
	cancelled  bool
	errText    string
	stop       acp.StopReason
}

type turnResult struct {
	stop acp.StopReason
	err  error
}

type toolState struct {
	name   string
	parent string
	input  map[string]any
}

// ensure 保证进程在跑（必要时起一个，并做 initialize 握手）。
func (s *session) ensure(ctx context.Context) error {
	fp := ""
	if s.a.opts.Ext != nil {
		fp = s.a.opts.Ext.Fingerprint()
	}
	for {
		s.mu.Lock()
		if s.p != nil && s.p.alive() && s.p != s.exiting && !s.respawn && (s.extFP == fp || s.turn != nil) {
			s.mu.Unlock()
			return nil
		}
		if s.starting != nil {
			ch := s.starting
			s.mu.Unlock()
			select {
			case <-ch:
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		old := s.p
		s.p = nil
		s.respawn = false
		ch := make(chan struct{})
		s.starting = ch
		s.mu.Unlock()
		if old != nil {
			old.stop()
		}
		err := s.start(ctx)
		s.mu.Lock()
		s.starting = nil
		close(ch)
		s.mu.Unlock()
		return err
	}
}

func (s *session) start(ctx context.Context) error {
	bin, err := s.a.bin()
	if err != nil {
		return err
	}
	s.mu.Lock()
	o := spawnOpts{bin: bin, cwd: s.cwd, model: s.model, mode: s.mode, effort: s.effort, env: s.a.opts.Env, prompt: s.a.opts.Prompt, noHint: s.a.opts.NoGUIHint}
	if f := s.a.opts.AppendPrompt; f != nil {
		if p := strings.TrimSpace(f(s.id, s.cwd, s.meta)); p != "" {
			o.prompt = strings.TrimSpace(o.prompt + "\n\n" + p)
		}
	}
	if f := s.a.opts.SessionEnv; f != nil {
		o.env = append(append([]string(nil), o.env...), f(s.id, s.meta)...)
	}
	s.mcpFP = s.a.mcpFingerprint(s.cwd)
	if e := s.a.opts.Ext; e != nil {
		s.extFP = e.Fingerprint()
		o.extra = e.Args(s.id, s.cwd)
	}
	o.extra = append(o.extra, s.clientMCPArgs()...)
	o.extra = append(o.extra, s.a.opts.Args...)
	fresh, from, at := s.pendingLocked()
	switch {
	case from != "":
		o.resume, o.resumeAt, o.fork, o.sessionID = from, at, true, s.id
	case fresh:
		o.sessionID = s.id
	default:
		// 建了会话但一句话都没说过：claude 还没写会话记录，--resume 会报 "No conversation found"。
		// 当成新会话（同一个 id）起。
		if _, err := findTranscript(s.a.projectsDir(), s.id, s.cwd); err != nil {
			o.sessionID = s.id
		} else {
			o.resume = s.id
		}
	}
	s.sawState = false
	s.mu.Unlock()

	var pr *proc
	pr, err = spawn(o, s.a.log.With("session", o.sessionID+o.resume), func(m map[string]json.RawMessage, raw []byte) {
		s.onLine(pr, m, raw)
	})
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.spawning = pr
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if s.spawning == pr {
			s.spawning = nil
		}
		s.mu.Unlock()
	}()
	go func() {
		<-pr.done
		s.onExit(pr)
	}()
	ictx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	res, err := pr.control(ictx, map[string]any{"subtype": "initialize"})
	if err != nil {
		pr.stop()
		if e := pr.err(); e != nil {
			return fmt.Errorf("claude 启动失败：%w", e)
		}
		return fmt.Errorf("claude initialize: %w", err)
	}
	var info struct {
		Models   []cliModel   `json:"models"`
		Commands []cliCommand `json:"commands"`
		Mode     string       `json:"current_permission_mode"`
	}
	_ = json.Unmarshal(res, &info)
	s.a.mu.Lock()
	if len(info.Models) > 0 {
		s.a.models = info.Models
	}
	if len(info.Commands) > 0 {
		s.a.commands = info.Commands
	}
	if info.Mode != "" && s.a.defMode == "" {
		s.a.defMode = info.Mode
	}
	s.a.mu.Unlock()

	s.mu.Lock()
	s.p = pr
	// 「怎么接上」的那几个标记不在这里清：进程起来了不等于分叉成了 —— claude 要等这句话写进去才有新会话的记录，
	// 这句被 Esc 撤回、进程又被收掉的话，下次还得照样分叉（以前在这里清，下次起成一个空会话，前面的对话全丢）。
	// 记录有了才算数，见 pendingLocked。
	if s.mode == "" {
		s.mode = info.Mode
	}
	s.lastUsed = time.Now()
	s.mu.Unlock()
	// 模型列表要等第一个进程起来才知道（gerdr 重启后接回的会话，Resume 时还没有）：补推一次。
	if opts := s.a.configOptions(s); len(opts) > 0 {
		s.update(context.Background(), acp.SessionUpdate{ConfigOptionUpdate: &acp.SessionConfigOptionUpdate{ConfigOptions: opts}})
	}
	if len(info.Commands) > 0 {
		s.mu.Lock()
		s.cmds = info.Commands
		s.mu.Unlock()
		s.a.mu.Lock()
		cmds := s.a.acpCommands(info.Commands)
		s.a.mu.Unlock()
		s.update(context.Background(), acp.SessionUpdate{AvailableCommandsUpdate: &acp.SessionAvailableCommandsUpdate{AvailableCommands: cmds}})
	}
	return nil
}

// pendingLocked：这个会话是不是还是个「打算」（回退出来、claude 还没写出它的记录）：是的话返回怎么起它。
// 记录文件有了就是分叉成了，标记作废，以后一律按 id --resume。持锁调。
func (s *session) pendingLocked() (fresh bool, from, at string) {
	if !s.fresh && s.forkFrom == "" {
		return false, "", ""
	}
	if _, err := findTranscript(s.a.projectsDir(), s.id, s.cwd); err == nil {
		s.fresh, s.forkFrom, s.forkAt = false, "", ""
		return false, "", ""
	}
	return s.fresh, s.forkFrom, s.forkAt
}

// live：还能往里发控制命令的进程（在跑、也不是正在退的那个）；没有就是 nil。持锁调。
func (s *session) live() *proc {
	if s.p == nil || s.p == s.exiting || !s.p.alive() {
		return nil
	}
	return s.p
}

func (s *session) onExit(pr *proc) {
	s.mu.Lock()
	if s.exiting == pr {
		s.exiting = nil
	}
	if s.p != pr {
		s.mu.Unlock()
		return
	}
	s.p = nil
	t := s.turn
	s.turn = nil
	s.openInputs = 0
	auto := s.auto
	s.auto = false
	// 进程没了，它的后台任务也跟着没了。
	for _, x := range s.tasks {
		if x.Status == "running" {
			x.Status, x.EndedAt = "killed", time.Now().UnixMilli()
		}
	}
	list := s.taskListLocked()
	s.mu.Unlock()
	if auto {
		s.autoTurn(context.Background(), false)
	}
	if len(list) > 0 {
		s.update(context.Background(), acp.SessionUpdate{SessionInfoUpdate: &acp.SessionSessionInfoUpdate{Meta: acpx.Meta(map[string]any{"tasks": list})}})
	}
	if t != nil {
		if t.cancelled {
			t.done <- turnResult{stop: acp.StopReasonCancelled}
		} else {
			t.done <- turnResult{err: fmt.Errorf("claude 进程意外退出：%v", pr.err())}
		}
	}
}

func (s *session) kill() {
	s.mu.Lock()
	pr := s.p
	s.mu.Unlock()
	if pr != nil {
		pr.stop()
	}
}

// adoptID：claude 自己换了会话 id —— /clear 就是这样（CLI 先发一条 conversation_reset，接着用一个新 id 再 init 一次，
// 之后的对话都写进新 id 的记录文件）。不跟过去的话：回退去旧文件里找消息，报「message not found in transcript」；
// 进程闲置被收掉、下次 --resume 的还是旧 id，清掉的上下文原样回来（两个都是用户报的 / 实测的）。
// 先用旧 id 通知 hub（它按旧 id 认这个会话）把记录改成新 id，再在这边换；旧 id 留作别名，正在路上的调用照样找得到。
func (s *session) adoptID(ctx context.Context, id string) {
	s.mu.Lock()
	old := s.id
	s.mu.Unlock()
	if id == old {
		return
	}
	s.update(ctx, acp.SessionUpdate{SessionInfoUpdate: &acp.SessionSessionInfoUpdate{Meta: acpx.Meta(map[string]any{"sessionId": id, "cleared": true})}})
	// A /clear typed while a turn was running goes in as a steer, so prompt()'s MCP check never saw it:
	// the cleared context would stay on a process holding the old MCP config (a server added or fixed
	// with `claude mcp add` mid-session stayed "needs authentication"). Check again here, where every
	// /clear ends up, and swap the process before the next turn.
	fp := s.a.mcpFingerprint(s.cwd)
	s.mu.Lock()
	s.id = id
	s.fresh, s.forkFrom, s.forkAt = false, "", ""
	s.ctxTokens = 0 // the cleared context's: the /clear's own result must not report it again for the new one
	if s.mcpFP != fp {
		s.respawn = true
	}
	s.mu.Unlock()
	s.a.mu.Lock()
	s.a.sessions[id] = s
	s.a.mu.Unlock()
}

func (s *session) update(ctx context.Context, u acp.SessionUpdate) {
	if s.emitHook != nil {
		s.emitHook(u)
		return
	}
	if s.a.conn == nil {
		return
	}
	s.mu.Lock()
	id := s.id
	s.mu.Unlock()
	if err := s.a.conn.SessionUpdate(ctx, acp.SessionNotification{SessionId: acp.SessionId(id), Update: u}); err != nil {
		s.a.log.Debug("session update failed", "err", err)
	}
}

func (s *session) prompt(ctx context.Context, blocks []acp.ContentBlock, msgID string) (acp.PromptResponse, error) {
	// 等进程起来（回退之后的第一句要现起一个、带着分叉参数 resume，零点几秒到一秒多）的这段时间里也要能打断：
	// 这时还没有 turn，cancel 落空，人按了 Esc 要干等进程起来、话发进去再被打断（用户报的「按 Esc 卡一秒」）。
	// 所以等的时候能被 cancel 叫停，这句不发了；进程照样在后台起好，下一句直接用。
	wctx, abort := context.WithCancel(ctx)
	defer abort()
	s.mu.Lock()
	s.abortWait = abort
	s.mu.Unlock()
	if isClear(blocks) {
		fp := s.a.mcpFingerprint(s.cwd)
		s.mu.Lock()
		if s.p != nil && s.turn == nil && s.mcpFP != fp { // 正在跑的那一轮不能被换掉（这句随后也会被拒）
			s.respawn = true
		}
		s.mu.Unlock()
	}
	ready := make(chan error, 1)
	go func() { ready <- s.ensure(context.WithoutCancel(ctx)) }()
	var err error
	select {
	case err = <-ready:
	case <-wctx.Done():
	}
	if err != nil && wctx.Err() == nil {
		s.mu.Lock()
		s.abortWait = nil
		s.mu.Unlock()
		return acp.PromptResponse{}, err
	}
	t := &turnState{done: make(chan turnResult, 1)}
	if _, err := uuid.Parse(msgID); err == nil {
		t.id = msgID // 和 userMessage 带的 uuid 一致
	}
	s.tok.Reset()
	s.mu.Lock()
	// 清 abortWait 和挂上 turn 在同一把锁里：中间不留空档，这时来的打断要么作废这句、要么打断这一轮
	s.abortWait = nil
	if wctx.Err() != nil {
		s.mu.Unlock()
		return acp.PromptResponse{StopReason: acp.StopReasonCancelled}, nil
	}
	if s.turn != nil {
		s.mu.Unlock()
		return acp.PromptResponse{}, acp.NewInvalidRequest("a turn is already running; use _session/steering")
	}
	s.turn = t
	pr := s.p
	s.lastUsed = time.Now()
	s.mu.Unlock()

	if err := pr.send(userMessage(blocks, msgID, "")); err != nil {
		s.mu.Lock()
		s.turn = nil
		s.mu.Unlock()
		return acp.PromptResponse{}, err
	}
	var r turnResult
	select {
	case r = <-t.done:
	case <-ctx.Done():
		s.cancel()
		r = <-t.done
	}
	s.mu.Lock()
	s.lastUsed = time.Now()
	s.mu.Unlock()
	if r.err != nil {
		return acp.PromptResponse{}, acp.NewInternalError(map[string]any{"message": r.err.Error()})
	}
	return acp.PromptResponse{StopReason: r.stop}, nil
}

// steer：把消息插进正在跑的那一轮。默认 priority "next"，和 Claude Code 自己的输入框一样（它运行中回车排队用的就是 next）：
// 等当前这步（比如正在跑的工具）做完，到下一个空档塞进去，不截断。人点「立即发送」/ Ctrl+Enter 时用 "now"
// （截断当前这次生成，马上处理）。原来默认就是 now，一插话正在跑的工具调用就被掐成「未完成」（用户报的）。
// 正等人回答审批 / 提问时一律 "later" —— "now" 会把那个等待中的回调掐掉，界面上的卡片撤了，
// 这一轮就卡住了（claude-agent-acp 踩过，IJAI-1191）；next 会不会也掐没验证过，不冒这个险。
func (s *session) steer(blocks []acp.ContentBlock, msgID string, now bool) (acpx.SteerResponse, error) {
	s.mu.Lock()
	t, pr, open, auto := s.turn, s.p, s.openInputs, s.auto
	s.mu.Unlock()
	// CLI 自己开的那一轮（后台任务跑完了在汇报）也能插话：它正在跑，消息照样排进去。
	if (t == nil && !auto) || pr == nil {
		return acpx.SteerResponse{Outcome: acpx.SteerPromptRequired}, nil
	}
	prio := "next"
	if open > 0 {
		prio = "later"
	} else if now {
		prio = "now"
	}
	if err := pr.send(userMessage(blocks, msgID, prio)); err != nil {
		return acpx.SteerResponse{}, err
	}
	return acpx.SteerResponse{Outcome: acpx.SteerInjected}, nil
}

func (s *session) cancel() {
	s.mu.Lock()
	t, pr, auto, abortWait := s.turn, s.p, s.auto, s.abortWait
	if t != nil {
		t.cancelled = true
	}
	s.mu.Unlock()
	if t == nil && abortWait != nil {
		abortWait()
		return
	}
	if pr == nil {
		return
	}
	if t == nil {
		if auto {
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				_, _ = pr.control(ctx, map[string]any{"subtype": "interrupt"})
			}()
		}
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := pr.control(ctx, map[string]any{"subtype": "interrupt"}); err != nil {
			s.a.log.Warn("interrupt failed", "err", err)
		}
		// 兜底：打断之后 30 秒还没回到 idle（卡死了），直接收掉进程，下一轮 --resume 回来。
		select {
		case <-time.After(30 * time.Second):
			s.mu.Lock()
			still := s.turn == t
			s.mu.Unlock()
			if still {
				s.a.log.Warn("interrupt did not settle; killing claude")
				pr.stop()
			}
		case <-pr.done:
		}
	}()
}

func (s *session) finishTurn(stop acp.StopReason) {
	s.mu.Lock()
	t := s.turn
	s.turn = nil
	s.mu.Unlock()
	if t == nil {
		return
	}
	if t.cancelled {
		stop = acp.StopReasonCancelled
	}
	if t.errText != "" && stop != acp.StopReasonCancelled {
		t.done <- turnResult{err: errors.New(t.errText)}
		return
	}
	t.done <- turnResult{stop: stop}
	go s.syncTitle(3 * time.Second)
}

// syncTitle：把 Claude Code 自己的标题带到 gerdr（pane 顶部、侧栏）。它起标题是异步的，常常在一轮结束后才写进记录，
// 所以等一会儿再读。/rename 的算人改的（renamed，以后不被自动标题盖）；ai-title 只在 gerdr 里没人改过名时用上（hub 判断）。
// gerdr 起的会话（stream-json 模式）claude 不写 ai-title（那是交互界面里才做的事），这种会话的标题由 hub 起（hub/title.go）。
func (s *session) syncTitle(delay time.Duration) {
	time.Sleep(delay)
	s.mu.Lock()
	id, cwd, last := s.id, s.cwd, s.lastTitle
	s.mu.Unlock()
	path, err := findTranscript(s.a.projectsDir(), id, cwd)
	if err != nil {
		return
	}
	custom, ai := titles(path)
	if custom != "" && custom == inheritedTitle(path) {
		custom = "" // carried over from before a /clear: that context's name, not this one's (hub/title.go)
	}
	t, renamed := ai, false
	if custom != "" {
		t, renamed = custom, true
	}
	if t == "" {
		return
	}
	if t == last {
		return
	}
	s.mu.Lock()
	s.lastTitle = t
	s.mu.Unlock()
	u := &acp.SessionSessionInfoUpdate{Title: &t}
	if renamed {
		u.Meta = acpx.Meta(map[string]any{"renamed": true})
	}
	s.update(context.Background(), acp.SessionUpdate{SessionInfoUpdate: u})
}

// userMessage 拼一条写进 stdin 的 user 消息。
//
// uuid：用 gerdr 的 messageId（是 UUID 的话）。CLI 会把它原样存成会话记录里这条消息的
// uuid —— 回退时靠它在记录里找到这条消息。origin 必须显式标 human：没标的会被当成来路
// 不明，在一些「只信人类输入」的检查上直接拒掉。
func userMessage(blocks []acp.ContentBlock, msgID, priority string) map[string]any {
	m := map[string]any{
		"type":               "user",
		"message":            map[string]any{"role": "user", "content": toClaudeContent(blocks)},
		"parent_tool_use_id": nil,
		"session_id":         "",
		"origin":             map[string]any{"kind": "human"},
	}
	if _, err := uuid.Parse(msgID); err == nil {
		m["uuid"] = msgID
	}
	if priority != "" {
		m["priority"] = priority
	}
	return m
}

// isClear：这句是不是 /clear（整句就这一个命令）。
func isClear(blocks []acp.ContentBlock) bool {
	return len(blocks) == 1 && blocks[0].Text != nil && strings.TrimSpace(blocks[0].Text.Text) == "/clear"
}

func toClaudeContent(blocks []acp.ContentBlock) []map[string]any {
	var out []map[string]any
	for _, b := range blocks {
		switch {
		case b.Text != nil:
			out = append(out, map[string]any{"type": "text", "text": b.Text.Text})
		case b.Image != nil:
			out = append(out, map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": b.Image.MimeType, "data": b.Image.Data}})
		case b.ResourceLink != nil:
			out = append(out, map[string]any{"type": "text", "text": "@" + strings.TrimPrefix(b.ResourceLink.Uri, "file://")})
		case b.Resource != nil && b.Resource.Resource.TextResourceContents != nil:
			r := b.Resource.Resource.TextResourceContents
			out = append(out, map[string]any{"type": "text", "text": fmt.Sprintf("<file path=%q>\n%s\n</file>", strings.TrimPrefix(r.Uri, "file://"), r.Text)})
		}
	}
	if len(out) == 0 {
		out = append(out, map[string]any{"type": "text", "text": ""})
	}
	return out
}
