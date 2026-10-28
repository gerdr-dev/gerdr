package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"github.com/coder/acp-go-sdk"
	"github.com/spf13/cobra"

	"github.com/zbysir/gerdr/internal/agents/claude"
	"github.com/zbysir/gerdr/internal/agents/codex"
	"github.com/zbysir/gerdr/internal/config"
	"github.com/zbysir/gerdr/internal/ext"
	"github.com/zbysir/gerdr/internal/hub"
	"github.com/zbysir/gerdr/internal/plugins"
	"github.com/zbysir/gerdr/internal/remote"
	"github.com/zbysir/gerdr/internal/server"
	"github.com/zbysir/gerdr/internal/sysenv"
	"github.com/zbysir/gerdr/internal/uisite"
	"github.com/zbysir/gerdr/internal/version"
	"github.com/zbysir/gerdr/internal/webui"
)

func main() {
	root := &cobra.Command{
		Use:           "gerdr",
		Short:         "Claude Code 和 Codex 的图形界面（ACP），可以中途插话、回退、审批",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	var open, appMode bool
	var parentPID int
	serve := func(cmd *cobra.Command, args []string) error {
		return runServe(open, appMode, parentPID)
	}
	root.RunE = serve
	root.Flags().BoolVar(&open, "open", false, "启动后用浏览器打开")
	root.Flags().BoolVar(&appMode, "app", false, "给 mac App 用：就绪后打印一行 GERDR_READY <url>")
	root.Flags().IntVar(&parentPID, "parent-pid", 0, "父进程退出时跟着退出")
	_ = root.Flags().MarkHidden("app")
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
		Use:   "acp <claude|codex>",
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
	root.AddCommand(sessionCmd(), uiCmd(), remoteCmd())
	if err := root.Execute(); err != nil {
		if len(os.Args) > 1 && (os.Args[1] == "session" || os.Args[1] == "ui") {
			fail(err) // 给 agent 用的：错误也是 JSON
		}
		fmt.Fprintln(os.Stderr, "gerdr:", err)
		os.Exit(1)
	}
}

type serverInfo struct {
	PID  int    `json:"pid"`
	Port int    `json:"port"`
	Host string `json:"host"`
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
	b, err := os.ReadFile(infoPath(env))
	if err != nil {
		return "", false
	}
	var si serverInfo
	if json.Unmarshal(b, &si) != nil || si.Port == 0 {
		return "", false
	}
	if p, err := os.FindProcess(si.PID); err != nil || p.Signal(syscall.Signal(0)) != nil {
		return "", false
	}
	host := si.Host
	if host == "" || host == "0.0.0.0" {
		host = "127.0.0.1"
	}
	c := http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get(fmt.Sprintf("http://%s/api/hello", net.JoinHostPort(host, strconv.Itoa(si.Port))))
	if err != nil {
		return "", false
	}
	resp.Body.Close()
	tok := env.Token
	if tok == "" {
		tok, _ = config.LoadToken(env.Dir)
	}
	return loginURL(si.Host, si.Port, tok), true
}

func runServe(open, appMode bool, parentPID int) error {
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
	sysenv.AdoptLoginPath()
	sysenv.SetGerdrContext(env.Dir)

	// 一个数据目录只能有一个 gerdr（两份进程同时写 sessions/ 会互相覆盖）。
	lock, err := lockFile(filepath.Join(env.Dir, "server.lock"))
	if err != nil {
		if u, ok := runningURL(env); ok {
			if appMode {
				fmt.Println("GERDR_READY " + u)
				if parentPID > 0 {
					waitParent(parentPID)
				}
				return nil
			}
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
	h, err := hub.New(hub.Options{Dir: env.Dir, ClaudeIdle: env.ClaudeIdle, ExtraAgents: cfgAgents(), NoSnapshots: env.NoSnapshots, Log: log, Ext: em})
	if err != nil {
		return err
	}
	pm := plugins.New(filepath.Join(env.Dir, "plugins"), log)
	if err := pm.Install(); err != nil {
		log.Warn("install builtin plugins", "err", err)
	}
	pm.Watch(func() { h.Broadcast("plugins", nil) })
	applyAgents := func() {
		h.SetAgents(cfgAgents())
		h.Broadcast("agents", h.Agents())
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
	srv := &server.Server{Hub: h, Token: token, Config: cfg, Layout: layout, Plugins: pm, Ext: em, Log: log, OnConfig: applyAgents,
		UI: ui, ESM: uisite.NewESM(env.ESMCDN, filepath.Join(env.Dir, "cache", "esm")), Remote: rm}
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
	si, _ := json.Marshal(serverInfo{PID: os.Getpid(), Port: port, Host: env.Host})
	_ = os.WriteFile(infoPath(env), si, 0o600)
	defer os.Remove(infoPath(env))

	handler := srv.Handler()
	hs := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
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
	<-ctx.Done()
	log.Info("shutting down")
	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = hs.Shutdown(sctx)
	h.Close()
	return nil
}

func waitParent(pid int) {
	for {
		time.Sleep(time.Second)
		if os.Getppid() != pid {
			return
		}
	}
}

func openBrowser(u string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", u)
	default:
		cmd = exec.Command("xdg-open", u)
	}
	_ = cmd.Start()
}

func lockFile(p string) (*os.File, error) {
	f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
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
	default:
		return fmt.Errorf("unknown agent %q (claude|codex)", which)
	}
	conn := acp.NewAgentSideConnection(a, os.Stdout, os.Stdin)
	conn.SetLogger(log)
	a.SetConn(conn)
	<-conn.Done()
	a.Close()
	return nil
}
