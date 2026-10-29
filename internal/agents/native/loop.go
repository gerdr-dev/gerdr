package native

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/acp-go-sdk"
	"github.com/google/uuid"

	"github.com/gerdr-dev/gerdr/internal/acpx"
	"github.com/gerdr-dev/gerdr/internal/agents/native/llm"
)

// 主循环：调模型 → 有工具调用就执行、把结果接上再调 → 没有了就结束。
//
// 插话（_session/steering）不打断正在跑的工具：排进 s.queue，下一次调模型之前接到历史末尾（Claude Code 输入框的
// 默认行为）；Now=true 时连正在流的那次生成一起掐掉（已经出来的字留着）马上带着它重调。模型说完了、队里还有话，
// 接着调 —— 不会出现「插的那句话没人回」。
//
// 打断（session/cancel）：掐掉生成和正在跑的命令，**把这批工具调用的结果补齐**（没跑完的写「被打断」）再结束 ——
// 历史里有 tool_call 没 tool 结果，下一轮两家接口都会 400。
//
// 子 agent（subagent.go）跑的是同一个循环，只是历史在内存里、没有插话和压缩。

// run 是一次循环的执行者：主会话的一轮，或者一个子 agent。
type run struct {
	s      *session
	sub    *subRun // nil = 主会话
	model  resolved
	effort string
	tools  []*tool
	system string

	msgs []llm.Message // 子 agent 的历史（主会话的在 s.entries）
	last string        // 子 agent 最后说的话（交回给主 agent）

	// 本次调模型的流式状态
	tok   *acpx.TokenMeter
	reqN  int
	input int // 最近一次请求的输入 token（上下文多大）
}

func (r *run) out(u acp.SessionUpdate) {
	if r.sub != nil {
		r.sub.out(r, u)
		return
	}
	r.s.send(u)
}

func (r *run) toolByName(name string) *tool {
	for _, t := range r.tools {
		if t.name == name {
			return t
		}
	}
	return nil
}

func (r *run) history() []llm.Message {
	if r.sub != nil {
		return r.msgs
	}
	return r.s.contextMessages()
}

func (r *run) add(m llm.Message, id string, ui *toolUI) {
	if r.sub != nil {
		r.msgs = append(r.msgs, m)
		return
	}
	r.s.addMsg(m, id, ui)
}

// errStopped：这一轮被打断了（不是出错）。
var errStopped = errors.New("stopped")

// loop 跑到模型不再调工具（且没有排队的插话）为止。
func (r *run) loop(ctx context.Context) error {
	for step := 0; ; step++ {
		if ctx.Err() != nil {
			return errStopped
		}
		if r.sub == nil {
			r.s.drainQueue()
			if err := r.maybeCompact(ctx); err != nil {
				if ctx.Err() != nil {
					return errStopped
				}
				return err
			}
		}
		res, interrupted, err := r.callModel(ctx)
		if err != nil {
			if ctx.Err() != nil {
				r.keepPartial(res)
				return errStopped
			}
			r.keepPartial(res)
			return err
		}
		if interrupted {
			// 插话要立刻处理：已经说出来的留着，带着插的话重调
			r.keepPartial(res)
			continue
		}
		msg := res.Message
		r.add(msg, "", nil)
		if len(msg.ToolCalls) == 0 {
			if r.sub != nil {
				r.last = msg.Text
				return nil
			}
			if r.s.hasQueued() {
				continue
			}
			if res.Stop == "length" && msg.Text == "" {
				return errors.New("the model hit its output token limit without answering")
			}
			return nil
		}
		if err := r.runTools(ctx, msg.ToolCalls); err != nil {
			return err
		}
	}
}

// keepPartial：被打断 / 出错时已经流出来的正文留在历史里（没有工具调用：半截的调用不能执行）。
func (r *run) keepPartial(res llm.Result) {
	m := res.Message
	m.ToolCalls = nil
	if strings.TrimSpace(m.Text) == "" {
		return
	}
	m.Reasoning = nil // 半截的不透明推理还回去会被拒
	r.add(m, "", nil)
}

