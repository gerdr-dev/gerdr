// Package codex 是 Codex 的 ACP 适配器：对上实现 ACP agent，对下说 codex app-server 的
// JSON-RPC（stdio）。参考的是 agentclientprotocol/codex-acp（TS），只搬了 gerdr 用得到的那部分。
//
// 进程模型：**一个 app-server 进程挂所有会话**（app-server 原生支持多 thread），常驻约 50MB。
// ACP 的 sessionId 就是 codex 的 threadId。
package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/coder/acp-go-sdk"

	"github.com/gerdr-dev/gerdr/internal/acpx"
	"github.com/gerdr-dev/gerdr/internal/ext"
	"github.com/gerdr-dev/gerdr/internal/rpcline"
	"github.com/gerdr-dev/gerdr/internal/sysenv"
)

const (
	modeAsk      = "ask"
	modeAuto     = "auto"
	modeReadOnly = "read-only"
	modeFull     = "full-access"
)

var modes = []acp.SessionMode{
	{Id: modeAsk, Name: "Ask", Description: acp.Ptr("除了明显安全的读操作，每条命令都问你")},
	{Id: modeAuto, Name: "Auto", Description: acp.Ptr("工作区内随便改、随便跑；越界（联网、写工作区外）才问")},
	{Id: modeReadOnly, Name: "Read only", Description: acp.Ptr("只读，不改文件")},
	{Id: modeFull, Name: "Full access", Description: acp.Ptr("不问、不设沙箱")},
}

// commands：app-server 不报斜杠命令（那是 TUI 的东西），我们接了哪几个就报哪几个。
// 名字和描述照 claude 的同名命令：两边都有的常用命令在界面上用起来一样（/new 是界面上 /clear 的别名）。
var commands = []acp.AvailableCommand{
	{Name: "clear", Description: "清空上下文：另起一段新对话，之前说过的不再带上"},
	{Name: "compact", Description: "压缩上下文：把之前的对话总结成摘要，腾出上下文空间"},
}

// slashCommand 认「整条消息就是一个 /命令」：只有一段文字、没有图片，去掉首尾空白后是 /名字。
func slashCommand(blocks []acp.ContentBlock) string {
	if len(blocks) != 1 || blocks[0].Text == nil {
		return ""
	}
	t := strings.TrimSpace(blocks[0].Text.Text)
	for _, c := range commands {
		if t == "/"+c.Name {
			return c.Name
		}
	}
	return ""
}

func (a *Agent) announceCommands(sid string) {
	a.send(context.Background(), sid, acp.SessionUpdate{AvailableCommandsUpdate: &acp.SessionAvailableCommandsUpdate{AvailableCommands: commands}})
}

type Agent struct {
	log  *slog.Logger
	conn *acp.AgentSideConnection

	mu       sync.Mutex
	rpc      *rpcline.Conn
	cmd      *exec.Cmd
	sessions map[string]*session
	models   []modelInfo
	ext      *ext.Manager
	opts     Options
}

// Options：「另一个 codex」（设置里配的变体）用的。Bin 换可执行文件，Args 追加在 app-server 后面，
// Env（KEY=VALUE）盖在继承来的环境上 —— 换账号一般是 CODEX_HOME（登录态、会话记录都在那个目录）或 OPENAI_API_KEY。
type Options struct {
	Bin  string
	Args []string
	Env  []string
	// Prompt：角色（设置 → Agent）追加的指令，跟在 developerInstructions 后面。
	Prompt string
}

type session struct {
	id  string
	cwd string

	mu     sync.Mutex
	mode   string
	model  string
	effort string

	turnID   string          // 正在跑的那一轮；空 = 闲着
	turnDone chan turnResult // Prompt 在等的那一轮的结果
	// compacts：`/compact` 那条消息 → 它跑出来的那一轮。压缩那一轮里没有 userMessage，
	// 按 clientId 找不回来（findTurn），回退到它头上只能靠这张表。只在内存里：
	// gerdr 重启之后回退到那条 /compact 会报找不到（回退到更早的消息不受影响）。
	compacts map[string]string
	extFP    string // 接上 thread 时 skill / MCP 设置的指纹；对不上下一轮前重接

	tasks  map[string]*acpx.Task // 子 agent（subagents.go），key 是子 thread id
	tok    acpx.TokenMeter       // 这一轮输出了多少 token（状态行）；按请求分，req 是当前这次请求的编号
	req    int
	sub    *subInfo // 非空 = 这是某个子 agent 的伪会话，update 改挂到主会话下（subagents.go）
	items  map[string]*itemState
	roll   *rollTail // code-mode exec / wait cards read from the rollout (codemode.go)
	cancel bool
	errMsg string
}

type turnResult struct {
	id     string
	status string
	err    *turnError
}

// itemState 记一个 item 在流式期间攒下来的东西。
type itemState struct {
	sawDelta bool
	output   strings.Builder
	lastPush time.Time
	title    string
}

// New：e 管 skill / MCP 从哪来（nil = 全用 codex 自己的）。
func New(log *slog.Logger, e *ext.Manager) *Agent {
	return NewWith(log, e, Options{})
}

func NewWith(log *slog.Logger, e *ext.Manager, o Options) *Agent {
	return &Agent{log: log.With("agent", "codex"), sessions: map[string]*session{}, ext: e, opts: o}
}

// instructions：每个 thread 的 developerInstructions —— 界面能显示媒体的提示，加上角色追加的指令。
func (a *Agent) instructions() string {
	if p := strings.TrimSpace(a.opts.Prompt); p != "" {
		return acpx.GUIHint + "\n\n" + p
	}
	return acpx.GUIHint
}

// SetConn 必须在连接建好之后、处理任何请求之前调。
func (a *Agent) SetConn(c *acp.AgentSideConnection) { a.conn = c }

// ---- 进程 ----

func (a *Agent) client(ctx context.Context) (*rpcline.Conn, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.rpc != nil {
		select {
		case <-a.rpc.Done():
			a.rpc = nil
		default:
			return a.rpc, nil
		}
	}
	bin, err := sysenv.FindCodex()
	if a.opts.Bin != "" {
		bin, err = sysenv.FindBin(a.opts.Bin)
	}
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(bin, append([]string{"app-server"}, a.opts.Args...)...)
	cmd.Env = append(sysenv.ChildEnv(), a.opts.Env...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr := &logWriter{log: a.log}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start codex app-server: %w", err)
	}
	rpc := rpcline.New(stdout, stdin, a, a.log)
	go func() {
		err := cmd.Wait()
		a.log.Warn("codex app-server exited", "err", err, "stderr", stderr.Tail())
		a.onExit()
	}()
	ictx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var initRes json.RawMessage
	if err := rpc.Call(ictx, "initialize", map[string]any{
		"clientInfo":   map[string]any{"name": "gerdr", "title": "gerdr", "version": "1"},
		"capabilities": map[string]any{"experimentalApi": true},
	}, &initRes); err != nil {
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("codex initialize: %w", err)
	}
	_ = rpc.Notify("initialized", nil)
	a.rpc, a.cmd = rpc, cmd
	// app-server 重启过的话，已知的会话要 resume 回来，不然下一轮发过去它不认识这个 thread。
	for _, s := range a.sessions {
		go a.resumeThread(context.Background(), rpc, s)
	}
	return rpc, nil
}

