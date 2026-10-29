package native

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/coder/acp-go-sdk"
	"github.com/google/uuid"

	"github.com/zbysir/gerdr/internal/acpx"
	"github.com/zbysir/gerdr/internal/agents/native/llm"
)

type session struct {
	a  *Agent
	id string // 当前 id（/clear 之后会换）

	mu      sync.Mutex
	entries []entry
	meta    sessionMeta
	rules   map[string]bool // 「以后不再问」的规则
	read    map[string]bool // 读过的文件（写之前没读过的，给模型提个醒）

	// 一轮
	busy         bool
	turnCancel   context.CancelFunc
	streamCancel context.CancelFunc // 只掐当前这次生成（Now 插话）
	turnDone     chan struct{}
	queue        []queued
	lastInput    int // 最近一次请求的输入 token，判断要不要压缩

	tok   acpx.TokenMeter
	tasks map[string]*acpx.Task    // 任务栏：子 agent、后台命令
	stops map[string]func()        // 跑着的任务怎么停（任务栏上的「停止」、task_stop）
	fg    map[string]chan struct{} // 前台跑着、可以「转到后台」的工具调用
	bgOut map[string]*lockedBuf    // 后台命令的输出（task_output）

	mcpWarned map[string]bool // 连不上的 MCP 服务器已经提示过了
}

type queued struct {
	id     string
	blocks []acp.ContentBlock
	note   string // 不是人说的话：后台任务的通知（bg.go）
}

func (s *session) currentID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.id
}

func (s *session) cwd() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.meta.Cwd
}

func (s *session) mode() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.meta.Mode
}

func (s *session) allowed(rule string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rules[rule]
}

func (s *session) allow(rule string) {
	if rule == "" {
		return
	}
	s.mu.Lock()
	if s.rules[rule] {
		s.mu.Unlock()
		return
	}
	s.rules[rule] = true
	s.meta.Allow = append(s.meta.Allow, rule)
	s.mu.Unlock()
	s.saveMeta()
}

func (s *session) setBgOut(id string, b *lockedBuf) {
	s.mu.Lock()
	if s.bgOut == nil {
		s.bgOut = map[string]*lockedBuf{}
	}
	s.bgOut[id] = b
	s.mu.Unlock()
}

func (s *session) markRead(p string) {
	s.mu.Lock()
	s.read[p] = true
	s.mu.Unlock()
}

func (s *session) send(u acp.SessionUpdate) {
	s.a.send(s.currentID(), u)
}

func (s *session) setStreamCancel(c context.CancelFunc) {
	s.mu.Lock()
	s.streamCancel = c
	s.mu.Unlock()
}

func (s *session) setLastInput(n int) {
	s.mu.Lock()
	s.lastInput = n
	s.mu.Unlock()
}

// ---- 历史 ----

func (s *session) contextMessages() []llm.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return buildContext(s.entries)
}

func (s *session) addMsg(m llm.Message, id string, ui *toolUI) {
	e := entry{T: "msg", At: time.Now().UnixMilli(), ID: id, Msg: &m, UI: ui}
	s.mu.Lock()
	s.entries = append(s.entries, e)
	sid := s.id
	s.mu.Unlock()
	if err := s.a.st.append(sid, e); err != nil {
		s.a.log.Warn("save session", "err", err)
	}
}

func (s *session) saveMeta() {
	s.mu.Lock()
	m := s.meta
	m.Allow = append([]string(nil), m.Allow...)
	e := entry{T: "meta", At: time.Now().UnixMilli(), Meta: &m}
	s.entries = append(s.entries, e)
	sid := s.id
	s.mu.Unlock()
	if err := s.a.st.append(sid, e); err != nil {
		s.a.log.Warn("save session", "err", err)
	}
}

// ---- 插话 ----

func (s *session) enqueue(id string, blocks []acp.ContentBlock, now bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.busy {
		return false
	}
	s.queue = append(s.queue, queued{id: id, blocks: blocks})
	if now && s.streamCancel != nil {
		s.streamCancel()
	}
	return true
}

