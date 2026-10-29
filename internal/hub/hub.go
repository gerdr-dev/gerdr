// Package hub 管所有会话：起 agent、转发审批 / 提问、决定「这句话是插话还是新开一轮」、
// 记日志、广播给前端。
package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/coder/acp-go-sdk"

	"github.com/gerdr-dev/gerdr/internal/acpx"
	"github.com/gerdr-dev/gerdr/internal/agenthost"
	"github.com/gerdr-dev/gerdr/internal/agents/claude"
	"github.com/gerdr-dev/gerdr/internal/agents/codex"
	"github.com/gerdr-dev/gerdr/internal/agents/native"
	"github.com/gerdr-dev/gerdr/internal/ext"
	"github.com/gerdr-dev/gerdr/internal/sysenv"
)

// AgentDef 描述一种 agent。内置 claude / codex / native（gerdr 自己的，直接调模型接口，config.json 的 llm 配了模型才能用）；用户可以在 config.json 的 agents 里加两种：
//   - 变体（Base = claude / codex）：同一个适配器、换一套启动参数 —— 典型是「claude 2」：
//     同一个 Claude Code、另一个账号（Env 里给 ANTHROPIC_API_KEY，或者给一个单独的 CLAUDE_CONFIG_DIR 在里面 /login）。
//     每个变体是一个**单独的适配器实例**（各自的进程、各自的会话记录目录），会话按变体 id 记，不会串到原版上去。
//   - 外部 ACP agent（Command）：自己会说 ACP 的程序。
//
// 变体也是「角色」：可以带一段追加的系统提示（Prompt），只给挑出来的 skill / MCP（Skills / MCP，nil = 照全局设置），
// 新会话的默认模式 / 模型另存在 config.json 的 defaultModes / defaultConfig（按 agent id，内置的三个也一样，见 NewDefaults）。
// 角色可以基于 native（Gerdr Agent）：它没有可执行文件，Bin / Args / Env 不起作用。
type AgentDef struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Command []string          `json:"command,omitempty"` // 外部 agent 的启动命令；内置的为空
	Base    string            `json:"base,omitempty"`    // claude / codex / native：变体
	Bin     string            `json:"bin,omitempty"`     // 变体：换一个可执行文件（空 = 和原版一样找）
	Args    []string          `json:"args,omitempty"`    // 变体：追加的命令行参数
	Env     map[string]string `json:"env,omitempty"`     // 变体：环境变量
	Prompt  string            `json:"prompt,omitempty"`  // 变体：追加的系统提示
	// Providers：引擎是 native 时只用这几个模型服务商（config.json llm.providers 的 id）；空 = 全部。
	Providers []string `json:"providers,omitempty"`
	// Skills / MCP：只给这几个（按名字）。不写 = 照全局设置；写了空列表 = 一个都不给。
	Skills []string `json:"skills"`
	MCP    []string `json:"mcp"`
}

// only：这个角色对 skill / MCP 的限定。
func (d AgentDef) only() ext.Only { return ext.Only{Skills: d.Skills, MCP: d.MCP} }

func (d AgentDef) envList() []string {
	keys := make([]string, 0, len(d.Env))
	for k := range d.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	home, _ := os.UserHomeDir()
	for _, k := range keys {
		v := d.Env[k]
		// 子进程不经过 shell，没人替你展开 ~（CLAUDE_CONFIG_DIR=~/.claude-2 会变成当前目录下一个叫「~」的目录）
		if v == "~" || strings.HasPrefix(v, "~/") {
			v = filepath.Join(home, v[1:])
		}
		out = append(out, k+"="+v)
	}
	return out
}

// ValidAgentDefs 挑出 config.json 里能用的那些（id 不能和内置的撞、得是变体或外部命令之一），补上缺的名字。
func ValidAgentDefs(in []AgentDef) []AgentDef {
	var out []AgentDef
	seen := map[string]bool{"claude": true, "codex": true, "native": true}
	for _, a := range in {
		if a.ID == "" || seen[a.ID] {
			continue
		}
		if a.Base != "" && !builtinAgent(a.Base) {
			continue
		}
		if a.Base == "" && len(a.Command) == 0 {
			continue
		}
		if a.Name == "" {
			a.Name = a.ID
		}
		seen[a.ID] = true
		out = append(out, a)
	}
	return out
}

