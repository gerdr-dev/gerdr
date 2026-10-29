// Package claude 是 Claude Code 的 ACP 适配器：对上实现 ACP agent，对下直接驱动 `claude` CLI
// 的 stream-json 模式。参考的是 agentclientprotocol/claude-agent-acp（TS，依赖 Agent SDK），
// 这里不经过 SDK —— 少一个 node 进程。
//
// 进程模型：**一个会话一个 claude 进程**（这是 CLI 的结构，躲不掉），每个 200–400MB。
// 所以闲着的会话会被收掉（Options.IdleTimeout），下一轮再 `--resume` 起来 —— 同时开着
// 十几个会话、实际在跑的只有两三个，是这个设计要省的那部分内存。
package claude

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coder/acp-go-sdk"
	"github.com/google/uuid"

	"github.com/gerdr-dev/gerdr/internal/acpx"
	"github.com/gerdr-dev/gerdr/internal/ext"
	"github.com/gerdr-dev/gerdr/internal/sysenv"
)

type Options struct {
	// IdleTimeout：会话闲着多久就收掉它的进程。0 = 默认 15 分钟；负数 = 永不。
	IdleTimeout time.Duration
	// Ext：skill / MCP 从哪来（nil = 全用 claude 自己的）。改了之后每个会话下一轮前换进程。
	Ext *ext.Manager
	// 下面三样是「另一个 claude」用的（设置里配的变体，比如换一个账号）：Bin 换可执行文件，Args 追加在
	// 我们自己那串参数后面，Env（KEY=VALUE）盖在继承来的环境上。账号靠 env 切：ANTHROPIC_API_KEY /
	// ANTHROPIC_BASE_URL，或者 CLAUDE_CONFIG_DIR（登录态、会话记录都跟着这个目录走）。
	Bin  string
	Args []string
	Env  []string
	// Prompt：角色（设置 → Agent）追加的系统提示，和 MediaHint 一起走 --append-system-prompt。
	Prompt string
}

// configDir：这个实例的 claude 把东西写在哪（CLAUDE_CONFIG_DIR，变体里配的优先）。会话记录在它的 projects/ 下 ——
// 两个账号各一个目录时，A 的会话在 B 的目录里根本找不到，所以这个不能是包级的。
func (a *Agent) projectsDir() string {
	d := ""
	for _, kv := range a.opts.Env {
		if v, ok := strings.CutPrefix(kv, "CLAUDE_CONFIG_DIR="); ok {
			d = v
		}
	}
	if d == "" {
		d = os.Getenv("CLAUDE_CONFIG_DIR")
	}
	if d == "" {
		home, _ := os.UserHomeDir()
		d = filepath.Join(home, ".claude")
	}
	if strings.HasPrefix(d, "~/") {
		home, _ := os.UserHomeDir()
		d = filepath.Join(home, d[2:])
	}
	return filepath.Join(d, "projects")
}

// loginHint：「Not logged in · Please run /login」在 gerdr 里没法照做（这里不是终端，/login 要交互）。
// 给出能在终端里照抄的那条命令 —— 带着这个实例的 CLAUDE_CONFIG_DIR，不然登进去的是原版那个账号。
// 注意设了 CLAUDE_CONFIG_DIR 之后，哪怕指的就是 ~/.claude，登录态也是另一份（钥匙串里按目录分开存），一样要登一次。
func (a *Agent) loginHint(msg string) string {
	if !strings.Contains(msg, "Not logged in") && !strings.Contains(msg, "/login") {
		return msg
	}
	cmd := "claude"
	if a.opts.Bin != "" {
		cmd = a.opts.Bin
	}
	for _, kv := range a.opts.Env {
		if strings.HasPrefix(kv, "CLAUDE_CONFIG_DIR=") {
			cmd = kv + " " + cmd
		}
	}
	return msg + "\n\n在终端里跑 `" + cmd + "`，进去之后输入 /login 登录，完了 /exit，再回来重试。"
}