func (a *Agent) onExit() {
	a.mu.Lock()
	ss := make([]*session, 0, len(a.sessions))
	for _, s := range a.sessions {
		ss = append(ss, s)
	}
	a.mu.Unlock()
	for _, s := range ss {
		s.finish(turnResult{status: "failed", err: &turnError{Message: "codex app-server 进程退出了"}})
	}
}

func (a *Agent) resumeThread(ctx context.Context, rpc *rpcline.Conn, s *session) error {
	p := map[string]any{"threadId": s.id, "excludeTurns": true, "developerInstructions": a.instructions()}
	if s.cwd != "" {
		p["cwd"] = s.cwd
	}
	cfg, fp := a.extConfig(ctx, rpc, s.cwd)
	if cfg != nil {
		p["config"] = cfg
	}
	if err := rpc.Call(ctx, "thread/resume", p, nil); err != nil {
		return err
	}
	s.mu.Lock()
	s.extFP = fp
	s.mu.Unlock()
	return nil
}

// extConfig 把 skill / MCP 设置翻成 thread/start、thread/resume 的 config 覆盖。
// 覆盖是**合并**进用户配置的，所以「关掉 codex 自己的」只能逐个列出来关（见 ext 包注释）。
func (a *Agent) extConfig(ctx context.Context, rpc *rpcline.Conn, cwd string) (map[string]any, string) {
	if a.ext == nil {
		return nil, ""
	}
	fp := a.ext.Fingerprint()
	st := a.ext.Settings()
	cfg := a.ext.CodexMCPOverrides()
	if st.MCPSource == ext.SourceGerdr || a.ext.LimitsMCP() {
		mine := map[string]bool{}
		if st.MCPSource.UsesGerdr() {
			for _, v := range st.MCP {
				mine[v.Name] = true
			}
		}
		// 只用 gerdr 的：codex 自己的全关；角色限定了 MCP：codex 自己的里没挑的关（gerdr 那份已经在 CodexMCPOverrides 里筛过）
		for _, v := range ext.CodexMCP() {
			if !mine[v.Name] && (st.MCPSource == ext.SourceGerdr || !a.ext.AllowMCP(v.Name)) {
				cfg["mcp_servers."+v.Name+".enabled"] = false
			}
		}
	}
	// gerdr 的 skill 目录是整个 app-server 的设置：每次接 thread 前按当前设置摆一遍。
	// 内置 skill（多会话协作、改界面）不管来源选什么都给；gerdr 自己那份要选了才给。
	a.ext.Builtin() // 确保已经装到磁盘上
	roots := []string{a.ext.BuiltinDir()}
	if st.SkillSource.UsesGerdr() {
		roots = append(roots, a.ext.SkillsDir())
	}
	if err := rpc.Call(ctx, "skills/extraRoots/set", map[string]any{"extraRoots": roots}, nil); err != nil {
		a.log.Warn("skills/extraRoots/set failed", "err", err)
	}
	if st.SkillSource != ext.SourceAgent || len(st.DisabledSkills) > 0 || a.ext.LimitsSkills() {
		var res struct {
			Data []struct {
				Skills []struct {
					Name    string `json:"name"`
					Path    string `json:"path"`
					Scope   string `json:"scope"`
					Enabled bool   `json:"enabled"`
				} `json:"skills"`
			} `json:"data"`
		}
		params := map[string]any{"forceReload": true}
		if cwd != "" {
			params["cwds"] = []string{cwd}
		}
		if err := rpc.Call(ctx, "skills/list", params, &res); err != nil {
			a.log.Warn("skills/list failed", "err", err)
		}
		dis := map[string]bool{}
		for _, n := range st.DisabledSkills {
			dis[n] = true
		}
		// codex 报回来的路径是解开过符号链接的（/var → /private/var），比前缀前这边也解一遍。
		under := func(p, dir string) bool {
			if real, err := filepath.EvalSymlinks(dir); err == nil && strings.HasPrefix(p, real+string(filepath.Separator)) {
				return true
			}
			return strings.HasPrefix(p, dir+string(filepath.Separator))
		}
		// skills.config 整个被覆盖掉，所以用户自己在 config.toml 里关掉的也要照抄一遍。
		off := []map[string]any{}
		for _, e := range res.Data {
			for _, sk := range e.Skills {
				var disable bool
				switch {
				case !sk.Enabled:
					disable = true
				case under(sk.Path, a.ext.BuiltinDir()):
					disable = dis[sk.Name]
				case under(sk.Path, a.ext.SkillsDir()):
					disable = dis[sk.Name] || !st.SkillSource.UsesGerdr()
				default:
					disable = st.SkillSource == ext.SourceGerdr && sk.Scope != "system"
				}
				// 角色限定了 skill：没挑的也关。codex 自带的（system）界面上列不出来、挑不了，不动它们。
				if !disable && sk.Scope != "system" && !a.ext.AllowSkill(sk.Name) {
					disable = true
				}
				if disable {
					off = append(off, map[string]any{"path": sk.Path, "enabled": false})
				}
			}
		}
		cfg["skills.config"] = off
	}
	if len(cfg) == 0 {
		return nil, fp
	}
	return cfg, fp
}

// refreshExt：设置改过了就把这个 thread 退订再接回来 —— 对一个已经加载着的 thread 直接
// thread/resume，新的 config 不生效（实测），得先让它卸载。
func (a *Agent) refreshExt(ctx context.Context, rpc *rpcline.Conn, s *session) {
	if a.ext == nil {
		return
	}
	s.mu.Lock()
	stale := s.extFP != a.ext.Fingerprint()
	s.mu.Unlock()
	if !stale {
		return
	}
	if err := rpc.Call(ctx, "thread/unsubscribe", map[string]any{"threadId": s.id}, nil); err != nil {
		a.log.Warn("thread/unsubscribe failed", "err", err)
		return
	}
	if err := a.resumeThread(ctx, rpc, s); err != nil {
		a.log.Warn("re-resume after ext change failed", "err", err)
	}
}

// Close 关掉 app-server。
func (a *Agent) Close() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cmd != nil && a.cmd.Process != nil {
		_ = a.cmd.Process.Kill()
	}
}