func (s *session) withdraw(ids []string) []string {
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var keep []queued
	var out []string
	for _, q := range s.queue {
		if want[q.id] {
			out = append(out, q.id)
		} else {
			keep = append(keep, q)
		}
	}
	s.queue = keep
	return out
}

func (s *session) hasQueued() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.queue) > 0
}

// drainQueue：排队的插话接到历史末尾（下一次调模型就带上）。
func (s *session) drainQueue() {
	s.mu.Lock()
	q := s.queue
	s.queue = nil
	s.mu.Unlock()
	for _, x := range q {
		if x.note != "" {
			s.addMsg(llm.Message{Role: "user", Content: []llm.Part{llm.TextPart(x.note)}}, "", nil)
			continue
		}
		s.addMsg(llm.Message{Role: "user", Content: s.a.toParts(x.blocks, s)}, x.id, nil)
	}
}

// ---- 一轮 ----

// begin 占住这个会话跑一轮；已经在跑就失败。end 收尾（可以多调）。
func (s *session) begin() (context.Context, func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.busy {
		return nil, nil, acp.NewInvalidRequest("a turn is already running; use _session/steering")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.busy, s.turnCancel, s.turnDone = true, cancel, done
	s.tok.Reset()
	var once sync.Once
	return ctx, func() {
		once.Do(func() {
			cancel()
			s.mu.Lock()
			if s.turnDone == done { // 没被下一轮接手（endIfIdle 之后可能已经开了新的一轮）
				s.busy, s.turnCancel, s.streamCancel = false, nil, nil
			}
			s.mu.Unlock()
			close(done)
		})
	}, nil
}

// endIfIdle：队里没有插话就把会话标成空闲（之后来的插话会回 promptRequired，client 改发新的一轮）；
// 有就返回 false，接着跑。两件事在一把锁里做：不然「刚判断完没有」和「标成空闲」之间进来的插话会被当成已接收、却没人处理。
func (s *session) endIfIdle(done func()) bool {
	s.mu.Lock()
	if len(s.queue) > 0 {
		s.mu.Unlock()
		return false
	}
	s.busy = false
	s.mu.Unlock()
	done()
	return true
}

func (s *session) cancel() {
	s.mu.Lock()
	c := s.turnCancel
	s.mu.Unlock()
	if c != nil {
		c()
	}
}

func (s *session) newRun(ctx context.Context) *run {
	cfg := s.a.config()
	s.mu.Lock()
	model, effort, cwd := s.meta.Model, s.meta.Effort, s.meta.Cwd
	s.mu.Unlock()
	m, _ := cfg.find(model)
	r := &run{s: s, model: m, effort: effort, tok: &s.tok}
	r.tools = append([]*tool(nil), builtinTools...)
	if servers := mcpServers(s.a.ext, cwd); len(servers) > 0 {
		// 第一次要起服务器（npx 下载之类）可能很慢：最多等 30 秒，没起完的这一轮先不带，起完了下一轮就有
		clients, errs := s.a.mcp.get(ctx, servers, 30*time.Second)
		for _, c := range clients {
			r.tools = append(r.tools, s.a.mcpTools(c)...)
		}
		for name, err := range errs {
			s.mu.Lock()
			warned := s.mcpWarned[name]
			if s.mcpWarned == nil {
				s.mcpWarned = map[string]bool{}
			}
			s.mcpWarned[name] = true
			s.mu.Unlock()
			if !warned {
				r.notice("MCP 服务器 " + name + " 没连上：" + oneLine(err.Error(), 200))
			}
		}
	}
	r.system = systemPrompt(cwd, skillsFor(s.a.ext, cwd), false)
	return r
}

// builtinTools：内置工具。在 init 里装：工具的 run 绕一圈会用到 newRun（后台任务跑完自己开一轮），直接写在变量初始化里是初始化循环。
var builtinTools []*tool

func init() {
	builtinTools = []*tool{readTool, writeTool, editTool, bashTool, grepTool, globTool, todoTool, askTool, taskTool, bashOutputTool, stopTaskTool, webFetchTool}
}

// ---- 压缩 ----

const compactPrompt = `Summarize the conversation so far so that work can continue from the summary alone (the earlier messages will be dropped). Include:
1. The user's requests and intent, in order, quoting important instructions verbatim.
2. Key technical context: files read or changed (with paths), important code, commands run and their results, errors and how they were fixed.
3. Decisions made and constraints learned.
4. What was being worked on right before this summary, and the next step — exactly where to pick up.
Be thorough but concise. Reply with the summary only.`

// maybeCompact：上一次请求的输入超过阈值就先压缩再接着跑。
func (r *run) maybeCompact(ctx context.Context) error {
	r.s.mu.Lock()
	n := r.s.lastInput
	r.s.mu.Unlock()
	if n == 0 || n < r.model.compactAt() {
		return nil
	}
	return r.compact(ctx, "")
}

// compact 让模型把目前的对话总结成一段，之后的上下文从摘要开始（store.go 的 buildContext）。
// 界面上是一张「压缩上下文」的卡片。
func (r *run) compact(ctx context.Context, extra string) error {
	id := "compact-" + uuid.NewString()
	r.out(acp.StartToolCall(acp.ToolCallId(id), "Compacting context", acp.WithStartKind(acp.ToolKindThink), acp.WithStartStatus(acp.ToolCallStatusInProgress)))
	msgs := r.history()
	if len(msgs) == 0 {
		r.out(acp.UpdateToolCall(acp.ToolCallId(id), acp.WithUpdateStatus(acp.ToolCallStatusCompleted)))
		return nil
	}
	instr := compactPrompt
	if extra != "" {
		instr += "\n\nAdditional instructions from the user: " + extra
	}
	msgs = append(msgs, llm.Message{Role: "user", Content: []llm.Part{llm.TextPart(instr)}})
	m := r.model
	if sm := r.s.a.config().SmallModel; sm != "" {
		if x, ok := r.s.a.config().find(sm); ok && x.Key == sm {
			m = x
		}
	}
	res, err := m.client().Stream(ctx, llm.Request{Model: m.ID, System: r.system, Messages: msgs, Thinking: m.Thinking, Effort: lowestEffort(m), CacheKey: r.s.currentID()}, func(llm.Event) {})
	if err != nil || strings.TrimSpace(res.Message.Text) == "" {
		r.out(acp.UpdateToolCall(acp.ToolCallId(id), acp.WithUpdateStatus(acp.ToolCallStatusFailed)))
		if err == nil {
			err = fmt.Errorf("compaction returned an empty summary")
		}
		return fmt.Errorf("compact context: %w", err)
	}
	e := entry{T: "compact", At: time.Now().UnixMilli(), Summary: res.Message.Text}
	r.s.mu.Lock()
	r.s.entries = append(r.s.entries, e)
	r.s.lastInput = 0
	sid := r.s.id
	r.s.mu.Unlock()
	_ = r.s.a.st.append(sid, e)
	r.out(acp.UpdateToolCall(acp.ToolCallId(id), acp.WithUpdateStatus(acp.ToolCallStatusCompleted),
		acp.WithUpdateContent([]acp.ToolCallContent{acp.ToolContent(acp.TextBlock(res.Message.Text))})))
	return nil
}

// lowestEffort：杂活用最低的思考档（DeepSeek 的 off 也行：总结不需要想）。
func lowestEffort(m resolved) string {
	if len(m.Efforts) == 0 {
		return ""
	}
	return m.Efforts[0]
}

// ---- 回退 ----

// rewind 截掉 messageId 那条 user 消息和之后的一切（含之后的压缩）。
func (s *session) rewind(msgID string) error {
	s.mu.Lock()
	if s.busy {
		s.mu.Unlock()
		return acp.NewInvalidRequest("cannot rewind while a turn is running")
	}
	idx := -1
	for i, e := range s.entries {
		if e.T == "msg" && e.ID == msgID && e.Msg != nil && e.Msg.Role == "user" {
			idx = i
			break
		}
	}
	if idx < 0 {
		s.mu.Unlock()
		return fmt.Errorf("message %s not found in this session", msgID)
	}
	kept := append([]entry(nil), s.entries[:idx]...)
	// 回退前的设置（模型、模式、放行规则）以现在的为准：截掉的那段里可能有后来改的 meta
	m := s.meta
	kept = append(kept, entry{T: "meta", At: time.Now().UnixMilli(), Meta: &m})
	s.entries = kept
	s.lastInput = 0
	sid := s.id
	s.mu.Unlock()
	return s.a.st.rewrite(sid, kept)
}

// ---- 子 agent 的任务栏 ----

func (s *session) pushTasks() {
	s.mu.Lock()
	list := make([]acpx.Task, 0, len(s.tasks))
	for _, t := range s.tasks {
		list = append(list, *t)
	}
	s.mu.Unlock()
	sort.Slice(list, func(i, j int) bool { return list[i].StartedAt < list[j].StartedAt })
	s.send(acp.SessionUpdate{SessionInfoUpdate: &acp.SessionSessionInfoUpdate{Meta: acpx.Meta(map[string]any{"tasks": list})}})
}

// ---- 输入 ----

// toParts：ACP 的内容块 → 给模型的。看不了图的模型：图存成文件、告诉它路径（它至少知道人发了图、能用别的工具处理）。
func (a *Agent) toParts(blocks []acp.ContentBlock, s *session) []llm.Part {
	var out []llm.Part
	m, _ := a.config().find(func() string { s.mu.Lock(); defer s.mu.Unlock(); return s.meta.Model }())
	for _, b := range blocks {
		switch {
		case b.Text != nil:
			out = append(out, llm.TextPart(b.Text.Text))
		case b.Image != nil:
			if m.seesImages() {
				out = append(out, llm.Part{Type: "image", MIME: b.Image.MimeType, Data: b.Image.Data})
			} else if p := saveImage(b.Image.Data, b.Image.MimeType); p != "" {
				out = append(out, llm.TextPart(fmt.Sprintf("[The user attached an image, saved at %s. The current model cannot view images.]", p)))
			}
		case b.ResourceLink != nil:
			out = append(out, llm.TextPart(fmt.Sprintf("[Attached: %s]", strings.TrimPrefix(b.ResourceLink.Uri, "file://"))))
		case b.Resource != nil:
			if t := b.Resource.Resource.TextResourceContents; t != nil {
				out = append(out, llm.TextPart(fmt.Sprintf("<file path=%q>\n%s\n</file>", strings.TrimPrefix(t.Uri, "file://"), t.Text)))
			}
		}
	}
	// 相邻的文字并成一段（有的网关对多段纯文字的 content 数组处理得不好）
	var merged []llm.Part
	for _, p := range out {
		if n := len(merged); n > 0 && p.Type == "text" && merged[n-1].Type == "text" {
			merged[n-1].Text += "\n" + p.Text
			continue
		}
		merged = append(merged, p)
	}
	return merged
}

func saveImage(data, mime string) string {
	b, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return ""
	}
	ext := ".png"
	switch mime {
	case "image/jpeg":
		ext = ".jpg"
	case "image/gif":
		ext = ".gif"
	case "image/webp":
		ext = ".webp"
	}
	dir := filepath.Join(os.TempDir(), "gerdr-native-images")
	_ = os.MkdirAll(dir, 0o700)
	p := filepath.Join(dir, uuid.NewString()+ext)
	if os.WriteFile(p, b, 0o600) != nil {
		return ""
	}
	return p
}