// builtinAgent：内置的三个（不是变体、不是外部命令）。
func builtinAgent(id string) bool { return id == "claude" || id == "codex" || id == "native" }

type Options struct {
	Dir         string // ~/.gerdr
	ClaudeIdle  time.Duration
	ExtraAgents []AgentDef
	Log         *slog.Logger
	Ext         *ext.Manager // skill / MCP 从哪来；nil = 全用 agent 自己的
	// LLM：native agent 的模型配置（config.json 的 llm），每次现取。nil = 没配。
	LLM func() native.Config
	// Defaults：新会话的默认模式 / 模型（config.json 的 defaultModes / defaultConfig），按 agent id，每次现取。
	Defaults func(agent string) NewDefaults
}

// NewDefaults：一种 agent（或角色）新开会话时用的模式和配置项（model / effort…，键是 agent 报的配置项 id）。
type NewDefaults struct {
	Mode   string
	Config map[string]string
}

// extra：设置里改了会热换，所以不放在 opt 里读；单独一把锁（Summary 持着会话锁时要读它，而 h.mu 的顺序是先 hub 后会话）。
func (h *Hub) extra() []AgentDef {
	h.defsMu.RLock()
	defer h.defsMu.RUnlock()
	return h.defs
}

// SetAgents：设置里改了 agents。定义变了（或被删掉）的那几个，已经起着的连接收掉 —— 下次用到时按新配置起，
// 挂在上面的会话自动重接（resume），对话不丢。
func (h *Hub) SetAgents(defs []AgentDef) {
	defs = ValidAgentDefs(defs)
	h.defsMu.Lock()
	old := map[string]AgentDef{}
	for _, d := range h.defs {
		old[d.ID] = d
	}
	h.defs = defs
	h.defsMu.Unlock()
	now := map[string]AgentDef{}
	for _, d := range defs {
		now[d.ID] = d
	}
	var stale []*agenthost.Host
	h.mu.Lock()
	for id, d := range old {
		if n, ok := now[id]; !ok || !reflect.DeepEqual(n, d) {
			if c := h.hosts[id]; c != nil {
				stale = append(stale, c)
				delete(h.hosts, id)
			}
		}
	}
	h.mu.Unlock()
	for _, c := range stale {
		c.Close()
	}
}

// Base：这个 agent 实际是哪一种（claude / codex / 外部 agent 自己的 id）。界面上图标、哪些功能可用都按它判断。
func (h *Hub) Base(agent string) string {
	if builtinAgent(agent) {
		return agent
	}
	for _, d := range h.extra() {
		if d.ID == agent && d.Base != "" {
			return d.Base
		}
	}
	return agent
}

type Hub struct {
	opt  Options
	log  *slog.Logger
	cmds *cmdCache
	opts *optCache

	mu       sync.Mutex
	hosts    map[string]*agenthost.Host // agent id → 连接（懒起）
	starting map[string]chan struct{}
	sessions map[string]*Session // gerdr id → 会话
	byAgent  map[string]*Session // agent:acpSessionId → 会话
	// early：agent 在 session/new 的响应之前就推过来的通知（斜杠命令列表多半是这么来的），
	// 那时这边还没登记这个会话。攒着，登记时补上；不攒的话 codex 新会话里打 / 什么都没有。
	early map[string][]earlyUpdate
	subs  map[*Sub]struct{}
	seq   int64
	// 增量补发（Watch 带 since）靠这两个判断前端手里那份还能不能接着用，见 Watch。
	bootSeq int64            // 载入完旧日志时的序号：重启前拿到的（≤ 它）都不信
	resetAt map[string]int64 // 会话 id → 最近一次整份重推（撤回 / 回退 / 清空改写了日志）时的序号，≤ 它的都不信

	defsMu sync.RWMutex
	defs   []AgentDef // config.json 里的 agents（变体 + 外部 agent）

	usageOnce sync.Once
	usage     *usageCache // 用量额度的缓存（usage.go）
}