// logWriter：app-server 的 stderr 平时只记 Debug（它很吵），但留着最后一段 —— 进程意外退出时跟着那条 Warn 打出来，
// 不然「codex initialize: connection closed」到底为什么一个字都查不到（踩过：它正好在被 npm 升级替换）。
type logWriter struct {
	log  *slog.Logger
	mu   sync.Mutex
	tail string
}

func (w *logWriter) Write(p []byte) (int, error) {
	if s := strings.TrimSpace(string(p)); s != "" {
		w.log.Debug("codex stderr", "line", s)
		w.mu.Lock()
		w.tail = (w.tail + "\n" + s)
		if len(w.tail) > 2000 {
			w.tail = w.tail[len(w.tail)-2000:]
		}
		w.mu.Unlock()
	}
	return len(p), nil
}

func (w *logWriter) Tail() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return strings.TrimSpace(w.tail)
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

func (s *session) finish(r turnResult) {
	s.mu.Lock()
	ch := s.turnDone
	s.turnDone = nil
	s.turnID = ""
	s.mu.Unlock()
	if ch != nil {
		ch <- r
	}
}

// ---- ACP: 初始化 / 会话 ----

func (a *Agent) Initialize(ctx context.Context, p acp.InitializeRequest) (acp.InitializeResponse, error) {
	return acp.InitializeResponse{
		ProtocolVersion: acp.ProtocolVersionNumber,
		AgentInfo:       &acp.Implementation{Name: "gerdr-codex", Title: acp.Ptr("Codex"), Version: "1"},
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

func modeFromMeta(meta map[string]any, def string) string {
	if v, ok := acpx.GetMeta(meta, "mode"); ok {
		if s, ok := v.(string); ok {
			for _, m := range modes {
				if string(m.Id) == s {
					return s
				}
			}
		}
	}
	return def
}

func (a *Agent) NewSession(ctx context.Context, p acp.NewSessionRequest) (acp.NewSessionResponse, error) {
	rpc, err := a.client(ctx)
	if err != nil {
		return acp.NewSessionResponse{}, err
	}
	mode := modeFromMeta(p.Meta, modeAuto)
	ap, sb := modeParams(mode)
	params := map[string]any{"cwd": p.Cwd, "approvalPolicy": ap, "sandbox": sandboxMode(sb), "developerInstructions": a.instructions()}
	cfg, fp := a.extConfig(ctx, rpc, p.Cwd)
	if cfg != nil {
		params["config"] = cfg
	}
	if v, ok := acpx.GetMeta(p.Meta, "model"); ok {
		if m, ok := v.(string); ok && m != "" {
			params["model"] = m
		}
	}
	var res threadStartResp
	if err := rpc.Call(ctx, "thread/start", params, &res); err != nil {
		return acp.NewSessionResponse{}, err
	}
	s := &session{id: res.Thread.ID, cwd: p.Cwd, mode: mode, model: res.Model, items: map[string]*itemState{}, extFP: fp, roll: newRollTail(res.Thread.ID, false)}
	if res.ReasoningEffort != nil {
		s.effort = *res.ReasoningEffort
	}
	a.mu.Lock()
	a.sessions[s.id] = s
	a.mu.Unlock()
	a.announceCommands(s.id)
	return acp.NewSessionResponse{
		SessionId:     acp.SessionId(s.id),
		Modes:         &acp.SessionModeState{AvailableModes: modes, CurrentModeId: acp.SessionModeId(mode)},
		ConfigOptions: a.configOptions(ctx, s),
	}, nil
}

func (a *Agent) ResumeSession(ctx context.Context, p acp.ResumeSessionRequest) (acp.ResumeSessionResponse, error) {
	s, err := a.attach(ctx, string(p.SessionId), p.Cwd, p.Meta)
	if err != nil {
		return acp.ResumeSessionResponse{}, err
	}
	return acp.ResumeSessionResponse{
		Modes:         &acp.SessionModeState{AvailableModes: modes, CurrentModeId: acp.SessionModeId(s.mode)},
		ConfigOptions: a.configOptions(ctx, s),
	}, nil
}

func (a *Agent) attach(ctx context.Context, id, cwd string, meta map[string]any) (*session, error) {
	rpc, err := a.client(ctx)
	if err != nil {
		return nil, err
	}
	mode := modeFromMeta(meta, modeAuto)
	ap, sb := modeParams(mode)
	params := map[string]any{"threadId": id, "excludeTurns": true, "approvalPolicy": ap, "sandbox": sandboxMode(sb), "developerInstructions": a.instructions()}
	if cwd != "" {
		params["cwd"] = cwd
	}
	cfg, fp := a.extConfig(ctx, rpc, cwd)
	if cfg != nil {
		params["config"] = cfg
	}
	var res threadStartResp
	if err := rpc.Call(ctx, "thread/resume", params, &res); err != nil {
		return nil, err
	}
	s := &session{id: id, cwd: res.Thread.Cwd, mode: mode, model: res.Model, items: map[string]*itemState{}, extFP: fp, roll: newRollTail(id, true)}
	if s.cwd == "" {
		s.cwd = cwd
	}
	if res.ReasoningEffort != nil {
		s.effort = *res.ReasoningEffort
	}
	a.mu.Lock()
	if old := a.sessions[id]; old != nil {
		old.mu.Lock()
		s.turnID, s.turnDone, s.compacts = old.turnID, old.turnDone, old.compacts
		if old.roll != nil {
			s.roll = old.roll // keep the read position and the open cards across a re-attach mid-turn
		}
		old.mu.Unlock()
	}
	a.sessions[id] = s
	a.mu.Unlock()
	a.announceCommands(id)
	return s, nil
}

// LoadSession = resume + 把历史当成 session/update 重放一遍（导入在别处开过的会话时用）。
func (a *Agent) LoadSession(ctx context.Context, p acp.LoadSessionRequest) (acp.LoadSessionResponse, error) {
	s, err := a.attach(ctx, string(p.SessionId), p.Cwd, p.Meta)
	if err != nil {
		return acp.LoadSessionResponse{}, err
	}
	rpc, err := a.client(ctx)
	if err != nil {
		return acp.LoadSessionResponse{}, err
	}
	var turns []turn
	cursor := ""
	for i := 0; i < 50; i++ {
		params := map[string]any{"threadId": s.id, "limit": 50, "sortDirection": "asc", "itemsView": "full"}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var res struct {
			Data       []turn  `json:"data"`
			NextCursor *string `json:"nextCursor"`
		}
		if err := rpc.Call(ctx, "thread/turns/list", params, &res); err != nil {
			return acp.LoadSessionResponse{}, err
		}
		turns = append(turns, res.Data...)
		if res.NextCursor == nil || *res.NextCursor == "" {
			break
		}
		cursor = *res.NextCursor
	}
	a.replayTurns(ctx, s, turns)
	return acp.LoadSessionResponse{
		Modes:         &acp.SessionModeState{AvailableModes: modes, CurrentModeId: acp.SessionModeId(s.mode)},
		ConfigOptions: a.configOptions(ctx, s),
	}, nil
}

func (a *Agent) ListSessions(ctx context.Context, p acp.ListSessionsRequest) (acp.ListSessionsResponse, error) {
	rpc, err := a.client(ctx)
	if err != nil {
		return acp.ListSessionsResponse{}, err
	}
	params := map[string]any{"limit": 50, "sortKey": "updated_at"}
	if p.Cwd != nil && *p.Cwd != "" {
		params["cwd"] = *p.Cwd
	}
	if p.Cursor != nil {
		params["cursor"] = *p.Cursor
	}
	var res struct {
		Data       []thread `json:"data"`
		NextCursor *string  `json:"nextCursor"`
	}
	if err := rpc.Call(ctx, "thread/list", params, &res); err != nil {
		// sortKey 的取值各版本不一样，失败就退回默认排序再试一次。
		delete(params, "sortKey")
		if err2 := rpc.Call(ctx, "thread/list", params, &res); err2 != nil {
			return acp.ListSessionsResponse{}, err
		}
	}
	out := acp.ListSessionsResponse{NextCursor: res.NextCursor}
	for _, t := range res.Data {
		if t.Ephemeral {
			continue
		}
		title := t.Preview
		if t.Name != nil && *t.Name != "" {
			title = *t.Name
		}
		up := time.Unix(t.UpdatedAt, 0).UTC().Format(time.RFC3339)
		out.Sessions = append(out.Sessions, acp.SessionInfo{SessionId: acp.SessionId(t.ID), Cwd: t.Cwd, Title: acp.Ptr(oneLine(title, 120)), UpdatedAt: &up})
	}
	return out, nil
}

func (a *Agent) CloseSession(ctx context.Context, p acp.CloseSessionRequest) (acp.CloseSessionResponse, error) {
	a.mu.Lock()
	s := a.sessions[string(p.SessionId)]
	delete(a.sessions, string(p.SessionId))
	rpc := a.rpc
	a.mu.Unlock()
	if s != nil && rpc != nil {
		s.mu.Lock()
		tid := s.turnID
		s.mu.Unlock()
		if tid != "" {
			_ = rpc.Call(ctx, "turn/interrupt", map[string]any{"threadId": s.id, "turnId": tid}, nil)
		}
		_ = rpc.Call(ctx, "thread/unsubscribe", map[string]any{"threadId": s.id}, nil)
	}
	return acp.CloseSessionResponse{}, nil
}

func (a *Agent) SetSessionMode(ctx context.Context, p acp.SetSessionModeRequest) (acp.SetSessionModeResponse, error) {
	s, err := a.session(p.SessionId)
	if err != nil {
		return acp.SetSessionModeResponse{}, err
	}
	ok := false
	for _, m := range modes {
		if m.Id == p.ModeId {
			ok = true
		}
	}
	if !ok {
		return acp.SetSessionModeResponse{}, acp.NewInvalidParams(map[string]any{"modeId": p.ModeId})
	}
	s.mu.Lock()
	s.mode = string(p.ModeId)
	s.mu.Unlock()
	// 下一轮 turn/start 时带上（审批策略和沙箱是按轮覆盖的）。
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
	s.mu.Lock()
	switch string(p.ValueId.ConfigId) {
	case "model":
		s.model = string(p.ValueId.Value)
		// 换了模型，原来的思考强度不一定还认，退回这个模型的默认值。
		s.effort = ""
	case "effort":
		s.effort = string(p.ValueId.Value)
	default:
		s.mu.Unlock()
		return acp.SetSessionConfigOptionResponse{}, acp.NewInvalidParams(map[string]any{"configId": p.ValueId.ConfigId})
	}
	s.mu.Unlock()
	return acp.SetSessionConfigOptionResponse{ConfigOptions: a.configOptions(ctx, s)}, nil
}

func (a *Agent) configOptions(ctx context.Context, s *session) []acp.SessionConfigOption {
	models := a.listModels(ctx)
	if len(models) == 0 {
		return nil
	}
	s.mu.Lock()
	cur, effort := s.model, s.effort
	s.mu.Unlock()
	var opts acp.SessionConfigSelectOptionsUngrouped
	var curModel *modelInfo
	for i, m := range models {
		if m.Hidden && m.Model != cur {
			continue
		}
		name := m.DisplayName
		if name == "" {
			name = m.Model
		}
		opts = append(opts, acp.SessionConfigSelectOption{Value: acp.SessionConfigValueId(m.Model), Name: name, Description: nilIfEmpty(m.Description)})
		if m.Model == cur {
			curModel = &models[i]
		}
	}
	if cur == "" {
		for i, m := range models {
			if m.IsDefault {
				cur, curModel = m.Model, &models[i]
			}
		}
	}
	out := []acp.SessionConfigOption{{Select: &acp.SessionConfigOptionSelect{
		Id: "model", Name: "Model", Type: "select", Category: acp.Ptr(acp.SessionConfigOptionCategoryModel),
		CurrentValue: acp.SessionConfigValueId(cur), Options: acp.SessionConfigSelectOptions{Ungrouped: &opts},
	}}}
	if curModel != nil && len(curModel.SupportedReasoningEfforts) > 0 {
		if effort == "" {
			effort = curModel.DefaultReasoningEffort
		}
		var eo acp.SessionConfigSelectOptionsUngrouped
		for _, e := range curModel.SupportedReasoningEfforts {
			eo = append(eo, acp.SessionConfigSelectOption{Value: acp.SessionConfigValueId(e.ReasoningEffort), Name: e.ReasoningEffort, Description: nilIfEmpty(e.Description)})
		}
		out = append(out, acp.SessionConfigOption{Select: &acp.SessionConfigOptionSelect{
			Id: "effort", Name: "Effort", Type: "select", Category: acp.Ptr(acp.SessionConfigOptionCategoryThoughtLevel),
			CurrentValue: acp.SessionConfigValueId(effort), Options: acp.SessionConfigSelectOptions{Ungrouped: &eo},
		}})
	}
	return out
}

func (a *Agent) listModels(ctx context.Context) []modelInfo {
	a.mu.Lock()
	if a.models != nil {
		m := a.models
		a.mu.Unlock()
		return m
	}
	a.mu.Unlock()
	rpc, err := a.client(ctx)
	if err != nil {
		return nil
	}
	var res struct {
		Data []modelInfo `json:"data"`
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := rpc.Call(cctx, "model/list", map[string]any{"limit": 100}, &res); err != nil {
		a.log.Warn("model/list failed", "err", err)
		return nil
	}
	a.mu.Lock()
	a.models = res.Data
	a.mu.Unlock()
	return res.Data
}

// clear：/clear（codex TUI 里叫 /new）。app-server 没有「清空 thread」，TUI 也是另开一个 thread：
// 照原来的目录 / 权限 / 模型 thread/start 一个新的，然后和 claude 的 /clear 走同一条路 —— 用旧 id 发
// sessionId + cleared 让 hub 把记录换成新 id（之后回退、恢复都按新的来，对话里留一条「上下文已清空」）。
// 不接的话 "/clear" 会当成一句普通的话发给模型，它回一句「已清空」，上下文其实原样都在（用户踩过）。
// 旧 id 留作别名（同 claude 的 adoptID），hub 换 id 之前路上的调用照样找得到。
func (a *Agent) clear(ctx context.Context, rpc *rpcline.Conn, s *session) (acp.PromptResponse, error) {
	s.mu.Lock()
	old, cwd, mode, model := s.id, s.cwd, s.mode, s.model
	s.mu.Unlock()
	ap, sb := modeParams(mode)
	params := map[string]any{"cwd": cwd, "approvalPolicy": ap, "sandbox": sandboxMode(sb), "developerInstructions": a.instructions()}
	cfg, fp := a.extConfig(ctx, rpc, cwd)
	if cfg != nil {
		params["config"] = cfg
	}
	if model != "" {
		params["model"] = model
	}
	var res threadStartResp
	if err := rpc.Call(ctx, "thread/start", params, &res); err != nil {
		return acp.PromptResponse{}, err
	}
	a.send(ctx, old, acp.SessionUpdate{SessionInfoUpdate: &acp.SessionSessionInfoUpdate{Meta: acpx.Meta(map[string]any{"sessionId": res.Thread.ID, "cleared": true})}})
	s.mu.Lock()
	s.id, s.extFP = res.Thread.ID, fp
	s.compacts = nil
	s.items = map[string]*itemState{}
	s.mu.Unlock()
	a.mu.Lock()
	a.sessions[res.Thread.ID] = s
	a.mu.Unlock()
	// 旧 thread 不再用：卸载掉，别让 app-server 一直挂着它
	_ = rpc.Call(ctx, "thread/unsubscribe", map[string]any{"threadId": old}, nil)
	return acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
}

// ---- ACP: 一轮 ----

func (a *Agent) Prompt(ctx context.Context, p acp.PromptRequest) (acp.PromptResponse, error) {
	s, err := a.session(p.SessionId)
	if err != nil {
		return acp.PromptResponse{}, err
	}
	rpc, err := a.client(ctx)
	if err != nil {
		return acp.PromptResponse{}, err
	}
	s.mu.Lock()
	busy := s.turnDone != nil
	s.mu.Unlock()
	if !busy && slashCommand(p.Prompt) == "clear" {
		return a.clear(ctx, rpc, s)
	}
	if !busy {
		a.refreshExt(ctx, rpc, s)
	}
	s.mu.Lock()
	if s.turnDone != nil {
		s.mu.Unlock()
		return acp.PromptResponse{}, acp.NewInvalidRequest("a turn is already running; use _session/steering")
	}
	done := make(chan turnResult, 1)
	s.turnDone = done
	s.cancel = false
	s.errMsg = ""
	mode, model, effort := s.mode, s.model, s.effort
	s.mu.Unlock()

	var turnID string
	compact := slashCommand(p.Prompt) == "compact"
	if compact {
		// 手动压缩在 app-server 里就是普通的一轮（turn/started → contextCompaction → turn/completed），
		// 只是响应里不带 turn id：等 turn/started 填进 s.turnID。
		if err := rpc.Call(ctx, "thread/compact/start", map[string]any{"threadId": s.id}, nil); err != nil {
			s.mu.Lock()
			s.turnDone = nil
			s.mu.Unlock()
			return acp.PromptResponse{}, err
		}
	} else {
		ap, sb := modeParams(mode)
		params := map[string]any{
			"threadId":       s.id,
			"input":          toInputs(p.Prompt),
			"approvalPolicy": ap,
			"sandboxPolicy":  sb,
		}
		if p.MessageId != nil {
			params["clientUserMessageId"] = *p.MessageId
		}
		if model != "" {
			params["model"] = model
		}
		if effort != "" {
			params["effort"] = effort
		}
		var res turnResp
		if err := rpc.Call(ctx, "turn/start", params, &res); err != nil {
			s.mu.Lock()
			s.turnDone = nil
			s.mu.Unlock()
			return acp.PromptResponse{}, err
		}
		turnID = res.Turn.ID
		s.mu.Lock()
		if s.turnDone == done && s.turnID == "" {
			s.turnID = turnID
		}
		s.mu.Unlock()
	}

	var r turnResult
	select {
	case r = <-done:
	case <-ctx.Done():
		// acp-go-sdk 收到 session/cancel 时会顺手取消这个 Prompt 请求的 ctx（连接断了也会）。
		// 照样打断对面，并且**等这一轮真的收尾**再返回 —— 不等的话 client 以为这一轮完了，
		// 紧接着发的回退 / 新消息会撞上「还有一轮在跑」（e2e 测出来的）。
		if turnID == "" {
			s.mu.Lock()
			if s.turnDone == done {
				turnID = s.turnID
			}
			s.mu.Unlock()
		}
		if turnID != "" {
			ictx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			_ = rpc.Call(ictx, "turn/interrupt", map[string]any{"threadId": s.id, "turnId": turnID}, nil)
			cancel()
		}
		select {
		case <-done:
		case <-time.After(30 * time.Second):
			s.mu.Lock()
			if s.turnDone == done {
				s.turnDone, s.turnID = nil, ""
			}
			s.mu.Unlock()
		}
		return acp.PromptResponse{StopReason: acp.StopReasonCancelled}, nil
	}
	s.mu.Lock()
	cancelled := s.cancel
	if compact && p.MessageId != nil && r.id != "" {
		if s.compacts == nil {
			s.compacts = map[string]string{}
		}
		s.compacts[*p.MessageId] = r.id
	}
	s.mu.Unlock()
	switch r.status {
	case "interrupted":
		return acp.PromptResponse{StopReason: acp.StopReasonCancelled}, nil
	case "failed":
		if cancelled {
			return acp.PromptResponse{StopReason: acp.StopReasonCancelled}, nil
		}
		msg := "turn failed"
		if r.err != nil && r.err.Message != "" {
			msg = r.err.Message
			if r.err.AdditionalDetails != nil && *r.err.AdditionalDetails != "" {
				msg += "\n" + *r.err.AdditionalDetails
			}
		}
		return acp.PromptResponse{}, acp.NewInternalError(map[string]any{"message": msg})
	}
	return acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
}

func (a *Agent) Cancel(ctx context.Context, p acp.CancelNotification) error {
	s, err := a.session(p.SessionId)
	if err != nil {
		return nil
	}
	s.mu.Lock()
	tid := s.turnID
	s.cancel = true
	s.mu.Unlock()
	if tid == "" {
		return nil
	}
	a.mu.Lock()
	rpc := a.rpc
	a.mu.Unlock()
	if rpc == nil {
		return nil
	}
	go func() {
		cctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := rpc.Call(cctx, "turn/interrupt", map[string]any{"threadId": s.id, "turnId": tid}, nil); err != nil {
			a.log.Warn("turn/interrupt failed", "err", err)
		}
	}()
	return nil
}

// complete：用 codex exec 跑一次小问答（hub 起会话标题用，acpx.MethodComplete）。用默认模型、低推理档，
// --ephemeral 不留 rollout（不然「接着之前的聊」列表里会多出一堆起标题的会话），只读沙箱、在临时目录里跑。
// 不带 opts.Args：那是给 app-server 的参数，exec 不一定认。一次大约 15 秒、1 万多 token（多半是 codex 自带的
// 系统提示，走缓存），所以 hub 只在几个节点上调它。
func (a *Agent) complete(ctx context.Context, prompt string) (string, error) {
	bin, err := sysenv.FindCodex()
	if a.opts.Bin != "" {
		bin, err = sysenv.FindBin(a.opts.Bin)
	}
	if err != nil {
		return "", err
	}
	f, err := os.CreateTemp("", "gerdr-complete-*.txt")
	if err != nil {
		return "", err
	}
	f.Close()
	defer os.Remove(f.Name())
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	// 结果从 -o 的文件拿：stdout 上还混着进度和 hook 的输出
	cmd := exec.CommandContext(ctx, bin, "exec", "--ephemeral", "--skip-git-repo-check", "-s", "read-only",
		"-c", "model_reasoning_effort=low", "-o", f.Name(), "-")
	cmd.Stdin = strings.NewReader(prompt)
	cmd.Dir = os.TempDir()
	cmd.Env = append(sysenv.ChildEnv(), a.opts.Env...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("codex exec: %w: %s", err, oneLineTail(out))
	}
	b, err := os.ReadFile(f.Name())
	return strings.TrimSpace(string(b)), err
}

func oneLineTail(b []byte) string {
	s := strings.Join(strings.Fields(string(b)), " ")
	if r := []rune(s); len(r) > 200 {
		s = string(r[len(r)-200:])
	}
	return s
}

// ---- 扩展方法 ----

func (a *Agent) HandleExtensionMethod(ctx context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case acpx.MethodUsage:
		return a.usage(ctx)
	case acpx.MethodComplete:
		var p acpx.CompleteRequest
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, acp.NewInvalidParams(err.Error())
		}
		t, err := a.complete(ctx, p.Prompt)
		return acpx.CompleteResponse{Text: t}, err
	case acpx.MethodTranscriptStat:
		// 别处（终端 / Codex App）接着聊过：rollout 文件会长（hub 的 CatchUp 拿它判断要不要重新载入）
		var p acpx.TranscriptStatRequest
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, acp.NewInvalidParams(err.Error())
		}
		path := a.rolloutPath(string(p.SessionId))
		if path == "" {
			return nil, fmt.Errorf("找不到 thread %s 的记录", p.SessionId)
		}
		st, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		res := acpx.TranscriptStatResponse{Size: st.Size(), ModTime: st.ModTime().UnixMilli()}
		if p.Since >= 0 {
			res.NewUserTexts = rolloutUserTexts(path, p.Since)
		}
		return res, nil
	case acpx.MethodTakeover:
		// codex 自己保证一个 thread 同一时间只有一处能写：别处（终端 / Codex App）开着时 thread/resume 直接报
		// 「already has an active writer」，不会分叉（实测）。所以这里只有 fork：thread/fork 复制出一个新 thread
		// 接着聊（不用先接上原来那个 —— 它正被别处占着，也接不上）。「关掉那边」做不了：没有登记文件，不知道是哪个进程。
		var p acpx.TakeoverRequest
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, acp.NewInvalidParams(err.Error())
		}
		if p.Action != "fork" {
			return nil, acp.NewInvalidParams(map[string]any{"action": p.Action, "error": "codex 只能另起一份"})
		}
		rpc, err := a.client(ctx)
		if err != nil {
			return nil, err
		}
		var res struct {
			Thread struct {
				ID string `json:"id"`
			} `json:"thread"`
		}
		if err := rpc.Call(ctx, "thread/fork", map[string]any{"threadId": string(p.SessionId)}, &res); err != nil {
			return nil, err
		}
		return acpx.TakeoverResponse{SessionId: acp.SessionId(res.Thread.ID)}, nil
	case acpx.MethodSteer:
		var p acpx.SteerRequest
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, acp.NewInvalidParams(err.Error())
		}
		return a.steer(ctx, p)
	case acpx.MethodRewind:
		var p acpx.RewindRequest
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, acp.NewInvalidParams(err.Error())
		}
		return a.rewind(ctx, p)
	}
	return nil, acp.NewMethodNotFound(method)
}