// callModel 调一次模型，边收边推给界面。interrupted = 被 Now 插话掐断了（不算错）。
func (r *run) callModel(ctx context.Context) (llm.Result, bool, error) {
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if r.sub == nil {
		r.s.setStreamCancel(cancel)
		defer r.s.setStreamCancel(nil)
	}
	sctx = llm.WithRetryNotice(sctx, func(err error, attempt int, wait time.Duration) {
		// retry 级别：界面原地更新同一行、重试成功（又出字）就收起；不带级别会按 warn 每次重试新起一行
		r.s.send(acp.SessionUpdate{SessionInfoUpdate: &acp.SessionSessionInfoUpdate{Meta: acpx.Meta(map[string]any{
			"notice":      fmt.Sprintf("模型接口出错：%s（%d 秒后第 %d 次重试）", oneLine(err.Error(), 160), int(wait.Seconds()), attempt),
			"noticeLevel": "retry",
		})}})
	})
	msgID := uuid.NewString()
	key := strconv.Itoa(r.reqN)
	r.reqN++
	req := llm.Request{
		Model: r.model.ID, System: r.system, Messages: r.history(), Tools: toolDefs(r.tools),
		Effort: r.effortValue(), Thinking: r.model.Thinking, CacheKey: r.s.currentID(),
	}
	res, err := r.model.client().Stream(sctx, req, func(e llm.Event) {
		switch e.Kind {
		case "text":
			r.out(acp.SessionUpdate{AgentMessageChunk: &acp.SessionUpdateAgentMessageChunk{Content: acp.TextBlock(e.Text), MessageId: acp.Ptr(msgID)}})
		case "thought":
			r.out(acp.SessionUpdate{AgentThoughtChunk: &acp.SessionUpdateAgentThoughtChunk{Content: acp.TextBlock(e.Text), MessageId: acp.Ptr(msgID)}})
		}
		if r.tok != nil && (e.Kind == "text" || e.Kind == "thought" || e.Kind == "tool_args") {
			r.tok.Chars(key, len(e.Text))
			if r.tok.Due() {
				r.s.send(acp.SessionUpdate{SessionInfoUpdate: &acp.SessionSessionInfoUpdate{Meta: acpx.Progress(r.tok.Total())}})
			}
		}
	})
	if res.Usage.Output > 0 && r.tok != nil {
		r.tok.Exact(key, res.Usage.Output)
		r.s.send(acp.SessionUpdate{SessionInfoUpdate: &acp.SessionSessionInfoUpdate{Meta: acpx.Progress(r.tok.Total())}})
	}
	if res.Usage.Input > 0 {
		r.input = res.Usage.Input
		if r.sub == nil {
			r.s.setLastInput(res.Usage.Input)
			r.s.send(acp.SessionUpdate{SessionInfoUpdate: &acp.SessionSessionInfoUpdate{Meta: acpx.ContextProgress(res.Usage.Input, r.model.Window)}})
		}
	}
	if err != nil && ctx.Err() == nil && sctx.Err() != nil {
		return res, true, nil // 只掐了这次生成：Now 插话
	}
	return res, false, err
}

func (r *run) effortValue() string {
	e := r.effort
	if e == "" {
		e = r.model.DefaultEffort
	}
	if len(r.model.Efforts) > 0 {
		ok := false
		for _, x := range r.model.Efforts {
			if x == e {
				ok = true
			}
		}
		if !ok {
			e = r.model.DefaultEffort
		}
	}
	return e
}

func (r *run) notice(text string) {
	r.s.send(acp.SessionUpdate{SessionInfoUpdate: &acp.SessionSessionInfoUpdate{Meta: acpx.Meta(map[string]any{"notice": text})}})
}

func toolDefs(ts []*tool) []llm.Tool {
	out := make([]llm.Tool, 0, len(ts))
	for _, t := range ts {
		out = append(out, llm.Tool{Name: t.name, Description: t.desc, Parameters: t.params})
	}
	return out
}

// ---- 执行工具 ----

type pending struct {
	tc   llm.ToolCall
	t    *tool
	args map[string]any
	res  result
	ui   *toolUI
	done bool
}

// runTools 执行一批工具调用，结果按原顺序接进历史。全是只读的（读、搜、子 agent）就并发跑，否则按顺序。
func (r *run) runTools(ctx context.Context, calls []llm.ToolCall) error {
	ps := make([]*pending, len(calls))
	parallel := true
	for i, tc := range calls {
		p := &pending{tc: tc, t: r.toolByName(tc.Name)}
		ps[i] = p
		if p.t == nil {
			p.res, p.done = errResult("unknown tool %q", tc.Name), true
			continue
		}
		if strings.TrimSpace(tc.Args) == "" {
			tc.Args = "{}"
		}
		if err := json.Unmarshal([]byte(tc.Args), &p.args); err != nil {
			p.res, p.done = errResult("invalid JSON arguments: %v", err), true
			continue
		}
		if !p.t.readOnly {
			parallel = false
		}
	}
	// 先把这批调用都摆到界面上（等着跑的显示成 pending）
	for _, p := range ps {
		r.start(p)
	}
	exec := func(p *pending) {
		if p.done {
			return
		}
		p.res = r.exec(ctx, p)
		p.done = true
	}
	if parallel {
		var wg sync.WaitGroup
		for _, p := range ps {
			wg.Add(1)
			go func() { defer wg.Done(); exec(p) }()
		}
		wg.Wait()
	} else {
		for _, p := range ps {
			if ctx.Err() != nil {
				break
			}
			exec(p)
		}
	}
	stopped := ctx.Err() != nil
	for _, p := range ps {
		if !p.done || (stopped && p.res.text == "") {
			p.res = result{text: "Interrupted by the user before this tool call finished.", err: true}
		}
		r.finish(p)
	}
	if stopped {
		return errStopped
	}
	return nil
}