func New(opt Options) (*Hub, error) {
	if opt.Log == nil {
		opt.Log = slog.Default()
	}
	if err := os.MkdirAll(filepath.Join(opt.Dir, "sessions"), 0o700); err != nil {
		return nil, err
	}
	h := &Hub{
		opt: opt, log: opt.Log,
		hosts: map[string]*agenthost.Host{}, starting: map[string]chan struct{}{},
		sessions: map[string]*Session{}, byAgent: map[string]*Session{}, early: map[string][]earlyUpdate{}, subs: map[*Sub]struct{}{},
		resetAt: map[string]int64{},
	}
	h.defs = ValidAgentDefs(opt.ExtraAgents)
	h.cmds = newCmdCache(opt.Dir)
	h.opts = newOptCache(opt.Dir)
	if err := h.load(); err != nil {
		return nil, err
	}
	h.bootSeq = h.seq
	// 等 hub 整个建好再去核对没收尾的轮次（要起 agent、广播事件）
	for _, s := range h.sessions {
		if s.orphanSince > 0 {
			go s.adoptOrphanTurn(s.orphanSince)
		}
	}
	go h.flusher()
	return h, nil
}

// Agents 列出能用的 agent（available 表示本机找得到它）。
func (h *Hub) Agents() []map[string]any {
	var out []map[string]any
	_, cerr := sysenv.FindClaude()
	_, xerr := sysenv.FindCodex()
	out = append(out,
		map[string]any{"id": "claude", "name": "Claude Code", "available": cerr == nil, "error": errStr(cerr)},
		map[string]any{"id": "codex", "name": "Codex", "available": xerr == nil, "error": errStr(xerr)},
	)
	nerr := native.Available(h.llm())
	if nerr == nil {
		// 没配模型时不列：大多数人只用 claude / codex，列一个永远不能用的选项是噪音
		out = append(out, map[string]any{"id": "native", "name": "Gerdr Agent", "available": true})
	}
	for _, a := range h.extra() {
		m := map[string]any{"id": a.ID, "name": a.Name, "available": true, "base": a.Base}
		switch {
		case a.Base != "" && a.Base != "native" && a.Bin != "":
			if _, err := sysenv.FindBin(a.Bin); err != nil {
				m["available"], m["error"] = false, err.Error()
			}
		case a.Base == "claude":
			m["available"], m["error"] = cerr == nil, errStr(cerr)
		case a.Base == "codex":
			m["available"], m["error"] = xerr == nil, errStr(xerr)
		case a.Base == "native":
			err := native.Available(h.llm().Only(a.Providers))
			if err != nil && len(a.Providers) > 0 && len(h.llm().Only(a.Providers).Providers) == 0 {
				// 勾的服务商都被删了（比 Available 那句「去加一个」更说得清）
				err = fmt.Errorf("勾的服务商（%s）已经删了：编辑它重新勾一个", strings.Join(a.Providers, "、"))
			}
			m["available"], m["error"] = err == nil, errStr(err)
		}
		out = append(out, m)
	}
	// options：这种 agent 上次报的模式 / 配置项（optCache），设置里编辑「新会话默认」时拿它画下拉框。
	// 新加的角色还没开过会话，用它底子的。
	for _, m := range out {
		id, _ := m["id"].(string)
		o, ok := h.opts.get(id)
		if !ok {
			o, ok = h.opts.get(h.Base(id))
		}
		if ok {
			m["options"] = o
		}
	}
	return out
}

func (h *Hub) llm() native.Config {
	if h.opt.LLM == nil {
		return native.Config{}
	}
	return h.opt.LLM()
}

func errStr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (h *Hub) agentDef(id string) (AgentDef, bool) {
	switch id {
	case "claude":
		return AgentDef{ID: "claude", Name: "Claude Code"}, true
	case "codex":
		return AgentDef{ID: "codex", Name: "Codex"}, true
	case "native":
		return AgentDef{ID: "native", Name: "Gerdr Agent"}, true
	}
	for _, a := range h.extra() {
		if a.ID == id {
			return a, true
		}
	}
	return AgentDef{}, false
}