func (a *Agent) steer(ctx context.Context, p acpx.SteerRequest) (acpx.SteerResponse, error) {
	s, err := a.session(p.SessionId)
	if err != nil {
		return acpx.SteerResponse{}, err
	}
	s.mu.Lock()
	tid := s.turnID
	s.mu.Unlock()
	if tid == "" {
		return acpx.SteerResponse{Outcome: acpx.SteerPromptRequired}, nil
	}
	rpc, err := a.client(ctx)
	if err != nil {
		return acpx.SteerResponse{}, err
	}
	params := map[string]any{"threadId": s.id, "expectedTurnId": tid, "input": toInputs(p.Prompt)}
	if p.MessageId != "" {
		params["clientUserMessageId"] = p.MessageId
	}
	if err := rpc.Call(ctx, "turn/steer", params, nil); err != nil {
		// 撞上这一轮刚好结束：让 client 改发 session/prompt。
		s.mu.Lock()
		still := s.turnID == tid
		s.mu.Unlock()
		if !still || strings.Contains(strings.ToLower(err.Error()), "active turn") {
			return acpx.SteerResponse{Outcome: acpx.SteerPromptRequired}, nil
		}
		return acpx.SteerResponse{}, err
	}
	return acpx.SteerResponse{Outcome: acpx.SteerInjected}, nil
}

