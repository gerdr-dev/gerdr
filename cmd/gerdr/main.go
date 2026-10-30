package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/coder/acp-go-sdk"
	"github.com/spf13/cobra"

	"github.com/gerdr-dev/gerdr/internal/agents/claude"
	"github.com/gerdr-dev/gerdr/internal/agents/codex"
	"github.com/gerdr-dev/gerdr/internal/agents/native"
	"github.com/gerdr-dev/gerdr/internal/awake"
	"github.com/gerdr-dev/gerdr/internal/config"
	"github.com/gerdr-dev/gerdr/internal/ext"
	"github.com/gerdr-dev/gerdr/internal/hub"
	"github.com/gerdr-dev/gerdr/internal/plugins"
	"github.com/gerdr-dev/gerdr/internal/remote"
	"github.com/gerdr-dev/gerdr/internal/server"
	"github.com/gerdr-dev/gerdr/internal/sysenv"
	"github.com/gerdr-dev/gerdr/internal/term"
	"github.com/gerdr-dev/gerdr/internal/uisite"
	"github.com/gerdr-dev/gerdr/internal/version"
	"github.com/gerdr-dev/gerdr/internal/webui"
)

func main() {
	root := &cobra.Command{
		Use:           "gerdr",
		Short:         "Claude Code 和 Codex 的图形界面（ACP），可以中途插话、回退、审批",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	var open, appMode, serviceMode bool
	var parentPID int
	serve := func(cmd *cobra.Command, args []string) error {
		return runServe(open, appMode, serviceMode, parentPID)
	}
	root.RunE = serve
	root.Flags().BoolVar(&open, "open", false, "启动后用浏览器打开")
	root.Flags().BoolVar(&appMode, "app", false, "给 mac App 用：就绪后打印一行 GERDR_READY <url>")
	root.Flags().IntVar(&parentPID, "parent-pid", 0, "父进程退出时跟着退出")
	root.Flags().BoolVar(&serviceMode, "service", false, "后台服务（launchd）用：数据目录被别的 gerdr 占着就等它退出再接手")
	_ = root.Flags().MarkHidden("app")
	_ = root.Flags().MarkHidden("service")
	_ = root.Flags().MarkHidden("parent-pid")

	root.AddCommand(&cobra.Command{
		Use:   "url",
		Short: "打印正在运行的 gerdr 的登录链接",
		RunE: func(cmd *cobra.Command, args []string) error {
			env := config.FromEnv()
			u, ok := runningURL(env)
			if !ok {
				return fmt.Errorf("gerdr 没在运行")
			}
			fmt.Println(u)
			return nil
		},
	})
	root.AddCommand(&cobra.Command{
		Use:   "acp <claude|codex|native>",
		Short: "把内置的适配器当成一个 stdio ACP agent 跑（给 Zed 之类的 ACP 客户端用）",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runACP(args[0])
		},
	})
	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "打印版本",
		Run:   func(cmd *cobra.Command, args []string) { fmt.Println(version.Version) },
	})
	root.AddCommand(sessionCmd(), uiCmd(), remoteCmd(), workspaceCmd(), tabCmd(), paneCmd())
	if err := root.Execute(); err != nil {
		if errors.Is(err, errHandover) {
			os.Exit(exitHandover)
		}
		if len(os.Args) > 1 && slices.Contains([]string{"session", "ui", "workspace", "tab", "pane"}, os.Args[1]) {
			fail(err) // for agents: errors are JSON too
		}
		fmt.Fprintln(os.Stderr, "gerdr:", err)
		os.Exit(1)
	}
}

type serverInfo struct {
	PID  int    `json:"pid"`
	Port int    `json:"port"`
	Host string `json:"host"`
	// Version / Service：App 启动时看在跑的那个是不是后台服务、是不是旧版（appAttach）
	Version string `json:"version,omitempty"`
	Service bool   `json:"service,omitempty"`
	// Build：二进制文件本身（修改时间 + 大小）。本地打包的版本号都是 0.1.0-dev，只比版本号的话 App 更新了也认不出来
	Build string `json:"build,omitempty"`
}

// buildID：本进程二进制的修改时间 + 大小。App 更新 = 同一个路径换了文件，这两个就变了。
func buildID() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	st, err := os.Stat(exe)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%d-%d", st.ModTime().UnixNano(), st.Size())
}

func infoPath(env config.Env) string { return filepath.Join(env.Dir, "server.json") }