// host 拿（必要时起）某种 agent 的连接。
func (h *Hub) host(ctx context.Context, agent string) (*agenthost.Host, error) {
	for {
		h.mu.Lock()
		if c := h.hosts[agent]; c != nil {
			select {
			case <-c.Conn.Done():
				delete(h.hosts, agent)
			default:
				h.mu.Unlock()
				return c, nil
			}
		}
		if ch := h.starting[agent]; ch != nil {
			h.mu.Unlock()
			select {
			case <-ch:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		ch := make(chan struct{})
		h.starting[agent] = ch
		h.mu.Unlock()

		c, err := h.startHost(ctx, agent)
		h.mu.Lock()
		delete(h.starting, agent)
		close(ch)
		if err == nil {
			h.hosts[agent] = c
			// 连接换了：之前挂在旧连接上的会话都要重新接一次。
			for _, s := range h.sessions {
				if s.rec.Agent == agent {
					s.mu.Lock()
					s.attached = false
					s.mu.Unlock()
				}
			}
		}
		h.mu.Unlock()
		return c, err
	}
}

func (h *Hub) startHost(ctx context.Context, agent string) (*agenthost.Host, error) {
	def, ok := h.agentDef(agent)
	if !ok {
		return nil, fmt.Errorf("unknown agent %q", agent)
	}
	log := h.log.With("agent", agent)
	handler := &agentHandler{h: h, agent: agent}
	sctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	base := def.Base
	if builtinAgent(agent) {
		base = agent
	}
	// 角色限定了 skill / MCP 的，给它一个只看得见挑中那几个的 ext（设置、目录还是同一份）
	em := h.opt.Ext.Scoped(def.only())
	switch base {
	case "claude":
		return agenthost.StartInProc(sctx, claude.New(log, claude.Options{IdleTimeout: h.opt.ClaudeIdle, Ext: em, Bin: def.Bin, Args: def.Args, Env: def.envList(), Prompt: def.Prompt}), handler, log)
	case "codex":
		return agenthost.StartInProc(sctx, codex.NewWith(log, em, codex.Options{Bin: def.Bin, Args: def.Args, Env: def.envList(), Prompt: def.Prompt}), handler, log)
	case "native":
		// 基于 Gerdr Agent 的角色和原版共用会话目录（id 是 uuid，撞不了）和记忆：记忆是人的偏好、项目的约定，不分角色
		return agenthost.StartInProc(sctx, native.New(log, native.Options{Dir: filepath.Join(h.opt.Dir, "native"), Ext: em, Config: func() native.Config { return h.llm().Only(def.Providers) }, Prompt: def.Prompt}), handler, log)
	}
	home, _ := os.UserHomeDir()
	return agenthost.StartProcess(sctx, def.Command, home, handler, log)
}

// Close 关掉所有 agent。
func (h *Hub) Close() {
	h.mu.Lock()
	hosts := h.hosts
	h.hosts = map[string]*agenthost.Host{}
	ss := make([]*Session, 0, len(h.sessions))
	for _, s := range h.sessions {
		ss = append(ss, s)
	}
	h.mu.Unlock()
	for _, s := range ss {
		s.flush(true)
	}
	for _, c := range hosts {
		c.Close()
	}
}

// ---- agent → hub ----

type agentHandler struct {
	h     *Hub
	agent string
}

func (a *agentHandler) find(sid acp.SessionId) *Session {
	a.h.mu.Lock()
	defer a.h.mu.Unlock()
	return a.h.byAgent[a.agent+":"+string(sid)]
}

type earlyUpdate struct {
	at time.Time
	u  acp.SessionUpdate
}

func (a *agentHandler) OnUpdate(n acp.SessionNotification) {
	key := a.agent + ":" + string(n.SessionId)
	a.h.mu.Lock()
	s := a.h.byAgent[key]
	if s == nil {
		// 只留几秒、几条：真正没人认领的（比如已经删掉的会话迟到的流）别一直占着内存。
		now := time.Now()
		for k, v := range a.h.early {
			if now.Sub(v[len(v)-1].at) > 10*time.Second {
				delete(a.h.early, k)
			}
		}
		if q := a.h.early[key]; len(q) < 32 {
			a.h.early[key] = append(q, earlyUpdate{now, n.Update})
		}
	}
	a.h.mu.Unlock()
	if s != nil {
		s.onUpdate(n.Update)
	}
}

// claimEarly 取走这个 key 下攒着的通知（调用方持有 h.mu）。
func (h *Hub) claimEarly(key string) []earlyUpdate {
	q := h.early[key]
	delete(h.early, key)
	return q
}

func replayEarly(s *Session, q []earlyUpdate) {
	for _, e := range q {
		s.onUpdate(e.u)
	}
}

func (a *agentHandler) OnPermission(ctx context.Context, req acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	s := a.find(req.SessionId)
	if s == nil {
		return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeCancelled()}, nil
	}
	return s.askPermission(ctx, req)
}

func (a *agentHandler) OnAsk(ctx context.Context, req acpx.AskRequest) (acpx.AskResponse, error) {
	s := a.find(req.SessionId)
	if s == nil {
		return acpx.AskResponse{Cancelled: true}, nil
	}
	return s.ask(ctx, req)
}

// ---- 持久化 ----

func (h *Hub) indexPath() string { return filepath.Join(h.opt.Dir, "sessions", "index.json") }

func (h *Hub) load() error {
	b, err := os.ReadFile(h.indexPath())
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var recs []Record
	if err := json.Unmarshal(b, &recs); err != nil {
		return fmt.Errorf("sessions/index.json: %w", err)
	}
	for _, r := range recs {
		s := newSession(h, r)
		evs, err := readLog(s.lf.path)
		if err != nil {
			h.log.Warn("read session log", "id", r.ID, "err", err)
		}
		s.events = evs
		s.lf.flushed = len(evs)
		for _, e := range evs {
			if e.Seq > h.seq {
				h.seq = e.Seq
			}
		}
		s.orphanSince = s.recoverAfterRestart()
		h.sessions[r.ID] = s
		h.byAgent[r.Agent+":"+r.AgentSessionID] = s
	}
	return nil
}

func (h *Hub) saveIndex() {
	h.mu.Lock()
	recs := make([]Record, 0, len(h.sessions))
	for _, s := range h.sessions {
		s.mu.Lock()
		recs = append(recs, s.rec)
		s.mu.Unlock()
	}
	h.mu.Unlock()
	sort.Slice(recs, func(i, j int) bool { return recs[i].CreatedAt < recs[j].CreatedAt })
	b, _ := json.MarshalIndent(recs, "", "  ")
	tmp := h.indexPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err == nil {
		_ = os.Rename(tmp, h.indexPath())
	}
}

func (h *Hub) flusher() {
	t := time.NewTicker(2 * time.Second)
	for range t.C {
		h.mu.Lock()
		ss := make([]*Session, 0, len(h.sessions))
		for _, s := range h.sessions {
			ss = append(ss, s)
		}
		h.mu.Unlock()
		for _, s := range ss {
			s.flush(false)
		}
	}
}

func (h *Hub) nextSeq() int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.seq++
	return h.seq
}