// rewind 用 thread/revert 就地截掉那一轮和之后的。那条消息是哪一轮的，靠当初 turn/start 带的
// clientUserMessageId —— codex 把它存成了 userMessage item 的 clientId，所以重启之后也找得到。
func (a *Agent) rewind(ctx context.Context, p acpx.RewindRequest) (acpx.RewindResponse, error) {
	s, err := a.session(p.SessionId)
	if err != nil {
		return acpx.RewindResponse{}, err
	}
	s.mu.Lock()
	busy := s.turnDone != nil
	s.mu.Unlock()
	if busy {
		return acpx.RewindResponse{}, acp.NewInvalidRequest("cannot rewind while a turn is running")
	}
	rpc, err := a.client(ctx)
	if err != nil {
		return acpx.RewindResponse{}, err
	}
	s.mu.Lock()
	turnID := s.compacts[p.MessageId]
	s.mu.Unlock()
	if turnID == "" {
		turnID, err = a.findTurn(ctx, rpc, s.id, p.MessageId)
		if err != nil {
			return acpx.RewindResponse{}, err
		}
	}
	if err := rpc.Call(ctx, "thread/revert", map[string]any{"threadId": s.id, "beforeTurnId": turnID}, nil); err != nil {
		return acpx.RewindResponse{}, err
	}
	return acpx.RewindResponse{SessionId: p.SessionId}, nil
}