func loginURL(host string, port int, token string) string {
	if host == "0.0.0.0" || host == "::" || host == "" {
		host = "127.0.0.1"
	}
	return fmt.Sprintf("http://%s/auth?token=%s", net.JoinHostPort(host, strconv.Itoa(port)), token)
}

// runningURL：已经有一个 gerdr 在跑（同一个数据目录）就返回它的登录链接。
func runningURL(env config.Env) (string, bool) {
	_, u, ok := running(env)
	return u, ok
}

func running(env config.Env) (serverInfo, string, bool) {
	var si serverInfo
	b, err := os.ReadFile(infoPath(env))
	if err != nil {
		return si, "", false
	}
	if json.Unmarshal(b, &si) != nil || si.Port == 0 {
		return si, "", false
	}
	if p, err := os.FindProcess(si.PID); err != nil || p.Signal(syscall.Signal(0)) != nil {
		return si, "", false
	}
	host := si.Host
	if host == "" || host == "0.0.0.0" {
		host = "127.0.0.1"
	}
	c := http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get(fmt.Sprintf("http://%s/api/hello", net.JoinHostPort(host, strconv.Itoa(si.Port))))
	if err != nil {
		return si, "", false
	}
	resp.Body.Close()
	tok := env.Token
	if tok == "" {
		tok, _ = config.LoadToken(env.Dir)
	}
	return si, loginURL(si.Host, si.Port, tok), true
}

