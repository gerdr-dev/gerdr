// Package native 是 gerdr 自己的 agent：直接调模型接口（GPT、DeepSeek……，见 llm/），工具、主循环、会话记录都在这里。
// 对上和 claude / codex 两个适配器一样是一个完整的 ACP agent（进程内管道接 hub，`gerdr acp native` 是 stdio 版）。
//
// 为什么自己写、不用 shuttle 的 pi：插话（_session/steering）、子 agent、回退这几样 gerdr 必需的，pi 的主循环里都没有
// 或者形状不对，改它不如自己写一个小的 —— 主循环一百多行（loop.go），每一步都看得见、改得动。
// 模型这层只要两种协议（Chat Completions、Responses），也自己写（llm/）。
//
// 进程模型：没有子进程，会话就是内存里的一个 session + 磁盘上一份 jsonl（store.go）。
package native

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/coder/acp-go-sdk"
	"github.com/google/uuid"

	"github.com/gerdr-dev/gerdr/internal/acpx"
	"github.com/gerdr-dev/gerdr/internal/agents/native/llm"
	"github.com/gerdr-dev/gerdr/internal/ext"
)

type Options struct {
	Dir    string        // 会话记录放哪（<GERDR_DIR>/native）
	Ext    *ext.Manager  // skill 从哪来；nil = 只用 Claude Code 的
	Config func() Config // 模型配置，每次现取（设置一改下一轮就用新的）
	Prompt string        // 角色（设置 → Agent）追加的系统提示，接在系统提示词最后
}

type Agent struct {
	log  *slog.Logger
	conn *acp.AgentSideConnection
	st   store
	ext  *ext.Manager
	cfg  func() Config
	role string // Options.Prompt

	mu       sync.Mutex
	sessions map[string]*session // 当前 id 和 /clear 之前的旧 id 都指向它
	mcp      mcpPool
}

func New(log *slog.Logger, o Options) *Agent {
	if o.Config == nil {
		o.Config = func() Config { return Config{} }
	}
	return &Agent{log: log.With("agent", "native"), st: store{dir: o.Dir}, ext: o.Ext, cfg: o.Config, role: strings.TrimSpace(o.Prompt), sessions: map[string]*session{}}
}

func (a *Agent) SetConn(c *acp.AgentSideConnection) { a.conn = c }

func (a *Agent) Close() {
	a.mu.Lock()
	ss := make([]*session, 0, len(a.sessions))
	for _, s := range a.sessions {
		ss = append(ss, s)
	}
	a.mu.Unlock()
	for _, s := range ss {
		s.cancel()
		s.stopAll()
	}
	a.mcp.closeAll()
}

func (a *Agent) config() Config { return a.cfg() }

func (a *Agent) send(sid string, u acp.SessionUpdate) {
	if a.conn == nil {
		return
	}
	if err := a.conn.SessionUpdate(context.Background(), acp.SessionNotification{SessionId: acp.SessionId(sid), Update: u}); err != nil {
		a.log.Debug("session update", "err", err)
	}
}

func (a *Agent) session(id acp.SessionId) (*session, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.sessions[string(id)]
	if s == nil {
		return nil, acp.NewInvalidParams(map[string]any{"sessionId": id, "error": "unknown session"})
	}
	return s, nil
}

// Available：配置里有没有能用的模型（hub 的 agent 列表据此显示能不能用）。
func Available(c Config) error {
	if len(c.models()) == 0 {
		return fmt.Errorf("还没配置模型：在 config.json 的 llm.providers 里加一个服务商（baseUrl、apiKey、models）")
	}
	return nil
}

// ---- ACP: 初始化 / 会话 ----

func (a *Agent) Initialize(ctx context.Context, p acp.InitializeRequest) (acp.InitializeResponse, error) {
	return acp.InitializeResponse{
		ProtocolVersion: acp.ProtocolVersionNumber,
		AgentInfo:       &acp.Implementation{Name: "gerdr-native", Title: acp.Ptr("Gerdr Agent"), Version: "1"},
		AgentCapabilities: acp.AgentCapabilities{
			LoadSession:        true,
			PromptCapabilities: acp.PromptCapabilities{Image: true, EmbeddedContext: true},
			SessionCapabilities: acp.SessionCapabilities{
				List:   &acp.SessionListCapabilities{},
				Resume: &acp.SessionResumeCapabilities{},
				Close:  &acp.SessionCloseCapabilities{},
			},
		},
		AuthMethods: []acp.AuthMethod{},
		Meta:        map[string]any{"steering": map[string]any{"supported": true}},
	}, nil
}