// ---- 会话的增删查 ----

func (h *Hub) Session(id string) (*Session, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.sessions[id]
	if s == nil {
		return nil, errors.New("session not found")
	}
	return s, nil
}

func (h *Hub) List() []Summary {
	h.mu.Lock()
	ss := make([]*Session, 0, len(h.sessions))
	for _, s := range h.sessions {
		ss = append(ss, s)
	}
	h.mu.Unlock()
	out := make([]Summary, 0, len(ss))
	for _, s := range ss {
		out = append(out, s.Summary())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt < out[j].CreatedAt })
	return out
}

type CreateOptions struct {
	Agent  string            `json:"agent"`
	Cwd    string            `json:"cwd"`
	Mode   string            `json:"mode,omitempty"`
	Config map[string]string `json:"config,omitempty"`
	// Import：接上一个在别处开过的会话（agent 自己的 session id），历史从它那儿重放。
	Import string `json:"import,omitempty"`
	Title  string `json:"title,omitempty"`
	// FromCLI：`gerdr session new` 开的（agent 自己跑的命令）。这时默认模式是「全放开」那种的不套：
	// 那条命令不审批就放行（sysenv.SafeGerdrCLI 不许带 --mode 就是为这个），沙箱里的 agent 不能借用户的默认值开一个不设防的会话。
	FromCLI bool `json:"-"`
}