// defaultEffort：claude 不带 --effort 起来时用哪一档 —— settings 里的 effortLevel，后读的盖前面的：
// 用户级（这个实例的配置目录）→ 项目 .claude/settings.json → .claude/settings.local.json。
// modelSettings.<模型 id>.effortLevel 是按模型覆盖的，比顶层的优先：只看顶层的话，顶层写 xhigh、
// Opus 单独写 medium 时会显示成 xhigh，实际跑的却是 medium。model 传解析后的完整 id（claude-opus-5-5）。
func (a *Agent) defaultEffort(cwd, model string) string {
	files := []string{filepath.Join(filepath.Dir(a.projectsDir()), "settings.json")}
	if cwd != "" {
		files = append(files, filepath.Join(cwd, ".claude", "settings.json"), filepath.Join(cwd, ".claude", "settings.local.json"))
	}
	// 模型 id 可能带 [1m] 这种后缀，modelSettings 的键不带
	if i := strings.IndexByte(model, '['); i > 0 {
		model = model[:i]
	}
	general, perModel := "", ""
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var st struct {
			EffortLevel   string `json:"effortLevel"`
			ModelSettings map[string]struct {
				EffortLevel string `json:"effortLevel"`
			} `json:"modelSettings"`
		}
		if json.Unmarshal(b, &st) != nil {
			continue
		}
		if st.EffortLevel != "" {
			general = st.EffortLevel
		}
		if ms, ok := st.ModelSettings[model]; ok && model != "" && ms.EffortLevel != "" {
			perModel = ms.EffortLevel
		}
	}
	if perModel != "" {
		return perModel
	}
	return general
}