func (a *Agent) Authenticate(ctx context.Context, p acp.AuthenticateRequest) (acp.AuthenticateResponse, error) {
	return acp.AuthenticateResponse{}, nil
}

func (a *Agent) Logout(ctx context.Context, p acp.LogoutRequest) (acp.LogoutResponse, error) {
	return acp.LogoutResponse{}, nil
}

func metaString(meta map[string]any, key string) string {
	if v, ok := acpx.GetMeta(meta, key); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func (a *Agent) NewSession(ctx context.Context, p acp.NewSessionRequest) (acp.NewSessionResponse, error) {
	if err := Available(a.config()); err != nil {
		return acp.NewSessionResponse{}, err
	}
	cwd := p.Cwd
	if cwd == "" {
		cwd, _ = os.UserHomeDir()
	}
	m := sessionMeta{Cwd: cwd, Mode: modeDefault, Created: time.Now().UnixMilli()}
	if md := metaString(p.Meta, "mode"); validMode(md) {
		m.Mode = md
	}
	if mk := metaString(p.Meta, "model"); mk != "" {
		if r, ok := a.config().find(mk); ok && r.Key == mk {
			m.Model = mk
		}
	}
	if m.Model == "" {
		r, _ := a.config().find("")
		m.Model = r.Key
	}
	s := a.register(uuid.NewString(), m, nil)
	s.saveMeta()
	a.announceCommands(s)
	return acp.NewSessionResponse{
		SessionId:     acp.SessionId(s.id),
		Modes:         &acp.SessionModeState{AvailableModes: modes, CurrentModeId: acp.SessionModeId(m.Mode)},
		ConfigOptions: a.configOptions(s),
	}, nil
}

func (a *Agent) register(id string, m sessionMeta, es []entry) *session {
	s := &session{a: a, id: id, meta: m, entries: es, rules: map[string]bool{}, read: map[string]bool{}}
	for _, r := range m.Allow {
		s.rules[r] = true
	}
	a.mu.Lock()
	a.sessions[id] = s
	a.mu.Unlock()
	return s
}

// attach：接回一个会话（Resume / Load）。记录不在（建了没说过话、或者被删了）就按同一个 id 新起一个。
func (a *Agent) attach(id, cwd string, meta map[string]any) (*session, error) {
	a.mu.Lock()
	if s := a.sessions[id]; s != nil {
		a.mu.Unlock()
		if md := metaString(meta, "mode"); validMode(md) {
			s.mu.Lock()
			s.meta.Mode = md
			s.mu.Unlock()
		}
		return s, nil
	}
	a.mu.Unlock()
	es, err := a.st.load(id)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	m := lastMeta(es)
	if m.Cwd == "" {
		m.Cwd = cwd
	}
	if m.Cwd == "" {
		m.Cwd, _ = os.UserHomeDir()
	}
	if m.Mode == "" {
		m.Mode = modeDefault
	}
	if md := metaString(meta, "mode"); validMode(md) {
		m.Mode = md
	}
	if r, ok := a.config().find(m.Model); ok {
		m.Model = r.Key // 原来的模型从配置里删掉了：换成默认的
	}
	s := a.register(id, m, es)
	if len(es) == 0 {
		s.saveMeta()
	}
	return s, nil
}

func (a *Agent) ResumeSession(ctx context.Context, p acp.ResumeSessionRequest) (acp.ResumeSessionResponse, error) {
	s, err := a.attach(string(p.SessionId), p.Cwd, p.Meta)
	if err != nil {
		return acp.ResumeSessionResponse{}, err
	}
	a.announceCommands(s)
	return acp.ResumeSessionResponse{
		Modes:         &acp.SessionModeState{AvailableModes: modes, CurrentModeId: acp.SessionModeId(s.mode())},
		ConfigOptions: a.configOptions(s),
	}, nil
}

// LoadSession = Resume + 把历史当成 session/update 重放一遍。
func (a *Agent) LoadSession(ctx context.Context, p acp.LoadSessionRequest) (acp.LoadSessionResponse, error) {
	s, err := a.attach(string(p.SessionId), p.Cwd, p.Meta)
	if err != nil {
		return acp.LoadSessionResponse{}, err
	}
	a.replay(s)
	a.announceCommands(s)
	return acp.LoadSessionResponse{
		Modes:         &acp.SessionModeState{AvailableModes: modes, CurrentModeId: acp.SessionModeId(s.mode())},
		ConfigOptions: a.configOptions(s),
	}, nil
}

func (a *Agent) replay(s *session) {
	s.mu.Lock()
	es := append([]entry(nil), s.entries...)
	s.mu.Unlock()
	pendingCalls := map[string]bool{}
	for _, e := range es {
		if e.T != "msg" || e.Msg == nil {
			continue
		}
		m := *e.Msg
		switch m.Role {
		case "user":
			if e.ID == "" {
				continue
			}
			s.send(acp.SessionUpdate{UserMessageChunk: &acp.SessionUpdateUserMessageChunk{Content: acp.TextBlock(userText(m)), MessageId: acp.Ptr(e.ID)}})
		case "assistant":
			id := uuid.NewString()
			for _, t := range m.Reasoning {
				if t.Text != "" {
					s.send(acp.SessionUpdate{AgentThoughtChunk: &acp.SessionUpdateAgentThoughtChunk{Content: acp.TextBlock(t.Text), MessageId: acp.Ptr(id)}})
				}
			}
			if m.Text != "" {
				s.send(acp.SessionUpdate{AgentMessageChunk: &acp.SessionUpdateAgentMessageChunk{Content: acp.TextBlock(m.Text), MessageId: acp.Ptr(id)}})
			}
			for _, tc := range m.ToolCalls {
				pendingCalls[tc.ID] = true
			}
		case "tool":
			ui := e.UI
			if ui == nil || !pendingCalls[m.CallID] {
				continue
			}
			if ui.Title == "Update todos" || ui.Title == "Ask the user" {
				continue
			}
			opts := []acp.ToolCallStartOpt{acp.WithStartKind(ui.Kind), acp.WithStartStatus(ui.Status), acp.WithStartRawInput(ui.Input), acp.WithStartContent(ui.Content)}
			if len(ui.Locs) > 0 {
				opts = append(opts, acp.WithStartLocations(ui.Locs))
			}
			s.send(acp.StartToolCall(acp.ToolCallId(m.CallID), ui.Title, opts...))
		}
	}
}

func (a *Agent) ListSessions(ctx context.Context, p acp.ListSessionsRequest) (acp.ListSessionsResponse, error) {
	var out acp.ListSessionsResponse
	for _, l := range a.st.list() {
		if p.Cwd != nil && *p.Cwd != "" && filepath.Clean(*p.Cwd) != filepath.Clean(l.Cwd) {
			continue
		}
		up := l.Updated.UTC().Format(time.RFC3339)
		out.Sessions = append(out.Sessions, acp.SessionInfo{SessionId: acp.SessionId(l.ID), Cwd: l.Cwd, Title: acp.Ptr(l.Title), UpdatedAt: &up})
		if len(out.Sessions) >= 200 {
			break
		}
	}
	return out, nil
}

func (a *Agent) CloseSession(ctx context.Context, p acp.CloseSessionRequest) (acp.CloseSessionResponse, error) {
	a.mu.Lock()
	s := a.sessions[string(p.SessionId)]
	if s != nil {
		for k, v := range a.sessions {
			if v == s {
				delete(a.sessions, k)
			}
		}
	}
	a.mu.Unlock()
	if s != nil {
		s.cancel()
	}
	return acp.CloseSessionResponse{}, nil
}

func (a *Agent) SetSessionMode(ctx context.Context, p acp.SetSessionModeRequest) (acp.SetSessionModeResponse, error) {
	s, err := a.session(p.SessionId)
	if err != nil {
		return acp.SetSessionModeResponse{}, err
	}
	if !validMode(string(p.ModeId)) {
		return acp.SetSessionModeResponse{}, acp.NewInvalidParams(map[string]any{"modeId": p.ModeId})
	}
	s.mu.Lock()
	s.meta.Mode = string(p.ModeId)
	s.mu.Unlock()
	s.saveMeta() // 正在跑的这一轮也立刻按新模式审批（每次调用现判断）
	return acp.SetSessionModeResponse{}, nil
}

func (a *Agent) SetSessionConfigOption(ctx context.Context, p acp.SetSessionConfigOptionRequest) (acp.SetSessionConfigOptionResponse, error) {
	if p.ValueId == nil {
		return acp.SetSessionConfigOptionResponse{}, acp.NewInvalidParams("only select options are supported")
	}
	s, err := a.session(p.ValueId.SessionId)
	if err != nil {
		return acp.SetSessionConfigOptionResponse{}, err
	}
	v := string(p.ValueId.Value)
	s.mu.Lock()
	switch string(p.ValueId.ConfigId) {
	case "model":
		s.meta.Model = v
		s.meta.Effort = "" // 换了模型，档位不一定还认：用新模型的默认档
	case "effort":
		s.meta.Effort = v
	default:
		s.mu.Unlock()
		return acp.SetSessionConfigOptionResponse{}, acp.NewInvalidParams(map[string]any{"configId": p.ValueId.ConfigId})
	}
	s.mu.Unlock()
	s.saveMeta()
	return acp.SetSessionConfigOptionResponse{ConfigOptions: a.configOptions(s)}, nil
}

var effortNames = map[string]string{"off": "关", "none": "关", "minimal": "最低", "low": "低", "medium": "中", "high": "高", "xhigh": "超高", "max": "最高"}

func (a *Agent) configOptions(s *session) []acp.SessionConfigOption {
	cfg := a.config()
	ms := cfg.models()
	if len(ms) == 0 {
		return nil
	}
	s.mu.Lock()
	curKey, effort := s.meta.Model, s.meta.Effort
	s.mu.Unlock()
	cur, _ := cfg.find(curKey)
	multi := len(cfg.Providers) > 1
	var opts acp.SessionConfigSelectOptionsUngrouped
	for _, m := range ms {
		name := m.Name
		if multi {
			pn := m.Provider.Name
			if pn == "" {
				pn = m.Provider.ID
			}
			name += " · " + pn
		}
		opts = append(opts, acp.SessionConfigSelectOption{Value: acp.SessionConfigValueId(m.Key), Name: name})
	}
	out := []acp.SessionConfigOption{{Select: &acp.SessionConfigOptionSelect{
		Id: "model", Name: "Model", Type: "select", Category: acp.Ptr(acp.SessionConfigOptionCategoryModel),
		CurrentValue: acp.SessionConfigValueId(cur.Key), Options: acp.SessionConfigSelectOptions{Ungrouped: &opts},
	}}}
	if len(cur.Efforts) > 0 {
		r := run{model: cur, effort: effort}
		var eo acp.SessionConfigSelectOptionsUngrouped
		for _, e := range cur.Efforts {
			n := effortNames[e]
			if n == "" {
				n = e
			}
			eo = append(eo, acp.SessionConfigSelectOption{Value: acp.SessionConfigValueId(e), Name: n})
		}
		out = append(out, acp.SessionConfigOption{Select: &acp.SessionConfigOptionSelect{
			Id: "effort", Name: "Effort", Type: "select", Category: acp.Ptr(acp.SessionConfigOptionCategoryThoughtLevel),
			CurrentValue: acp.SessionConfigValueId(r.effortValue()), Options: acp.SessionConfigSelectOptions{Ungrouped: &eo},
		}})
	}
	return out
}

// ---- 斜杠命令 ----

var builtinCommands = []acp.AvailableCommand{
	{Name: "clear", Description: "清空上下文：另起一段新对话，之前说过的不再带上"},
	{Name: "compact", Description: "压缩上下文：把之前的对话总结成摘要，腾出上下文空间（后面可以跟要求）"},
	{Name: "memory", Description: "看它跨会话记住了什么；后面跟话就是关于记忆的要求（比如「/memory 忘掉 xxx」「/memory 记住 xxx」）"},
}

func (a *Agent) announceCommands(s *session) {
	cmds := append([]acp.AvailableCommand(nil), builtinCommands...)
	for _, sk := range skillsFor(a.ext, s.cwd()) {
		cmds = append(cmds, acp.AvailableCommand{Name: sk.Name, Description: oneLine(sk.Description, 200), Meta: map[string]any{"skill": true}})
	}
	s.send(acp.SessionUpdate{AvailableCommandsUpdate: &acp.SessionAvailableCommandsUpdate{AvailableCommands: cmds}})
}

// slash 认「整条消息以 /命令 开头」：返回命令名和后面的参数。
func slash(blocks []acp.ContentBlock) (name, args string) {
	if len(blocks) == 0 || blocks[0].Text == nil {
		return "", ""
	}
	t := strings.TrimSpace(blocks[0].Text.Text)
	if !strings.HasPrefix(t, "/") {
		return "", ""
	}
	name, args, _ = strings.Cut(t[1:], " ")
	if name == "" || strings.ContainsAny(name, "/\n") {
		return "", ""
	}
	return name, strings.TrimSpace(args)
}

// ---- ACP: 一轮 ----

func (a *Agent) Prompt(ctx context.Context, p acp.PromptRequest) (acp.PromptResponse, error) {
	s, err := a.session(p.SessionId)
	if err != nil {
		return acp.PromptResponse{}, err
	}
	msgID := ""
	if p.MessageId != nil {
		msgID = *p.MessageId
	}
	name, args := slash(p.Prompt)
	if name == "clear" && args == "" {
		return a.clear(s)
	}
	tctx, end, err := s.begin()
	if err != nil {
		return acp.PromptResponse{}, err
	}
	defer end()
	// acp-go-sdk 收到 session/cancel 时会顺手取消 Prompt 的 ctx（连接断了也会）
	stop := context.AfterFunc(ctx, s.cancel)
	defer stop()

	r := s.newRun(tctx)
	if r.model.Key == "" {
		return acp.PromptResponse{}, Available(a.config())
	}
	blocks := p.Prompt
	if name == "compact" {
		// 这条消息本身也记进历史（回退按它找），压缩完它就在摘要之前了
		s.addMsg(llmUser(a.toParts(blocks, s)), msgID, nil)
		if err := r.compact(tctx, args); err != nil {
			if tctx.Err() != nil {
				return acp.PromptResponse{StopReason: acp.StopReasonCancelled}, nil
			}
			return acp.PromptResponse{}, acp.NewInternalError(map[string]any{"message": err.Error()})
		}
		return acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
	}
	if name == "memory" {
		// 换成一句给模型的话（记忆就是文件，它自己会读写）；界面上显示的还是人打的 /memory
		text := "Show me what you remember across conversations: read both memory indexes (global and project) and list the memories grouped by scope, one line each with its file path. Do not change anything."
		if args != "" {
			text = "About your long-term memory: " + args + "\n\n(Act on this by reading / writing / deleting files in your memory directories as described in your instructions, then tell me what changed.)"
		}
		blocks = append([]acp.ContentBlock{acp.TextBlock(text)}, blocks[1:]...)
	} else if name != "" {
		if sk := findSkill(skillsFor(a.ext, s.cwd()), name); sk != nil {
			text := fmt.Sprintf("The user invoked the skill %q. Read %s with the read tool and follow it.", sk.Name, filepath.Join(sk.Dir, "SKILL.md"))
			if args != "" {
				text += "\n\nArguments: " + args
			}
			blocks = append([]acp.ContentBlock{acp.TextBlock(text)}, blocks[1:]...)
		}
	}
	s.addMsg(llmUser(a.toParts(blocks, s)), msgID, nil)

	for {
		err = r.loop(tctx)
		if err != nil || s.endIfIdle(end) {
			break
		}
	}
	// 不能只看 tctx：endIfIdle 收尾时已经把它取消了
	switch {
	case err == errStopped || (err != nil && tctx.Err() != nil):
		return acp.PromptResponse{StopReason: acp.StopReasonCancelled}, nil
	case err != nil:
		return acp.PromptResponse{}, acp.NewInternalError(map[string]any{"message": err.Error()})
	}
	return acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
}

func findSkill(ss []ext.Skill, name string) *ext.Skill {
	for i := range ss {
		if ss[i].Name == name {
			return &ss[i]
		}
	}
	return nil
}

// clear：/clear 另起一个新会话（同目录、同设置），用旧 id 报 sessionId + cleared，hub 把记录换成新 id
// （和 claude / codex 的 /clear 走同一条路）。旧 id 留作别名。
func (a *Agent) clear(s *session) (acp.PromptResponse, error) {
	s.mu.Lock()
	if s.busy {
		s.mu.Unlock()
		return acp.PromptResponse{}, acp.NewInvalidRequest("a turn is already running")
	}
	old := s.id
	nid := uuid.NewString()
	m := s.meta
	m.Created = time.Now().UnixMilli()
	s.id, s.meta, s.entries, s.lastInput, s.queue = nid, m, nil, 0, nil
	s.mu.Unlock()
	a.mu.Lock()
	a.sessions[nid] = s
	a.mu.Unlock()
	s.saveMeta()
	a.send(old, acp.SessionUpdate{SessionInfoUpdate: &acp.SessionSessionInfoUpdate{Meta: acpx.Meta(map[string]any{"sessionId": nid, "cleared": true})}})
	return acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
}

func (a *Agent) Cancel(ctx context.Context, p acp.CancelNotification) error {
	if s, err := a.session(p.SessionId); err == nil {
		s.cancel()
	}
	return nil
}

// ---- 扩展方法 ----

func (a *Agent) HandleExtensionMethod(ctx context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case acpx.MethodSteer:
		var p acpx.SteerRequest
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, acp.NewInvalidParams(err.Error())
		}
		s, err := a.session(p.SessionId)
		if err != nil {
			return nil, err
		}
		if !s.enqueue(p.MessageId, p.Prompt, p.Now) {
			return acpx.SteerResponse{Outcome: acpx.SteerPromptRequired}, nil
		}
		return acpx.SteerResponse{Outcome: acpx.SteerInjected}, nil
	case acpx.MethodWithdraw:
		var p acpx.WithdrawRequest
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, acp.NewInvalidParams(err.Error())
		}
		s, err := a.session(p.SessionId)
		if err != nil {
			return nil, err
		}
		return acpx.WithdrawResponse{Withdrawn: s.withdraw(p.MessageIds)}, nil
	case acpx.MethodRewind:
		var p acpx.RewindRequest
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, acp.NewInvalidParams(err.Error())
		}
		s, err := a.session(p.SessionId)
		if err != nil {
			return nil, err
		}
		if err := s.rewind(p.MessageId); err != nil {
			return nil, err
		}
		return acpx.RewindResponse{SessionId: acp.SessionId(s.currentID())}, nil
	case acpx.MethodTaskStop:
		var p acpx.TaskStopRequest
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, acp.NewInvalidParams(err.Error())
		}
		s, err := a.session(p.SessionId)
		if err != nil {
			return nil, err
		}
		if !s.stopTask(p.TaskId) {
			return nil, fmt.Errorf("no running task %s", p.TaskId)
		}
		return map[string]any{}, nil
	case acpx.MethodMCPStatus:
		var p struct {
			Cwd   string `json:"cwd"`
			Probe bool   `json:"probe"`
		}
		_ = json.Unmarshal(params, &p)
		return map[string]any{"servers": a.mcpStatus(ctx, p.Cwd, p.Probe)}, nil
	case acpx.MethodTaskBackground:
		var p acpx.TaskBackgroundRequest
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, acp.NewInvalidParams(err.Error())
		}
		s, err := a.session(p.SessionId)
		if err != nil {
			return nil, err
		}
		return acpx.TaskBackgroundResponse{Backgrounded: s.detach(p.ToolCallId)}, nil
	}
	return nil, acp.NewMethodNotFound(method)
}

func llmUser(parts []llm.Part) llm.Message { return llm.Message{Role: "user", Content: parts} }