// permissive：不再问人就动手的模式。
var permissive = regexp.MustCompile(`(?i)bypass|full-access|dontAsk`)

func (h *Hub) Create(ctx context.Context, o CreateOptions) (*Session, error) {
	if _, ok := h.agentDef(o.Agent); !ok {
		return nil, fmt.Errorf("unknown agent %q", o.Agent)
	}
	cwd, err := normCwd(o.Cwd)
	if err != nil {
		return nil, err
	}
	c, err := h.host(ctx, o.Agent)
	if err != nil {
		return nil, err
	}
	// 没指定的模式 / 配置项用设置里的默认值（config.json 的 defaultModes / defaultConfig）。界面和 `gerdr session new`
	// 都走这里，所以两边开出来的一样。默认值不对（agent 不认这个模式、模型被删了）不报错：适配器不认的模式按自己的默认开，
	// 配置项在下面对着 agent 报的可选值核过才设。
	explicitMode := o.Mode != ""
	var defConfig map[string]string
	if h.opt.Defaults != nil {
		d := h.opt.Defaults(o.Agent)
		if o.Mode == "" && !(o.FromCLI && permissive.MatchString(d.Mode)) {
			o.Mode = d.Mode
		}
		if o.Import == "" { // 导入的会话用它原来的模型，不拿默认的盖
			defConfig = d.Config
		}
	}
	meta := map[string]any{}
	if o.Mode != "" {
		meta["mode"] = o.Mode
	}
	for k, v := range defConfig {
		if _, given := o.Config[k]; !given {
			meta[k] = v
		}
	}
	for k, v := range o.Config {
		meta[k] = v
	}
	// 新建时给的标题是人起的（`gerdr session new --title`），算手动命名、不被自动标题盖掉；导入时带进来的是原会话的标题，照旧可以被更新
	rec := Record{ID: newID(), Agent: o.Agent, Cwd: cwd, CreatedAt: now(), UpdatedAt: now(), Title: o.Title, CustomTitle: o.Title != "" && o.Import == "", Mode: o.Mode, Config: o.Config}
	s := newSession(h, rec)

	if o.Import != "" {
		rec.AgentSessionID = o.Import
		s.rec = rec
		// 先登记，LoadSession 重放的 update 才找得到主人。
		h.register(s)
		res, err := c.Conn.LoadSession(ctx, acp.LoadSessionRequest{SessionId: acp.SessionId(o.Import), Cwd: cwd, McpServers: []acp.McpServer{}, Meta: acpx.Meta(meta)})
		if err != nil {
			h.unregister(s)
			return nil, err
		}
		s.mu.Lock()
		s.attached = true
		s.modes, s.config = res.Modes, res.ConfigOptions
		h.opts.set(o.Agent, res.Modes, res.ConfigOptions)
		s.mu.Unlock()
	} else {
		res, err := c.Conn.NewSession(ctx, acp.NewSessionRequest{Cwd: cwd, McpServers: []acp.McpServer{}, Meta: acpx.Meta(meta)})
		if err != nil {
			return nil, err
		}
		// 各家适配器遇到不认识的模式都是悄悄按默认模式开（比如给 native 填了 codex 的 full-access），
		// 调用方（`gerdr session new --mode`）以为放开了权限、其实每步都要人批 —— 这里对着 agent 报的可选模式查，不对就报错。
		// 只查调用方点名的；设置里的默认模式 agent 不认就算了（它已经按自己的默认开了，记录跟着 agent 报的走）。
		if err := checkMode(o.Mode, res.Modes); explicitMode && err != nil {
			_, _ = c.Conn.CloseSession(ctx, acp.CloseSessionRequest{SessionId: res.SessionId})
			return nil, err
		}
		s.rec.AgentSessionID = string(res.SessionId)
		s.attached = true
		s.modes, s.config = res.Modes, res.ConfigOptions
		h.opts.set(o.Agent, res.Modes, res.ConfigOptions)
		h.register(s)
		s.applyConfig(ctx, mergeConfig(defConfig, o.Config))
	}
	s.syncRecFromState()
	h.saveIndex()
	h.broadcastSession(s)
	return s, nil
}