func (a *Agent) findTurn(ctx context.Context, rpc *rpcline.Conn, threadID, messageID string) (string, error) {
	cursor := ""
	for i := 0; i < 100; i++ {
		params := map[string]any{"threadId": threadID, "limit": 50, "sortDirection": "desc", "itemsView": "full"}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var res struct {
			Data       []turn  `json:"data"`
			NextCursor *string `json:"nextCursor"`
		}
		if err := rpc.Call(ctx, "thread/turns/list", params, &res); err != nil {
			return "", err
		}
		for _, t := range res.Data {
			for _, raw := range t.Items {
				var h itemHead
				_ = json.Unmarshal(raw, &h)
				if h.Type != "userMessage" {
					continue
				}
				var um userMessageItem
				_ = json.Unmarshal(raw, &um)
				if um.ClientID != nil && *um.ClientID == messageID {
					return t.ID, nil
				}
			}
		}
		if res.NextCursor == nil || *res.NextCursor == "" {
			break
		}
		cursor = *res.NextCursor
	}
	return "", acp.NewInvalidParams(map[string]any{"messageId": messageID, "error": "message not found in thread"})
}

// ---- 对端发来的请求（审批 / 提问） ----

func (a *Agent) HandleRequest(ctx context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case "item/commandExecution/requestApproval":
		var p commandApprovalParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		title := "Run command"
		if p.Command != nil {
			title = stripShell(*p.Command)
			// gerdr 自己的 session / ui 命令（多个会话协作）：直接放行，见 sysenv.SafeGerdrCLI。
			if sysenv.SafeGerdrCLI(title) {
				return map[string]any{"decision": "accept"}, nil
			}
		}
		raw := map[string]any{"command": title}
		if p.Cwd != nil {
			raw["cwd"] = *p.Cwd
		}
		if p.Reason != nil {
			raw["reason"] = *p.Reason
		}
		opt, err := a.askPermission(ctx, p.ThreadID, p.ItemID, title, acp.ToolKindExecute, raw, true)
		if err != nil {
			return map[string]any{"decision": "cancel"}, nil
		}
		return map[string]any{"decision": decision(opt)}, nil
	case "item/fileChange/requestApproval":
		var p fileApprovalParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		raw := map[string]any{}
		if p.Reason != nil {
			raw["reason"] = *p.Reason
		}
		if p.GrantRoot != nil {
			raw["grantRoot"] = *p.GrantRoot
		}
		title := a.itemTitle(p.ThreadID, p.ItemID, "Edit files")
		opt, err := a.askPermission(ctx, p.ThreadID, p.ItemID, title, acp.ToolKindEdit, raw, true)
		if err != nil {
			return map[string]any{"decision": "cancel"}, nil
		}
		return map[string]any{"decision": decision(opt)}, nil
	case "item/permissions/requestApproval":
		var p permissionsApprovalParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		var perms any
		_ = json.Unmarshal(p.Permissions, &perms)
		raw := map[string]any{"permissions": perms, "cwd": p.Cwd}
		if p.Reason != nil {
			raw["reason"] = *p.Reason
		}
		opt, err := a.askPermission(ctx, p.ThreadID, p.ItemID, "Grant additional permissions", acp.ToolKindOther, raw, true)
		if err != nil || strings.HasPrefix(opt, "reject") {
			return map[string]any{"permissions": map[string]any{}, "scope": "turn"}, nil
		}
		scope := "turn"
		if opt == "allow_always" {
			scope = "session"
		}
		return map[string]any{"permissions": perms, "scope": scope}, nil
	case "item/tool/requestUserInput":
		var p userInputParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		req := acpx.AskRequest{SessionId: acp.SessionId(p.ThreadID), ToolCallId: p.ItemID}
		for _, q := range p.Questions {
			aq := acpx.AskQuestion{Id: q.ID, Header: q.Header, Question: q.Question, AllowOther: q.IsOther || len(q.Options) == 0, Secret: q.IsSecret}
			for _, o := range q.Options {
				aq.Options = append(aq.Options, acpx.AskOption{Label: o.Label, Description: o.Description})
			}
			req.Questions = append(req.Questions, aq)
		}
		var res acpx.AskResponse
		raw, err := a.conn.CallExtension(ctx, acpx.MethodAsk, req)
		if err == nil {
			err = json.Unmarshal(raw, &res)
		}
		answers := map[string]any{}
		if err == nil && !res.Cancelled {
			for id, v := range res.Answers {
				answers[id] = map[string]any{"answers": v}
			}
		} else {
			// codex 的 requestUserInput 没有「拒绝」这一档，只能回答案：每道题都填上「为什么没答」的说明。
			// 空着回的话 codex 看到的是一份空答卷，不知道发生了什么（和 claude 那边「被关掉了」同一个毛病）
			why := acpx.AskDeclined(res.Reason, res.Text)
			for _, q := range req.Questions {
				answers[q.Id] = map[string]any{"answers": []string{why}}
			}
		}
		return map[string]any{"answers": answers}, nil
	case "mcpServer/elicitation/request":
		// MCP 服务器要表单：gerdr 还没有通用表单界面，先拒绝（agent 会照常往下走）。
		return map[string]any{"action": "decline", "content": nil, "_meta": nil}, nil
	}
	return nil, &rpcline.Error{Code: -32601, Message: "gerdr does not handle " + method}
}