// complete：用 haiku 跑一次小问答（hub 起会话标题用，acpx.MethodComplete）。用这个实例自己的可执行文件和环境
// （另一个账号的 claude 就记在那个账号上），不落会话记录、不给工具。
func (a *Agent) complete(ctx context.Context, prompt string) (string, error) {
	bin, err := a.bin()
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	// 提示词走 stdin：--tools 是变长参数，提示词跟在它后面会被当成工具名吞掉（实测：报「没有输入」）
	cmd := exec.CommandContext(ctx, bin, "-p", "--model", "haiku", "--no-session-persistence", "--output-format", "text", "--tools", "")
	cmd.Stdin = strings.NewReader(prompt)
	cmd.Dir = os.TempDir()
	cmd.Env = append(sysenv.ChildEnv(), a.opts.Env...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (a *Agent) bin() (string, error) {
	if a.opts.Bin != "" {
		return sysenv.FindBin(a.opts.Bin)
	}
	return sysenv.FindClaude()
}

var modes = []acp.SessionMode{
	{Id: "default", Name: "Default", Description: acp.Ptr("改文件、跑命令前都问你")},
	{Id: "acceptEdits", Name: "Accept edits", Description: acp.Ptr("改文件不问，跑命令照样问")},
	{Id: "plan", Name: "Plan", Description: acp.Ptr("只读，先出计划给你批")},
	{Id: "auto", Name: "Auto", Description: acp.Ptr("由分类器判断哪些要问")},
	{Id: "bypassPermissions", Name: "Bypass", Description: acp.Ptr("什么都不问")},
}

type Agent struct {
	log  *slog.Logger
	opts Options
	conn *acp.AgentSideConnection

	mu       sync.Mutex
	sessions map[string]*session
	models   []cliModel
	commands []cliCommand
	// skills：init 消息里报的 skill 名字（initialize 的命令列表不分，见 cliCommand.Builtin）。进程之间共用，
	// 下一个进程的 initialize 一回来就能标对，不用再等它的 init
	skills  map[string]bool
	defMode string

	stopReaper chan struct{}
}

type cliModel struct {
	Value                 string   `json:"value"`
	ResolvedModel         string   `json:"resolvedModel"`
	DisplayName           string   `json:"displayName"`
	Description           string   `json:"description"`
	SupportsEffort        bool     `json:"supportsEffort"`
	SupportedEffortLevels []string `json:"supportedEffortLevels"`
}

type cliCommand struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	ArgumentHint string `json:"argumentHint"`
	// Builtin：Claude Code 自带的命令（/compact、/clear……）。不是 builtin 的是用户 / 项目 / 插件的 skill 和自定义命令；
	// 但自带的里也有几个 skill（/code-review、/run、/simplify），只有 init 消息的 skills 列表认得出来
	Builtin bool `json:"builtin"`
}

// acpCommands：把 claude 报的命令转成 ACP 的。是 skill 的在 _meta 里标 skill: true —— 界面上 pin 住的命令点一下就发，
// skill 一般要跟一句话说做什么，只填进输入框（ui 的 Composer.pickPinned）。
// list 用这个会话自己进程报的（项目级的 skill 跟着目录走，各会话不一样），不用 a.commands。调用方持有 a.mu
func (a *Agent) acpCommands(list []cliCommand) []acp.AvailableCommand {
	var cmds []acp.AvailableCommand
	for _, c := range list {
		ac := acp.AvailableCommand{Name: c.Name, Description: c.Description}
		if c.ArgumentHint != "" {
			ac.Input = &acp.AvailableCommandInput{Unstructured: &acp.UnstructuredCommandInput{Hint: c.ArgumentHint}}
		}
		if !c.Builtin || a.skills[c.Name] {
			ac.Meta = map[string]any{"skill": true}
		}
		cmds = append(cmds, ac)
	}
	return cmds
}

func New(log *slog.Logger, opts Options) *Agent {
	if opts.IdleTimeout == 0 {
		opts.IdleTimeout = 15 * time.Minute
	}
	a := &Agent{log: log.With("agent", "claude"), opts: opts, sessions: map[string]*session{}, stopReaper: make(chan struct{})}
	go a.reaper()
	return a
}

func (a *Agent) SetConn(c *acp.AgentSideConnection) { a.conn = c }

func (a *Agent) Close() {
	select {
	case <-a.stopReaper:
	default:
		close(a.stopReaper)
	}
	a.mu.Lock()
	ss := make([]*session, 0, len(a.sessions))
	for _, s := range a.sessions {
		ss = append(ss, s)
	}
	a.mu.Unlock()
	var wg sync.WaitGroup
	for _, s := range ss {
		wg.Add(1)
		go func(s *session) { defer wg.Done(); s.kill() }(s)
	}
	wg.Wait()
}

// reaper 收掉闲着的进程。
func (a *Agent) reaper() {
	if a.opts.IdleTimeout < 0 {
		return
	}
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-a.stopReaper:
			return
		case <-t.C:
		}
		a.mu.Lock()
		ss := make([]*session, 0, len(a.sessions))
		for _, s := range a.sessions {
			ss = append(ss, s)
		}
		a.mu.Unlock()
		for _, s := range ss {
			s.mu.Lock()
			idle := s.p != nil && s.turn == nil && s.openInputs == 0 && time.Since(s.lastUsed) > a.opts.IdleTimeout
			s.mu.Unlock()
			if idle {
				a.log.Info("reap idle claude", "session", s.id)
				s.kill()
			}
		}
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

// own：只认本名的会话。/clear 之后旧 id 还作为别名指着同一个会话对象（adoptID），可「接上 / 导入旧 id」
// 要的是**那段旧对话**，不是它 —— 拿别名的话重放出来的历史全按新 id 发出去，灌进了清空后的那个会话（实测踩到：
// /resume 回清空之前的对话，新 pane 一片空白，旧历史跑到了别的 pane 里）。
func (a *Agent) own(id acp.SessionId) *session {
	a.mu.Lock()
	s := a.sessions[string(id)]
	a.mu.Unlock()
	if s == nil {
		return nil
	}
	s.mu.Lock()
	ok := s.id == string(id)
	s.mu.Unlock()
	if !ok {
		return nil
	}
	return s
}

// ---- ACP ----

func (a *Agent) Initialize(ctx context.Context, p acp.InitializeRequest) (acp.InitializeResponse, error) {
	return acp.InitializeResponse{
		ProtocolVersion: acp.ProtocolVersionNumber,
		AgentInfo:       &acp.Implementation{Name: "gerdr-claude", Title: acp.Ptr("Claude Code"), Version: "1"},
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

func metaStr(meta map[string]any, key string) string {
	if v, ok := acpx.GetMeta(meta, key); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func validMode(m string) bool {
	for _, x := range modes {
		if string(x.Id) == m {
			return true
		}
	}
	return false
}

func (a *Agent) newSession(id, cwd string, meta map[string]any) *session {
	s := &session{a: a, id: id, cwd: cwd, tools: map[string]*toolState{}, lastUsed: time.Now()}
	if m := metaStr(meta, "mode"); validMode(m) {
		s.mode = m
	}
	s.model = metaStr(meta, "model")
	s.effort = metaStr(meta, "effort")
	// 回退出来、还没说过话的会话（hub 替我们记着，见 acpx.RewindResponse.Fork）：pendingLocked 会核对它是不是已经分叉成了
	if f := acpx.GetPendingFork(meta); f != nil {
		s.fresh, s.forkFrom, s.forkAt = f.From == "", f.From, f.At
	}
	a.mu.Lock()
	a.sessions[id] = s
	a.mu.Unlock()
	return s
}

func (a *Agent) NewSession(ctx context.Context, p acp.NewSessionRequest) (acp.NewSessionResponse, error) {
	if _, err := a.bin(); err != nil {
		return acp.NewSessionResponse{}, err
	}
	s := a.newSession(uuid.NewString(), p.Cwd, p.Meta)
	s.fresh = true
	// 新会话马上起进程：拿到模型列表 / 斜杠命令 / 当前模式，人通常也马上就要说话了。
	if err := s.ensure(ctx); err != nil {
		a.mu.Lock()
		delete(a.sessions, s.id)
		a.mu.Unlock()
		return acp.NewSessionResponse{}, err
	}
	return acp.NewSessionResponse{SessionId: acp.SessionId(s.id), Modes: s.modeState(), ConfigOptions: a.configOptions(s)}, nil
}

// ResumeSession：接上一个已有的会话（gerdr 自己有记录，不用重放）。进程懒起。
func (a *Agent) ResumeSession(ctx context.Context, p acp.ResumeSessionRequest) (acp.ResumeSessionResponse, error) {
	s := a.own(p.SessionId)
	if s == nil {
		s = a.newSession(string(p.SessionId), p.Cwd, p.Meta)
	}
	if s.mode == "" {
		s.mode = a.defaultMode()
	}
	go s.syncTitle(500 * time.Millisecond) // 接上时就带上 claude 那边的标题，不用等聊一句
	return acp.ResumeSessionResponse{Modes: s.modeState(), ConfigOptions: a.configOptions(s)}, nil
}

// LoadSession：导入一个在别处开过的会话，从磁盘上的会话记录重放历史。
func (a *Agent) LoadSession(ctx context.Context, p acp.LoadSessionRequest) (acp.LoadSessionResponse, error) {
	s := a.own(p.SessionId)
	if s == nil && acpx.GetPendingFork(p.Meta) != nil {
		s = a.newSession(string(p.SessionId), p.Cwd, p.Meta)
	}
	emit := func(u acp.SessionUpdate) { s.update(context.Background(), u) }
	// 回退之后还没说过话：新会话的记录要等下一句话才由 claude 写出来，这时去找它的文件只会 transcript not found
	// （踩过：回退完点「重新载入」报错）。它的内容就是原会话到分叉点为止；回退到第一条的就是空的。
	if s != nil {
		s.mu.Lock()
		fresh, from, at := s.pendingLocked()
		cwd := s.cwd
		s.mu.Unlock()
		if fresh || from != "" {
			if from != "" {
				path, err := findTranscript(a.projectsDir(), from, cwd)
				if err != nil {
					return acp.LoadSessionResponse{}, err
				}
				if err := replayTranscriptUntil(path, at, emit); err != nil {
					return acp.LoadSessionResponse{}, err
				}
			}
			return acp.LoadSessionResponse{Modes: s.modeState(), ConfigOptions: a.configOptions(s)}, nil
		}
	}
	path, err := findTranscript(a.projectsDir(), string(p.SessionId), p.Cwd)
	if err != nil {
		return acp.LoadSessionResponse{}, err
	}
	if s == nil {
		s = a.newSession(string(p.SessionId), p.Cwd, p.Meta)
	}
	if err := replayTranscript(path, emit); err != nil {
		return acp.LoadSessionResponse{}, err
	}
	if s.mode == "" {
		s.mode = a.defaultMode()
	}
	return acp.LoadSessionResponse{Modes: s.modeState(), ConfigOptions: a.configOptions(s)}, nil
}

func (a *Agent) ListSessions(ctx context.Context, p acp.ListSessionsRequest) (acp.ListSessionsResponse, error) {
	cwd := ""
	if p.Cwd != nil {
		cwd = *p.Cwd
	}
	infos, err := listTranscripts(a.projectsDir(), cwd, 50)
	if err != nil {
		return acp.ListSessionsResponse{}, err
	}
	return acp.ListSessionsResponse{Sessions: infos}, nil
}

func (a *Agent) CloseSession(ctx context.Context, p acp.CloseSessionRequest) (acp.CloseSessionResponse, error) {
	a.mu.Lock()
	s := a.sessions[string(p.SessionId)]
	delete(a.sessions, string(p.SessionId))
	a.mu.Unlock()
	if s != nil {
		s.kill()
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
	s.mode = string(p.ModeId)
	pr := s.live()
	s.mu.Unlock()
	if pr != nil {
		if _, err := pr.control(ctx, map[string]any{"subtype": "set_permission_mode", "mode": string(p.ModeId)}); err != nil {
			return acp.SetSessionModeResponse{}, err
		}
	}
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
	val := string(p.ValueId.Value)
	switch string(p.ValueId.ConfigId) {
	case "model":
		s.mu.Lock()
		s.model = val
		pr := s.live()
		s.mu.Unlock()
		if pr != nil {
			m := any(val)
			if val == "default" {
				m = nil
			}
			if _, err := pr.control(ctx, map[string]any{"subtype": "set_model", "model": m}); err != nil {
				return acp.SetSessionConfigOptionResponse{}, err
			}
		}
	case "effort":
		// 思考强度只能在起进程时用 --effort 给：标记一下，下一轮开始前换一个进程（--resume，对话不丢）。
		s.mu.Lock()
		s.effort = val
		s.respawn = true
		s.mu.Unlock()
	default:
		return acp.SetSessionConfigOptionResponse{}, acp.NewInvalidParams(map[string]any{"configId": p.ValueId.ConfigId})
	}
	return acp.SetSessionConfigOptionResponse{ConfigOptions: a.configOptions(s)}, nil
}

func (a *Agent) defaultMode() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.defMode != "" {
		return a.defMode
	}
	return "default"
}

func (s *session) modeState() *acp.SessionModeState {
	s.mu.Lock()
	mode := s.mode
	s.mu.Unlock()
	if mode == "" {
		mode = s.a.defaultMode()
	}
	return &acp.SessionModeState{AvailableModes: modes, CurrentModeId: acp.SessionModeId(mode)}
}

func (a *Agent) configOptions(s *session) []acp.SessionConfigOption {
	a.mu.Lock()
	models := a.models
	a.mu.Unlock()
	if len(models) == 0 {
		return nil
	}
	s.mu.Lock()
	cur, effort := s.model, s.effort
	s.mu.Unlock()
	if cur == "" {
		cur = "default"
	}
	var opts acp.SessionConfigSelectOptionsUngrouped
	var curModel *cliModel
	// 「Default (recommended)」不说是哪个模型，人要的是「现在用的是 Opus 5.5」（用户点名的）。CLI 的说明里本来就写着
	// 「Opus 5.5 · Best for…」，拿点号前面那段当名字；和它解析到同一个模型的别名（opus）不再单列，不然列表里两个 Opus 5.5。
	defResolved := ""
	for _, m := range models {
		if m.Value == "default" {
			defResolved = m.ResolvedModel
		}
	}
	for i, m := range models {
		name, desc := m.DisplayName, m.Description
		if m.Value == "default" {
			if n, rest, ok := strings.Cut(desc, " · "); ok && n != "" {
				name, desc = n, "默认 · "+rest
			}
		} else if defResolved != "" && m.ResolvedModel == defResolved && cur != m.Value {
			continue
		}
		opts = append(opts, acp.SessionConfigSelectOption{Value: acp.SessionConfigValueId(m.Value), Name: name, Description: nilIfEmpty(desc)})
		if m.Value == cur {
			curModel = &models[i]
		}
	}
	if curModel == nil {
		// 用了别名以外的模型名（比如完整的模型 id）：也列进去，不然选择框显示空白。
		opts = append(opts, acp.SessionConfigSelectOption{Value: acp.SessionConfigValueId(cur), Name: cur})
	}
	out := []acp.SessionConfigOption{{Select: &acp.SessionConfigOptionSelect{
		Id: "model", Name: "Model", Type: "select", Category: acp.Ptr(acp.SessionConfigOptionCategoryModel),
		CurrentValue: acp.SessionConfigValueId(cur), Options: acp.SessionConfigSelectOptions{Ungrouped: &opts},
	}}}
	if curModel != nil && curModel.SupportsEffort && len(curModel.SupportedEffortLevels) > 0 {
		// 和模型下拉一样，第一项永远是「默认」（值为空 = 不传 --effort，CLI 按 settings 的 effortLevel 来），手选过也能退回去。
		// 名字直接写那一档（原来写的「auto」是我们编的，Claude Code 里没有这一档，用户问「auto 是什么逻辑」）；
		// 同一档的显式选项不再单列，不然列表里两个 medium。settings 里没写才叫「默认」。
		def := a.defaultEffort(s.cwd, curModel.ResolvedModel)
		if !slices.Contains(curModel.SupportedEffortLevels, def) {
			def = ""
		}
		var eo acp.SessionConfigSelectOptionsUngrouped
		if def != "" {
			eo = append(eo, acp.SessionConfigSelectOption{Value: "", Name: def, Description: acp.Ptr("默认 · 跟着 settings 里的 effortLevel")})
		} else {
			eo = append(eo, acp.SessionConfigSelectOption{Value: "", Name: "默认", Description: acp.Ptr("不指定，由 Claude Code 决定")})
		}
		for _, e := range curModel.SupportedEffortLevels {
			if e == def && effort != e {
				continue
			}
			eo = append(eo, acp.SessionConfigSelectOption{Value: acp.SessionConfigValueId(e), Name: e})
		}
		out = append(out, acp.SessionConfigOption{Select: &acp.SessionConfigOptionSelect{
			Id: "effort", Name: "Effort", Type: "select", Category: acp.Ptr(acp.SessionConfigOptionCategoryThoughtLevel),
			CurrentValue: acp.SessionConfigValueId(effort), Options: acp.SessionConfigSelectOptions{Ungrouped: &eo},
		}})
	}
	return out
}

func (a *Agent) Prompt(ctx context.Context, p acp.PromptRequest) (acp.PromptResponse, error) {
	s, err := a.session(p.SessionId)
	if err != nil {
		return acp.PromptResponse{}, err
	}
	msgID := ""
	if p.MessageId != nil {
		msgID = *p.MessageId
	}
	return s.prompt(ctx, p.Prompt, msgID)
}

func (a *Agent) Cancel(ctx context.Context, p acp.CancelNotification) error {
	s, err := a.session(p.SessionId)
	if err != nil {
		return nil
	}
	s.cancel()
	return nil
}

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
	case acpx.MethodSteer:
		var p acpx.SteerRequest
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, acp.NewInvalidParams(err.Error())
		}
		s, err := a.session(p.SessionId)
		if err != nil {
			return nil, err
		}
		return s.steer(p.Prompt, p.MessageId, p.Now)
	case acpx.MethodRewind:
		var p acpx.RewindRequest
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, acp.NewInvalidParams(err.Error())
		}
		return a.rewind(ctx, p)
	case acpx.MethodWarm:
		var p struct {
			SessionId acp.SessionId `json:"sessionId"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, acp.NewInvalidParams(err.Error())
		}
		s, err := a.session(p.SessionId)
		if err != nil {
			return nil, err
		}
		return map[string]any{}, s.ensure(ctx)
	case acpx.MethodWithdraw:
		var p acpx.WithdrawRequest
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, acp.NewInvalidParams(err.Error())
		}
		s, err := a.session(p.SessionId)
		if err != nil {
			return nil, err
		}
		return s.withdraw(ctx, p.MessageIds), nil
	case acpx.MethodTaskStop:
		var p acpx.TaskStopRequest
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, acp.NewInvalidParams(err.Error())
		}
		s, err := a.session(p.SessionId)
		if err != nil {
			return nil, err
		}
		return map[string]any{}, s.stopTask(ctx, p.TaskId)
	case acpx.MethodTaskBackground:
		var p acpx.TaskBackgroundRequest
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, acp.NewInvalidParams(err.Error())
		}
		s, err := a.session(p.SessionId)
		if err != nil {
			return nil, err
		}
		ok, err := s.backgroundTool(ctx, p.ToolCallId)
		return acpx.TaskBackgroundResponse{Backgrounded: ok}, err
	case acpx.MethodTranscriptStat:
		var p acpx.TranscriptStatRequest
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, acp.NewInvalidParams(err.Error())
		}
		path, err := findTranscript(a.projectsDir(), string(p.SessionId), p.Cwd)
		if err != nil {
			return nil, err
		}
		st, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		res := acpx.TranscriptStatResponse{Size: st.Size(), ModTime: st.ModTime().UnixMilli()}
		if p.Since >= 0 {
			res.NewUserTexts = userTextsFrom(path, p.Since)
		}
		return res, nil
	case acpx.MethodHolders:
		var p acpx.HoldersRequest
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, acp.NewInvalidParams(err.Error())
		}
		return acpx.HoldersResponse{Holders: a.holders(string(p.SessionId))}, nil
	case acpx.MethodTakeover:
		var p acpx.TakeoverRequest
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, acp.NewInvalidParams(err.Error())
		}
		s, err := a.session(p.SessionId)
		if err != nil {
			return nil, err
		}
		return s.takeover(p.Action)
	case acpx.MethodAllowRules:
		var p acpx.AllowRulesRequest
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, acp.NewInvalidParams(err.Error())
		}
		s, err := a.session(p.SessionId)
		if err != nil {
			return nil, err
		}
		s.mu.Lock()
		cwd := s.cwd
		s.mu.Unlock()
		return map[string]any{}, addAllowRules(cwd, p.Rules)
	}
	return nil, acp.NewMethodNotFound(method)
}

// rewind：在会话记录里找到那条用户消息的 parentUuid（它前面那一条），从那儿分叉出一个
// 新会话（--resume 旧 --resume-session-at parent --fork-session --session-id 新）。
// 原会话不动 —— 回退本身也是可以反悔的。那条消息是第一条的话，就是一个全新的会话。
func (a *Agent) rewind(ctx context.Context, p acpx.RewindRequest) (acpx.RewindResponse, error) {
	s, err := a.session(p.SessionId)
	if err != nil {
		return acpx.RewindResponse{}, err
	}
	s.mu.Lock()
	if s.turn != nil {
		s.mu.Unlock()
		return acpx.RewindResponse{}, acp.NewInvalidRequest("cannot rewind while a turn is running")
	}
	// 还没真正分叉过的会话（上次回退之后还没说过话）：要在原会话的记录里找。
	src := s.id
	if _, from, _ := s.pendingLocked(); from != "" {
		src = from
	}
	cwd := s.cwd
	s.mu.Unlock()

	path, err := findTranscript(a.projectsDir(), src, cwd)
	if err != nil {
		return acpx.RewindResponse{}, err
	}
	parent, found, err := parentOf(path, p.MessageId)
	if err != nil {
		return acpx.RewindResponse{}, err
	}
	if !found {
		return acpx.RewindResponse{}, acp.NewInvalidParams(map[string]any{"messageId": p.MessageId, "error": "message not found in transcript"})
	}
	newID := uuid.NewString()
	s.mu.Lock()
	// 旧进程不在这里等它退：claude 关了 stdin 也要 1.5 秒左右才退完（实测，闲着的也一样），
	// Esc 撤回刚发的话走的就是回退，同步等的话人按完 Esc 要干等两三秒（用户报的「很卡」）。
	// 让它在后台退（exiting）：ensure 起新进程前会先 stop 旧的、等它退完，分叉时读到的记录是写完的。
	if s.p != nil {
		s.exiting = s.p
		go s.p.stop()
	}
	if parent == "" {
		s.fresh, s.forkFrom, s.forkAt = true, "", ""
	} else {
		s.fresh, s.forkFrom, s.forkAt = false, src, parent
	}
	fork := &acpx.PendingFork{From: s.forkFrom, At: s.forkAt}
	old := s.id
	s.id = newID
	s.mu.Unlock()
	a.mu.Lock()
	delete(a.sessions, old)
	a.sessions[newID] = s
	a.mu.Unlock()
	return acpx.RewindResponse{SessionId: acp.SessionId(newID), Fork: fork}, nil
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

var errNoTurn = errors.New("no turn")

var _ acp.Agent = (*Agent)(nil)
var _ acp.AgentLoader = (*Agent)(nil)