func mergeConfig(def, given map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range def {
		out[k] = v
	}
	for k, v := range given {
		out[k] = v
	}
	return out
}

// applyConfig：建会话时要的配置项，agent 在 session/new 里没接住的补设一遍。三家能在新建时就接的不一样
// （claude 接 model / effort，codex 只接 model，native 只接 model），统一在这儿对着它报回来的当前值补：
// 先 model（换模型会把思考强度重置成这个模型的默认），再别的；值不在可选项里的跳过（默认值里存的模型可能已经没了）。
func (s *Session) applyConfig(ctx context.Context, want map[string]string) {
	keys := make([]string, 0, len(want))
	for k := range want {
		keys = append(keys, k)
	}
	sortKeys(keys)
	for _, k := range keys {
		v := want[k]
		s.mu.Lock()
		cur, ok := selectOption(s.config, k)
		s.mu.Unlock()
		if !ok || v == "" || string(cur.CurrentValue) == v || !hasValue(cur, v) {
			continue
		}
		if err := s.SetConfig(ctx, k, v); err != nil {
			s.h.log.Warn("apply default config", "session", s.rec.ID, "key", k, "err", err)
		}
	}
}

// sortKeys：model 排最前，其余按名字。
func sortKeys(keys []string) {
	sort.Slice(keys, func(i, j int) bool {
		if (keys[i] == "model") != (keys[j] == "model") {
			return keys[i] == "model"
		}
		return keys[i] < keys[j]
	})
}

func selectOption(opts []acp.SessionConfigOption, id string) (*acp.SessionConfigOptionSelect, bool) {
	for _, o := range opts {
		if o.Select != nil && string(o.Select.Id) == id {
			return o.Select, true
		}
	}
	return nil, false
}

func hasValue(o *acp.SessionConfigOptionSelect, v string) bool {
	if u := o.Options.Ungrouped; u != nil {
		for _, x := range *u {
			if string(x.Value) == v {
				return true
			}
		}
	}
	if g := o.Options.Grouped; g != nil {
		for _, grp := range *g {
			for _, x := range grp.Options {
				if string(x.Value) == v {
					return true
				}
			}
		}
	}
	return false
}