func decision(opt string) string {
	switch opt {
	case "allow_once":
		return "accept"
	case "allow_always":
		return "acceptForSession"
	case "reject_once", "reject_always":
		return "decline"
	}
	return "cancel"
}

func (a *Agent) askPermission(ctx context.Context, threadID, itemID, title string, kind acp.ToolKind, raw map[string]any, always bool) (string, error) {
	opts := []acp.PermissionOption{
		{OptionId: "allow_once", Name: "允许", Kind: acp.PermissionOptionKindAllowOnce},
	}
	if always {
		opts = append(opts, acp.PermissionOption{OptionId: "allow_always", Name: "本会话都允许", Kind: acp.PermissionOptionKindAllowAlways})
	}
	opts = append(opts, acp.PermissionOption{OptionId: "reject_once", Name: "拒绝", Kind: acp.PermissionOptionKindRejectOnce})
	res, err := a.conn.RequestPermission(ctx, acp.RequestPermissionRequest{
		SessionId: acp.SessionId(threadID),
		ToolCall: acp.ToolCallUpdate{
			ToolCallId: acp.ToolCallId(itemID),
			Title:      acp.Ptr(title),
			Kind:       acp.Ptr(kind),
			Status:     acp.Ptr(acp.ToolCallStatusPending),
			RawInput:   raw,
		},
		Options: opts,
	})
	if err != nil {
		return "", err
	}
	if res.Outcome.Selected != nil {
		return string(res.Outcome.Selected.OptionId), nil
	}
	return "", errors.New("cancelled")
}