func runServe(open, appMode, serviceMode bool, parentPID int) error {
	env := config.FromEnv()
	level := slog.LevelInfo
	if os.Getenv("GERDR_DEBUG") != "" {
		level = slog.LevelDebug
	}
	if err := os.MkdirAll(env.Dir, 0o700); err != nil {
		return err
	}
	logOut := os.Stderr
	log := slog.New(slog.NewTextHandler(logOut, &slog.HandlerOptions{Level: level}))
	if err := sysenv.AdoptLoginPath(); err != nil {
		// Agents would start with a bare PATH (codex / node / go not found); it retries on the next spawn.
		log.Warn("login shell PATH not read, will retry", "err", err)
	}
	sysenv.SetGerdrContext(env.Dir)

	// 一个数据目录只能有一个 gerdr（两份进程同时写 sessions/ 会互相覆盖）。
	svc := newService(env, serviceMode)
	var lock *os.File
	err := errBusy
	if !appMode || !svc.Installed() || svc.Why() != "" {
		lock, err = lockFile(lockPath(env))
	}
	switch {
	case err == nil:
	case appMode:
		// 开了后台常驻（始终由它跑），或者终端里有一个在跑：连过去（见 appAttach）
		if lock = appAttach(env, parentPID, svc); lock == nil {
			return nil
		}
	case serviceMode:
		// 多半是 App 自己起的那个后端还在：等它退出（⌘Q）再接手。不能退出 —— KeepAlive 会每 10 秒拉一次
		log.Info("another gerdr is using the data dir, waiting to take over", "dir", env.Dir)
		if lock, err = lockFileWait(lockPath(env)); err != nil {
			return err
		}
	default:
		if u, ok := runningURL(env); ok {
			fmt.Println("gerdr 已经在运行：" + u)
			if open {
				openBrowser(u)
			}
			return nil
		}
		return fmt.Errorf("另一个 gerdr 正在用 %s：%w", env.Dir, err)
	}
	defer lock.Close()

	token := env.Token
	if token == "" {
		if token, err = config.LoadToken(env.Dir); err != nil {
			return err
		}
	}
	cfg := config.NewStore(filepath.Join(env.Dir, "config.json"), `{}`)
	layout := config.NewStore(filepath.Join(env.Dir, "layout.json"), `null`)

	// config.json 的 agents：变体（另一个账号的 claude……）和外部 ACP agent。设置里改了 / 手改了文件都热换。
	cfgAgents := func() []hub.AgentDef {
		var c struct {
			Agents []hub.AgentDef `json:"agents"`
		}
		_ = json.Unmarshal(cfg.Get(), &c)
		return c.Agents
	}
	em := ext.New(env.Dir)
	if added, err := em.MigrateNativeMCP(); err != nil {
		log.Warn("migrate native mcp", "err", err)
	} else if len(added) > 0 {
		log.Info("Native 引擎借用的 Claude Code MCP 已导入到 Gerdr 的 MCP", "servers", added)
	}
	// config.json 的 llm：native agent 的模型（每轮现取，改了下一轮就生效）
	cfgLLM := func() native.Config {
		var c struct {
			LLM native.Config `json:"llm"`
		}
		_ = json.Unmarshal(cfg.Get(), &c)
		return c.LLM
	}
	// config.json 的 defaultModes / defaultConfig：新会话的默认模式 / 模型（按 agent id，设置 → Agent 里改），建会话时现取
	cfgDefaults := func(agent string) hub.NewDefaults {
		var c struct {
			Modes  map[string]string            `json:"defaultModes"`
			Config map[string]map[string]string `json:"defaultConfig"`
		}
		_ = json.Unmarshal(cfg.Get(), &c)
		return hub.NewDefaults{Mode: c.Modes[agent], Config: c.Config[agent]}
	}
	h, err := hub.New(hub.Options{Dir: env.Dir, ClaudeIdle: env.ClaudeIdle, ExtraAgents: cfgAgents(), LLM: cfgLLM, Defaults: cfgDefaults, Log: log, Ext: em})
	if err != nil {
		return err
	}
	pm := plugins.New(filepath.Join(env.Dir, "plugins"), log)
	if err := pm.Install(); err != nil {
		log.Warn("install builtin plugins", "err", err)
	}
	pm.Watch(func() { h.Broadcast("plugins", nil) })
	// 有远程连着时不让电脑睡：界面配置里的实验室开关（keepAwakeRemote），和 agents 一样存了就生效。
	// 开着就连合盖也不睡（原来是两个开关，合成一个：要远程用电脑，合盖睡着了一样连不上）；手动时段的合盖也跟着它。
	keeper := awake.New(log, env.Dir)
	defer keeper.Close()
	applyAwake := func() {
		var c struct {
			KeepAwakeRemote bool `json:"keepAwakeRemote"`
		}
		_ = json.Unmarshal(cfg.Get(), &c)
		keeper.SetEnabled(c.KeepAwakeRemote)
		keeper.SetLid(c.KeepAwakeRemote)
	}
	applyAwake()
	applyAgents := func() {
		h.SetAgents(cfgAgents())
		h.Broadcast("agents", h.Agents())
		applyAwake()
	}
	cfg.Watch(func() {
		h.Broadcast("config", map[string]any{"data": cfg.Get()})
		applyAgents()
	})

	// 界面：源码目录，没有打包。默认目录第一次启动从内嵌的那份装出来（webui 包注释讲了升级怎么不覆盖人家的改动）。
	if env.UI == filepath.Join(env.Dir, "ui") {
		if msg, err := webui.Install(env.UI); err != nil {
			log.Warn("install ui", "err", err)
		} else if msg != "" {
			log.Info(msg)
		}
	}
	if _, err := os.Stat(filepath.Join(env.UI, "package.json")); err != nil {
		return fmt.Errorf("界面目录 %s 不对（没有 package.json）：用 GERDR_UI 指到界面源码目录", env.UI)
	}
	ui := uisite.New(env.UI, filepath.Join(env.Dir, "cache", "ui"), log)
	stopUI := make(chan struct{})
	defer close(stopUI)
	go ui.Watch(stopUI)
	// 远程访问：设备表 / passkey 总是打开（设置里要能看、能踢）；配了中继才真的连出去。
	// 起不来（证书签不下来、没 join 过）不拦启动，原因在设置 → 远程访问里看。
	rcfg, err := config.LoadRemote(env.Dir)
	if err != nil {
		return fmt.Errorf("读远程访问配置（%s）失败：%w", config.RemoteDir(env.Dir), err)
	}
	rm, err := remote.Open(env.Dir, rcfg, log)
	if err != nil {
		return err
	}
	defer rm.Close()
	srv := &server.Server{Dir: env.Dir, Hub: h, Token: token, Config: cfg, Layout: layout, Plugins: pm, Ext: em, Log: log, OnConfig: applyAgents,
		UI: ui, ESM: uisite.NewESM(env.ESMCDN, filepath.Join(env.Dir, "cache", "esm")), Remote: rm, Awake: keeper,
		Service: svc, Terms: term.New(func() []string { return sysenv.ChildEnv() }, log)}
	defer srv.Terms.Close()
	// 打开后台常驻时，App 起的这个后端让位：退出（errHandover → 退出码 75），后台服务拿到锁接手，
	// App 看到 75 就重起一个后端，它只连后台、不自己干活（desktop/main.js）。
	handover := make(chan struct{})
	if appMode {
		var once sync.Once
		srv.OnServiceInstalled = func() { once.Do(func() { close(handover) }) }
	}
	ln, err := server.Listen(env.Host, env.Port)
	if err != nil && env.Port != 0 {
		// 默认口被占了（比如上一个实例还没退干净）：换一个空闲的，别起不来。
		log.Warn("port busy, picking another", "port", env.Port, "err", err)
		ln, err = server.Listen(env.Host, 0)
	}
	if err != nil {
		return err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	si, _ := json.Marshal(serverInfo{PID: os.Getpid(), Port: port, Host: env.Host, Version: version.Version, Service: serviceMode, Build: buildID()})
	_ = os.WriteFile(infoPath(env), si, 0o600)
	defer os.Remove(infoPath(env))

	// 所有请求的 context 都挂在 base 上，关机时先取消它：界面热更新的 SSE（/_ui/events）是条一直开着的普通请求，
	// Shutdown 会一直等它结束 —— 等满超时才退，mac App 上表现为 ⌘Q 卡 4 秒再被强杀（实测 5.0s → 修完 0.5s）。
	base, cancelBase := context.WithCancel(context.Background())
	handler := srv.Handler()
	hs := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second, BaseContext: func(net.Listener) context.Context { return base }}
	go func() {
		if err := hs.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Error("serve", "err", err)
		}
	}()
	// 远程那条路和本机口是同一个 handler，只是先盖一个「从远程进来的」章（规则完全不同，见 server.remoteGuard）
	rm.Start(server.RemoteHandler(handler))
	u := loginURL(env.Host, port, token)
	if appMode {
		fmt.Println("GERDR_READY " + u)
	} else {
		fmt.Printf("gerdr %s\n  打开：%s\n  数据：%s\n", version.Version, u, env.Dir)
		if r := rm.URL(); r != "" {
			fmt.Printf("  远程：%s（新设备：gerdr remote pair）\n", r)
		}
	}
	if open {
		openBrowser(u)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	if parentPID > 0 {
		go func() { waitParent(parentPID); stop() }()
	}
	handed := false
	select {
	case <-ctx.Done():
	case <-handover:
		handed = true
		log.Info("background service installed, handing over")
	}
	log.Info("shutting down")
	cancelBase()
	sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = hs.Shutdown(sctx)
	h.Close()
	if handed {
		return errHandover
	}
	return nil
}