func (r *run) start(p *pending) {
	title, kind, locs := p.tc.Name, acp.ToolKindOther, []acp.ToolCallLocation(nil)
	if p.t != nil && p.t.show != nil {
		title, kind, locs = p.t.show(p.args, r.s.cwd())
	}
	if p.t != nil && (p.t.name == "write" || p.t.name == "edit") {
		if path := absPath(str(p.args, "path"), r.s.cwd()); r.s.memory().inMemory(path) {
			title = memoryTitle(r.s.memory(), path)
		}
	}
	p.ui = &toolUI{Title: title, Kind: kind, Input: p.args, Locs: locs}
	if p.t != nil && p.t.hidden {
		return
	}
	opts := []acp.ToolCallStartOpt{acp.WithStartKind(kind), acp.WithStartStatus(acp.ToolCallStatusPending), acp.WithStartRawInput(p.args)}
	if len(locs) > 0 {
		opts = append(opts, acp.WithStartLocations(locs))
	}
	r.out(acp.StartToolCall(acp.ToolCallId(p.tc.ID), title, opts...))
}

func (r *run) finish(p *pending) {
	st := acp.ToolCallStatusCompleted
	if p.res.err {
		st = acp.ToolCallStatusFailed
	}
	disp := p.res.display
	if disp == nil {
		disp = []acp.ToolCallContent{acp.ToolContent(acp.TextBlock(tailStr(p.res.text, 64<<10)))}
	}
	p.ui.Status, p.ui.Content = st, disp
	if p.t == nil || !p.t.hidden {
		r.out(acp.UpdateToolCall(acp.ToolCallId(p.tc.ID), acp.WithUpdateStatus(st), acp.WithUpdateContent(disp)))
	}
	content := []llm.Part{llm.TextPart(p.res.text)}
	content = append(content, p.res.images...)
	r.add(llm.Message{Role: "tool", CallID: p.tc.ID, Content: content, IsError: p.res.err}, "", p.ui)
}

// exec 跑一个工具：先过权限，再执行。
func (r *run) exec(ctx context.Context, p *pending) result {
	v, rule := decide(r.s.mode(), p.t, p.args, r.s.cwd(), r.s.memory(), r.s.allowed)
	switch v {
	case deny:
		return errResult("Not allowed in read-only mode. Describe the change instead, or ask the user to switch modes.")
	case ask:
		opt, err := r.askPermission(ctx, p, rule)
		if err != nil {
			if ctx.Err() != nil {
				return result{}
			}
			return errResult("permission request failed: %v", err)
		}
		switch opt {
		case "allow_always":
			r.s.allow(rule)
		case "allow_once":
		default:
			return errResult("The user declined this tool call. Do not retry it as is; if it is unclear what to do instead, ask the user.")
		}
	}
	if p.t.hidden {
		return p.t.run(&call{ctx: ctx, r: r, id: p.tc.ID}, p.args)
	}
	r.out(acp.UpdateToolCall(acp.ToolCallId(p.tc.ID), acp.WithUpdateStatus(acp.ToolCallStatusInProgress)))
	return p.t.run(&call{ctx: ctx, r: r, id: p.tc.ID}, p.args)
}

func (r *run) askPermission(ctx context.Context, p *pending, rule string) (string, error) {
	opts := []acp.PermissionOption{{OptionId: "allow_once", Name: "允许", Kind: acp.PermissionOptionKindAllowOnce}}
	if rule != "" {
		name := "本会话都允许"
		switch {
		case rule == "edit":
			name = "允许，本会话改文件都不再问"
		case strings.HasPrefix(rule, "bash:"):
			name = "允许，本会话 `" + strings.TrimPrefix(rule, "bash:") + "` 开头的命令都不再问"
		case strings.HasPrefix(rule, "fetch:"):
			name = "允许，本会话访问 " + strings.TrimPrefix(rule, "fetch:") + " 都不再问"
		}
		opts = append(opts, acp.PermissionOption{OptionId: "allow_always", Name: name, Kind: acp.PermissionOptionKindAllowAlways})
	}
	opts = append(opts, acp.PermissionOption{OptionId: "reject_once", Name: "拒绝", Kind: acp.PermissionOptionKindRejectOnce})
	tc := acp.ToolCallUpdate{
		ToolCallId: acp.ToolCallId(p.tc.ID), Title: acp.Ptr(p.ui.Title), Kind: acp.Ptr(p.ui.Kind),
		Status: acp.Ptr(acp.ToolCallStatusPending), RawInput: p.args, Locations: p.ui.Locs,
	}
	// 改文件的审批卡片上直接给 diff：人要看的就是改成什么样
	if p.t.name == "edit" || p.t.name == "write" {
		if d := previewDiff(r.s.cwd(), p.t.name, p.args); d != nil {
			tc.Content = d
		}
	}
	res, err := r.s.a.conn.RequestPermission(ctx, acp.RequestPermissionRequest{SessionId: acp.SessionId(r.s.currentID()), ToolCall: tc, Options: opts})
	if err != nil {
		return "", err
	}
	if res.Outcome.Selected != nil {
		return string(res.Outcome.Selected.OptionId), nil
	}
	return "", nil
}