func (a *Agent) itemTitle(threadID, itemID, def string) string {
	a.mu.Lock()
	s := a.sessions[threadID]
	a.mu.Unlock()
	if s == nil {
		return def
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if it := s.items[itemID]; it != nil && it.title != "" {
		return it.title
	}
	return def
}

// ---- 杂项 ----

func modeParams(mode string) (approval any, sandbox map[string]any) {
	ws := map[string]any{"type": "workspaceWrite", "writableRoots": []string{}, "networkAccess": false, "excludeTmpdirEnvVar": false, "excludeSlashTmp": false}
	switch mode {
	case modeAsk:
		return "untrusted", ws
	case modeReadOnly:
		return "on-request", map[string]any{"type": "readOnly", "networkAccess": false}
	case modeFull:
		return "never", map[string]any{"type": "dangerFullAccess"}
	}
	return "on-request", ws
}

func sandboxMode(sb map[string]any) string {
	switch sb["type"] {
	case "readOnly":
		return "read-only"
	case "dangerFullAccess":
		return "danger-full-access"
	}
	return "workspace-write"
}

func toInputs(blocks []acp.ContentBlock) []userInput {
	var out []userInput
	for _, b := range blocks {
		switch {
		case b.Text != nil:
			out = append(out, userInput{Type: "text", Text: b.Text.Text, TextElements: []any{}})
		case b.Image != nil:
			url := b.Image.Uri
			if url == nil || *url == "" || !strings.HasPrefix(*url, "http") {
				out = append(out, userInput{Type: "image", URL: "data:" + b.Image.MimeType + ";base64," + b.Image.Data})
			} else {
				out = append(out, userInput{Type: "image", URL: *url})
			}
		case b.ResourceLink != nil:
			// codex 的 mention 要 name + path，而一个裸路径给模型看就够了（它自己会去读）。
			path := strings.TrimPrefix(b.ResourceLink.Uri, "file://")
			out = append(out, userInput{Type: "text", Text: "@" + path, TextElements: []any{}})
		case b.Resource != nil:
			if b.Resource.Resource.TextResourceContents != nil {
				r := b.Resource.Resource.TextResourceContents
				out = append(out, userInput{Type: "text", Text: fmt.Sprintf("<file path=%q>\n%s\n</file>", strings.TrimPrefix(r.Uri, "file://"), r.Text), TextElements: []any{}})
			}
		}
	}
	if len(out) == 0 {
		out = append(out, userInput{Type: "text", Text: "", TextElements: []any{}})
	}
	return out
}

// stripShell 把 `/bin/zsh -lc 'echo hi'` 剥成 `echo hi`（codex 把每条命令都包一层登录 shell）。
func stripShell(cmd string) string {
	for _, pre := range []string{"/bin/zsh -lc ", "/bin/bash -lc ", "bash -lc ", "zsh -lc ", "/bin/sh -c ", "sh -c "} {
		if strings.HasPrefix(cmd, pre) {
			rest := strings.TrimPrefix(cmd, pre)
			if len(rest) >= 2 && (rest[0] == '\'' && rest[len(rest)-1] == '\'') {
				return strings.ReplaceAll(rest[1:len(rest)-1], `'\''`, `'`)
			}
			if len(rest) >= 2 && rest[0] == '"' && rest[len(rest)-1] == '"' {
				return rest[1 : len(rest)-1]
			}
			return rest
		}
	}
	return cmd
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

var _ acp.Agent = (*Agent)(nil)
var _ acp.AgentLoader = (*Agent)(nil)
var _ io.Writer = (*logWriter)(nil)

// rolloutPath：thread 的记录文件 <CODEX_HOME>/sessions/YYYY/MM/DD/rollout-<时间>-<thread id>.jsonl。
// CODEX_HOME 以这个实例配的为准（另一个账号的 codex 变体），再看环境、最后 ~/.codex。
func (a *Agent) rolloutPath(id string) string {
	if id == "" || strings.ContainsAny(id, "/*?[") {
		return ""
	}
	home := os.Getenv("CODEX_HOME")
	for _, kv := range a.opts.Env {
		if v, ok := strings.CutPrefix(kv, "CODEX_HOME="); ok {
			home = v
		}
	}
	if home == "" {
		h, _ := os.UserHomeDir()
		home = filepath.Join(h, ".codex")
	}
	m, _ := filepath.Glob(filepath.Join(home, "sessions", "*", "*", "*", "rollout-*-"+id+".jsonl"))
	if len(m) == 0 {
		return ""
	}
	return m[0]
}

// rolloutUserTexts：rollout 文件 offset 之后人说的话（event_msg / user_message）。
func rolloutUserTexts(path string, offset int64) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil || offset > st.Size() {
		offset = 0
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil
	}
	var out []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 256<<20)
	for sc.Scan() {
		if !bytes.Contains(sc.Bytes(), []byte(`"user_message"`)) {
			continue
		}
		var l struct {
			Type    string `json:"type"`
			Payload struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"payload"`
		}
		if json.Unmarshal(sc.Bytes(), &l) == nil && l.Type == "event_msg" && l.Payload.Type == "user_message" {
			if t := strings.TrimSpace(l.Payload.Message); t != "" {
				out = append(out, t)
			}
		}
	}
	return out
}