var (
	errBusy     = errors.New("数据目录被另一个 gerdr 占着")
	errHandover = errors.New("交给后台服务了")
)

// exitHandover：App 看到这个退出码就重起一个后端（它会去连后台服务），不当成出错。和 desktop/main.js 一致。
const exitHandover = 75

func openBrowser(u string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", u)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	default:
		cmd = exec.Command("xdg-open", u)
	}
	_ = cmd.Start()
}

// runACP：stdio ACP agent 模式。日志只能写 stderr（stdout 是协议）。
func runACP(which string) error {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	sysenv.AdoptLoginPath()
	type agent interface {
		acp.Agent
		SetConn(*acp.AgentSideConnection)
		Close()
	}
	var a agent
	switch which {
	case "claude":
		a = claude.New(log, claude.Options{})
	case "codex":
		a = codex.New(log, nil)
	case "native":
		// 独立跑时模型配置从 $GERDR_DIR/config.json（默认 ~/.gerdr）的 llm 读，会话记录放在同一个目录下
		dir := os.Getenv("GERDR_DIR")
		if dir == "" {
			home, _ := os.UserHomeDir()
			dir = filepath.Join(home, ".gerdr")
		}
		a = native.New(log, native.Options{Dir: filepath.Join(dir, "native"), Config: func() native.Config {
			var c struct {
				LLM native.Config `json:"llm"`
			}
			b, _ := os.ReadFile(filepath.Join(dir, "config.json"))
			_ = json.Unmarshal(b, &c)
			return c.LLM
		}})
	default:
		return fmt.Errorf("unknown agent %q (claude|codex|native)", which)
	}
	conn := acp.NewAgentSideConnection(a, os.Stdout, os.Stdin)
	conn.SetLogger(log)
	a.SetConn(conn)
	<-conn.Done()
	a.Close()
	return nil
}