// FindByAgent：按 agent 自己的会话 id 找 gerdr 会话（agent 跑的 `gerdr session …` 靠
// CLAUDE_CODE_SESSION_ID / CODEX_THREAD_ID 认出「我是谁」）。
func (h *Hub) FindByAgent(agent, agentSessionID string) *Session {
	// agent 可以是变体 id，也可以是底子（claude / codex）：agent 跑的 `gerdr session current` 只知道自己是哪一种
	// （CLAUDE_CODE_SESSION_ID / CODEX_THREAD_ID），不知道自己是哪个变体 —— 底子相同的变体挨个试。
	ids := []string{agent}
	for _, d := range h.extra() {
		if d.Base == agent {
			ids = append(ids, d.ID)
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, id := range ids {
		if s := h.byAgent[id+":"+agentSessionID]; s != nil {
			return s
		}
	}
	return nil
}

func (h *Hub) register(s *Session) {
	h.mu.Lock()
	h.sessions[s.rec.ID] = s
	key := s.rec.Agent + ":" + s.rec.AgentSessionID
	h.byAgent[key] = s
	q := h.claimEarly(key)
	h.mu.Unlock()
	replayEarly(s, q)
}

func (h *Hub) unregister(s *Session) {
	h.mu.Lock()
	delete(h.sessions, s.rec.ID)
	delete(h.byAgent, s.rec.Agent+":"+s.rec.AgentSessionID)
	h.mu.Unlock()
}

// rekey：回退后 agent 那边的会话 id 变了（claude 是分叉出一个新会话）。
func (h *Hub) rekey(s *Session, newAgentID string) {
	h.mu.Lock()
	delete(h.byAgent, s.rec.Agent+":"+s.rec.AgentSessionID)
	s.mu.Lock()
	s.rec.AgentSessionID = newAgentID
	s.rec.Fork = nil // 换了 id，上一个「从哪儿分叉」作废（回退会在 rekey 之后重新填）
	s.mu.Unlock()
	key := s.rec.Agent + ":" + newAgentID
	h.byAgent[key] = s
	q := h.claimEarly(key)
	h.mu.Unlock()
	replayEarly(s, q)
	h.saveIndex()
}

// Shutdown 关掉 agent 那边的会话（claude 的进程当场收掉），gerdr 的记录留着：界面上关了 pane / tab 就调它，
// 不然 claude 进程要白挂到闲置超时（15 分钟）。之后再发话 ensureAttached 会 --resume 接回来，⌘⇧T 撤销关闭照样能用。
func (h *Hub) Shutdown(ctx context.Context, id string) error {
	s, err := h.Session(id)
	if err != nil {
		return err
	}
	s.attachMu.Lock() // 和 ensureAttached 错开：别刚接上就被这边标成没接
	s.mu.Lock()
	s.shut = true // 先标上再杀：杀掉后 runTurn 拿到「进程意外退出」时要认得出是我们关的
	s.mu.Unlock()
	s.close(ctx)
	s.mu.Lock()
	s.attached = false
	s.mu.Unlock()
	s.attachMu.Unlock()
	s.setStatus() // close 把等人处理的请求清掉了
	h.broadcastSession(s)
	return nil
}

// Delete 关掉并删除一个会话（gerdr 这边的记录；agent 自己的会话记录不动）。
func (h *Hub) Delete(ctx context.Context, id string) error {
	s, err := h.Session(id)
	if err != nil {
		return err
	}
	s.close(ctx)
	h.unregister(s)
	_ = os.Remove(s.lf.path)
	h.saveIndex()
	h.broadcast(Msg{T: "session_removed", ID: id})
	return nil
}

// ListImportable 列出某种 agent 在某个目录下已有的会话（给「接着之前的聊」用）。
func (h *Hub) ListImportable(ctx context.Context, agent, cwd string) ([]acp.SessionInfo, error) {
	c, err := h.host(ctx, agent)
	if err != nil {
		return nil, err
	}
	req := acp.ListSessionsRequest{}
	if cwd != "" {
		if n, err := normCwd(cwd); err == nil {
			req.Cwd = &n
		}
	}
	res, err := c.Conn.ListSessions(ctx, req)
	if err != nil {
		return nil, err
	}
	// 已经在 gerdr 里开着的不再列。
	h.mu.Lock()
	var out []acp.SessionInfo
	for _, si := range res.Sessions {
		if h.byAgent[agent+":"+string(si.SessionId)] == nil {
			out = append(out, si)
		}
	}
	h.mu.Unlock()
	return out, nil
}

func normCwd(p string) (string, error) {
	if p == "" {
		home, _ := os.UserHomeDir()
		return home, nil
	}
	if len(p) > 1 && p[0] == '~' {
		home, _ := os.UserHomeDir()
		p = filepath.Join(home, p[1:])
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		abs = real
	}
	st, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("目录不存在：%s", abs)
	}
	if !st.IsDir() {
		return "", fmt.Errorf("不是目录：%s", abs)
	}
	return abs, nil
}

// Pending：所有会话里在等人处理的审批 / 提问（给手环、脚本这类外面的程序：一次看全，挑一个回 /answer）。
func (h *Hub) Pending() []PendingReq {
	h.mu.Lock()
	ss := make([]*Session, 0, len(h.sessions))
	for _, s := range h.sessions {
		ss = append(ss, s)
	}
	h.mu.Unlock()
	out := []PendingReq{}
	for _, s := range ss {
		out = append(out, s.Pending()...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At < out[j].At })
	return out
}

// checkMode：新建会话时指定的模式得在 agent 报的可选模式里。agent 没报模式（有的外部 agent 不支持）就不查。
func checkMode(mode string, modes *acp.SessionModeState) error {
	if mode == "" || modes == nil || len(modes.AvailableModes) == 0 {
		return nil
	}
	ids := make([]string, 0, len(modes.AvailableModes))
	for _, m := range modes.AvailableModes {
		if string(m.Id) == mode {
			return nil
		}
		ids = append(ids, string(m.Id))
	}
	return fmt.Errorf("不支持的模式 %q，可选：%s", mode, strings.Join(ids, "、"))
}
