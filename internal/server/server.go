// Package server：HTTP API + WebSocket + 内嵌的前端。
//
// 这个服务能让人（经由 agent）在这台机器上跑任意命令，所以**永远要登录令牌**，哪怕只听
// 127.0.0.1 —— 本机端口不一定只有本机连得到（隧道、端口转发），从进程里看不出来。
// 远程（云端中继）进来的请求换一套凭据（设备 cookie + passkey，不认令牌），见 remoteapi.go 和 docs/dev/REMOTE.md。
package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/coder/acp-go-sdk"
	"github.com/gorilla/websocket"

	"github.com/gerdr-dev/gerdr/internal/auth"
	"github.com/gerdr-dev/gerdr/internal/awake"
	"github.com/gerdr-dev/gerdr/internal/config"
	"github.com/gerdr-dev/gerdr/internal/doctor"
	"github.com/gerdr-dev/gerdr/internal/ext"
	"github.com/gerdr-dev/gerdr/internal/hub"
	"github.com/gerdr-dev/gerdr/internal/plugins"
	"github.com/gerdr-dev/gerdr/internal/remote"
	"github.com/gerdr-dev/gerdr/internal/service"
	"github.com/gerdr-dev/gerdr/internal/term"
	"github.com/gerdr-dev/gerdr/internal/uisite"
	"github.com/gerdr-dev/gerdr/internal/version"
	"github.com/gerdr-dev/gerdr/internal/webui"
	"github.com/gerdr-dev/gerdr/internal/work"
)

type Server struct {
	Dir     string // 数据目录（~/.gerdr）
	Hub     *hub.Hub
	Token   string
	Config  *config.Store
	Secrets *config.Secrets // API keys moved out of config.json (config/secrets.go)
	Layout  *config.Store
	Plugins *plugins.Manager
	// PluginSvc runs the plugins' services (plugin.json); RunPluginServices starts it.
	PluginSvc *plugins.Services
	Ext       *ext.Manager
	// Work：the Work view's local functions (internal/work). nil = off (tests).
	Work *work.Runner
	UI   *uisite.Renderer
	ESM  *uisite.ESM
	Log  *slog.Logger
	// OnConfig：界面上存了 config.json 之后（agents 那一节要热换）
	OnConfig func()
	// Remote：远程访问（设备表、passkey、中继）。nil = 这个进程不管远程（测试里）。
	Remote *remote.Service
	// Service：后台常驻（launchd）。nil = 不管（测试里）。
	Service *service.Manager
	// Terms：界面里的终端。nil = 不开（测试里）。
	Terms *term.Manager
	// Awake：有远程设备连着时不让电脑睡（设置 → 实验室）。nil = 不管（测试里）。
	Awake *awake.Keeper
	// Doctor: Settings → Labs → Diagnosis (doctorapi.go). nil = off (tests).
	Doctor      *doctor.Doctor
	remoteConns remoteConns // 正在连着的远程界面（remoteconns.go）
	appWatch    appWatch    // 安卓 App 后台通知的长轮询（appwatch.go）
	workUIs     workUIs     // each project's Work site, started on first use (workapi.go)
	links       fileLinks   // cookie-less links to one file, for opening it in the system browser (filelink.go)
	appUpdate   appUpdate   // the desktop App's update status, relayed by its page for other devices (appupdate.go)
	// OnServiceInstalled：刚打开后台常驻、本进程是 App 起的后端时让位（见 cmd/gerdr 的 handover）。
	OnServiceInstalled func()
}

// cookieName：按令牌区分。cookie 不分端口 —— 同一个浏览器里开着两个 gerdr（正式的 + 调试的，都在 127.0.0.1），
// 名字一样的话后登录的那个把前一个的 cookie 盖掉，前一个就掉线了（踩过）。
func (s *Server) cookieName() string {
	h := sha256.Sum256([]byte(s.Token))
	return "gerdr_" + hex.EncodeToString(h[:4])
}

func (s *Server) Handler() http.Handler {
	if s.Work != nil {
		s.Work.Host = workHost{s} // ctx.sessions / ctx.workspaces in local functions (workhost.go)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /auth", s.auth)
	mux.HandleFunc("GET /api/hello", s.hello)
	mux.HandleFunc("GET /api/plugins", s.plugins)
	mux.HandleFunc("POST /api/plugins/{id}/message", s.pluginMessage)
	mux.HandleFunc("GET /api/config", func(w http.ResponseWriter, r *http.Request) { writeRaw(w, s.Config.Get()) })
	mux.HandleFunc("PUT /api/config", s.putStore(s.Config, "config"))
	mux.HandleFunc("GET /api/layout", func(w http.ResponseWriter, r *http.Request) { writeRaw(w, s.Layout.Get()) })
	// awake：不让电脑睡（设置 → 实验室）。远程也能调：手机上要能手动续一段，这个口最坏也就是电脑不睡。
	mux.HandleFunc("GET /api/awake", s.awakeStatus)
	mux.HandleFunc("PUT /api/awake", s.setAwake)
	// The inbox's low-battery alert polls this once a minute; /api/awake would also run sudo -l each time.
	mux.HandleFunc("GET /api/battery", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, awake.ReadBattery()) })
	// 合盖不睡要一次管理员授权（弹 macOS 的密码框）：只在本机口（remoteGuard 挡掉远程）—— 远程点了，框弹在没人的电脑上
	mux.HandleFunc("POST /api/awake/lid-setup", func(w http.ResponseWriter, r *http.Request) {
		if s.Awake == nil {
			httpError(w, 501, "这个进程不管休眠")
			return
		}
		if err := awake.SetupLid(); err != nil {
			httpError(w, 400, err.Error())
			return
		}
		s.Awake.Retry()
		writeJSON(w, s.Awake.Status())
	})
	mux.HandleFunc("PUT /api/layout", s.putStore(s.Layout, "layout"))
	mux.HandleFunc("POST /api/layout/op", s.layoutOp) // gerdr workspace / tab / pane (layoutapi.go)
	mux.HandleFunc("POST /api/sessions", s.createSession)
	mux.HandleFunc("DELETE /api/sessions/{id}", s.sessionOp(func(r *http.Request, sess *hub.Session) (any, error) {
		return nil, s.Hub.Delete(r.Context(), sess.ID())
	}))
	mux.HandleFunc("POST /api/sessions/{id}/send", s.sessionOp(s.send))
	// 输入框里的 `!命令`：gerdr 在会话目录里直接跑，不经过 agent（hub/shell.go）
	mux.HandleFunc("POST /api/sessions/{id}/shell", s.sessionOp(func(r *http.Request, sess *hub.Session) (any, error) {
		var b struct {
			Command string `json:"command"`
			ID      string `json:"id"` // client 生成的 UUID：重发同一条时不跑两遍
		}
		if err := decode(r, &b); err != nil {
			return nil, err
		}
		id, err := sess.Shell(b.Command, b.ID)
		return map[string]any{"id": id}, err
	}))
	mux.HandleFunc("POST /api/sessions/{id}/shell/{run}/stop", s.sessionOp(func(r *http.Request, sess *hub.Session) (any, error) {
		return map[string]any{}, sess.StopShell(r.PathValue("run"))
	}))
	mux.HandleFunc("POST /api/sessions/{id}/cancel", s.sessionOp(func(r *http.Request, sess *hub.Session) (any, error) {
		return nil, sess.Cancel(r.Context())
	}))
	// 全部会话，最近有动静的在前（给外面的程序；界面用的是 /api/hello 那一大包）
	mux.HandleFunc("GET /api/sessions", func(w http.ResponseWriter, r *http.Request) {
		list := s.Hub.List()
		sort.SliceStable(list, func(i, j int) bool { return list[i].UpdatedAt > list[j].UpdatedAt })
		writeJSON(w, list)
	})
	// 账号的用量额度（claude / codex 各一份）。?force=1：人点了刷新，绕过缓存（hub 里仍有最小间隔）
	mux.HandleFunc("GET /api/usage", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, s.Hub.Usage(r.Context(), r.URL.Query().Get("force") == "1"))
	})
	mux.HandleFunc("GET /api/pending", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, s.Hub.Pending()) })
	mux.HandleFunc("GET /api/busy", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, s.busy()) })
	s.appUpdateRoutes(mux)
	// A session's slash commands by the key in its summary (hub cmdSets). The key is a hash of the content, so the
	// browser may keep it for good.
	mux.HandleFunc("GET /api/commands/{key}", func(w http.ResponseWriter, r *http.Request) {
		cmds := s.Hub.CommandSet(r.PathValue("key"))
		if cmds == nil {
			httpError(w, 404, "no such command list")
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
		_ = json.NewEncoder(w).Encode(cmds)
	})
	mux.HandleFunc("POST /api/sessions/{id}/answer", s.sessionOp(func(r *http.Request, sess *hub.Session) (any, error) {
		var b struct {
			ReqID    string          `json:"reqId"`
			Response json.RawMessage `json:"response"`
		}
		if err := decode(r, &b); err != nil {
			return nil, err
		}
		return nil, sess.Answer(b.ReqID, b.Response)
	}))
	mux.HandleFunc("POST /api/sessions/{id}/mode", s.sessionOp(func(r *http.Request, sess *hub.Session) (any, error) {
		var b struct{ Mode string }
		if err := decode(r, &b); err != nil {
			return nil, err
		}
		return nil, sess.SetMode(r.Context(), b.Mode)
	}))
	mux.HandleFunc("POST /api/sessions/{id}/config", s.sessionOp(func(r *http.Request, sess *hub.Session) (any, error) {
		var b struct{ ID, Value string }
		if err := decode(r, &b); err != nil {
			return nil, err
		}
		return nil, sess.SetConfig(r.Context(), b.ID, b.Value)
	}))
	mux.HandleFunc("POST /api/sessions/{id}/rename", s.sessionOp(func(r *http.Request, sess *hub.Session) (any, error) {
		var b struct {
			Title string
			Auto  bool // 插件起的：不算人改的名，人改过名时不生效（hub/title.go）
		}
		if err := decode(r, &b); err != nil {
			return nil, err
		}
		if b.Auto {
			sess.SetAutoTitle(b.Title)
		} else {
			sess.Rename(b.Title)
		}
		return nil, nil
	}))
	// 起标题用的材料（插件 g.sessionTitle）：当前标题、这段上下文（最后一次 /clear 之后）用户说过的话
	mux.HandleFunc("GET /api/sessions/{id}/title", s.sessionOp(func(r *http.Request, sess *hub.Session) (any, error) {
		return sess.TitleState(), nil
	}))
	// 用这个会话的 agent 的账号跑一次小问答（插件的 ctx.summarize）
	mux.HandleFunc("POST /api/sessions/{id}/complete", s.sessionOp(func(r *http.Request, sess *hub.Session) (any, error) {
		var b struct{ Prompt string }
		if err := decode(r, &b); err != nil {
			return nil, err
		}
		if strings.TrimSpace(b.Prompt) == "" {
			return nil, errors.New("prompt 是空的")
		}
		t, err := s.Hub.Complete(r.Context(), sess.Summary().Agent, b.Prompt)
		return map[string]string{"text": t}, err
	}))
	mux.HandleFunc("POST /api/sessions/{id}/rewind", s.sessionOp(func(r *http.Request, sess *hub.Session) (any, error) {
		var b struct {
			MessageID string `json:"messageId"`
		}
		if err := decode(r, &b); err != nil {
			return nil, err
		}
		return sess.Rewind(r.Context(), b.MessageID)
	}))
	mux.HandleFunc("GET /api/sessions/{id}/events", s.sessionOp(func(r *http.Request, sess *hub.Session) (any, error) {
		return sess.Events(), nil
	}))
	// 界面往上翻到顶：序号 before 之前的再一段（打开会话时 /ws 只推了最近一段，见 hub/blob.go）
	mux.HandleFunc("GET /api/sessions/{id}/events/older", s.sessionOp(func(r *http.Request, sess *hub.Session) (any, error) {
		before, err := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
		if err != nil {
			return nil, errors.New("before 不对")
		}
		evs, more := sess.OlderEvents(before)
		return map[string]any{"events": evs, "older": more}, nil
	}))
	mux.HandleFunc("GET /api/blobs/{hash}", s.blob)
	// A composer image, stored so the draft can reference it on the other devices instead of carrying the bytes
	mux.HandleFunc("POST /api/blobs", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Data string `json:"data"` // base64
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 40<<20)).Decode(&body); err != nil {
			httpError(w, 400, err.Error())
			return
		}
		u := s.Hub.SaveBlob(body.Data)
		if u == "" {
			httpError(w, 400, "bad image data")
			return
		}
		writeJSON(w, map[string]string{"url": u})
	})
	mux.HandleFunc("POST /api/sessions/{id}/tasks/{task}/stop", s.sessionOp(func(r *http.Request, sess *hub.Session) (any, error) {
		return map[string]any{}, sess.StopTask(r.Context(), r.PathValue("task"))
	}))
	mux.HandleFunc("POST /api/sessions/{id}/tools/{tool}/background", s.sessionOp(func(r *http.Request, sess *hub.Session) (any, error) {
		ok, err := sess.BackgroundTool(r.Context(), r.PathValue("tool"))
		return map[string]any{"backgrounded": ok}, err
	}))
	mux.HandleFunc("POST /api/sessions/{id}/allow-rules", s.sessionOp(func(r *http.Request, sess *hub.Session) (any, error) {
		var b struct {
			Rules []string `json:"rules"`
		}
		if err := decode(r, &b); err != nil {
			return nil, err
		}
		return map[string]any{}, sess.AllowRules(r.Context(), b.Rules)
	}))
	mux.HandleFunc("POST /api/sessions/{id}/escape", s.sessionOp(func(r *http.Request, sess *hub.Session) (any, error) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		return sess.Escape(ctx)
	}))
	mux.HandleFunc("GET /api/sessions/{id}/files", s.sessionOp(s.searchFiles))
	mux.HandleFunc("POST /api/sessions/{id}/reload", s.sessionOp(func(r *http.Request, sess *hub.Session) (any, error) {
		return nil, sess.Reload(r.Context())
	}))
	mux.HandleFunc("POST /api/sessions/{id}/takeover", s.sessionOp(func(r *http.Request, sess *hub.Session) (any, error) {
		var b struct {
			Action string `json:"action"`
		}
		_ = decode(r, &b)
		return nil, sess.Takeover(r.Context(), b.Action)
	}))
	mux.HandleFunc("POST /api/sessions/{id}/warm", s.sessionOp(func(r *http.Request, sess *hub.Session) (any, error) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_ = sess.Warm(ctx) // 不支持的 agent 报错无所谓
		return nil, nil
	}))
	mux.HandleFunc("POST /api/sessions/{id}/withdraw-last", s.sessionOp(func(r *http.Request, sess *hub.Session) (any, error) {
		return sess.WithdrawLast(r.Context())
	}))
	// withdraw：收回指定的一条排队中的插话（对话里「排队中」那行的撤回 / 立即发）
	mux.HandleFunc("POST /api/sessions/{id}/withdraw", s.sessionOp(func(r *http.Request, sess *hub.Session) (any, error) {
		var b struct {
			MessageID string `json:"messageId"`
		}
		if err := decode(r, &b); err != nil {
			return nil, err
		}
		return sess.Withdraw(r.Context(), b.MessageID)
	}))
	mux.HandleFunc("POST /api/sessions/{id}/open", s.sessionOp(func(r *http.Request, sess *hub.Session) (any, error) {
		var b struct {
			Near  string `json:"near"`
			Focus bool   `json:"focus"`
		}
		_ = decode(r, &b)
		if err := s.crossSpace(b.Near, sess.ID()); err != nil {
			return nil, err
		}
		s.Hub.Broadcast("open", map[string]any{"id": sess.Summary().ID, "near": b.Near, "focus": b.Focus})
		return nil, nil
	}))
	// close：open 的反面（gerdr session close），见 Hub.CloseUI。afterTurn：会话关自己，等它这一轮跑完再关（Hub.CloseAfterTurn）
	mux.HandleFunc("POST /api/sessions/{id}/close", s.sessionOp(func(r *http.Request, sess *hub.Session) (any, error) {
		var b struct {
			AfterTurn bool   `json:"afterTurn"`
			From      string `json:"from"` // the agent session asking (gerdr session close); empty from the UI
		}
		_ = decode(r, &b)
		if err := s.crossSpace(b.From, sess.ID()); err != nil {
			return nil, err
		}
		if b.AfterTurn {
			now, err := s.Hub.CloseAfterTurn(r.Context(), sess.ID())
			return map[string]any{"closedNow": now}, err
		}
		return nil, s.Hub.CloseUI(r.Context(), sess.ID())
	}))
	// shutdown：界面关 pane / tab 时调，只关 agent 那边、不广播（布局已经是它自己改的）。
	mux.HandleFunc("POST /api/sessions/{id}/shutdown", s.sessionOp(func(r *http.Request, sess *hub.Session) (any, error) {
		return nil, s.Hub.Shutdown(r.Context(), sess.ID())
	}))
	mux.HandleFunc("GET /api/sessions/{id}", s.sessionOp(func(r *http.Request, sess *hub.Session) (any, error) {
		return sess.Summary(), nil
	}))
	// resolve：agent 跑的 `gerdr session current` 拿自己的 agent 会话 id 换 gerdr 会话。
	mux.HandleFunc("GET /api/resolve", func(w http.ResponseWriter, r *http.Request) {
		for _, agent := range []string{"claude", "codex", "native"} {
			if id := r.URL.Query().Get(agent); id != "" {
				if sess := s.Hub.FindByAgent(agent, id); sess != nil {
					writeJSON(w, sess.Summary())
					return
				}
			}
		}
		httpError(w, 404, "不是 gerdr 里的会话")
	})
	mux.HandleFunc("GET /api/importable", s.importable)
	s.extRoutes(mux)
	s.mcpAuthRoutes(mux)
	s.workRoutes(mux)
	s.llmRoutes(mux)
	s.memoryRoutes(mux)
	s.importRoutes(mux)
	s.doctorRoutes(mux)
	if s.Remote != nil {
		s.remoteRoutes(mux)
		s.appRoutes(mux)
	}
	if s.Service != nil {
		s.serviceRoutes(mux)
	}
	if s.Terms != nil {
		s.termRoutes(mux)
	}
	if s.UI != nil {
		s.rescueRoutes(mux)
	}
	mux.HandleFunc("GET /api/system/stats", s.stats)
	mux.HandleFunc("GET /api/fs/dirs", s.dirs)
	mux.HandleFunc("POST /api/fs/mkdir", s.mkdir)
	mux.HandleFunc("GET /api/fs/stat", s.fileStat)
	mux.HandleFunc("GET /api/fs/raw", s.fileRaw)
	mux.HandleFunc("POST /api/fs/link", s.fileLinkIssue)
	mux.HandleFunc("GET /f/{token}/{name}", s.fileLinkServe)
	mux.HandleFunc("POST /api/fs/exists", s.fileExists)
	mux.HandleFunc("POST /api/fs/upload", s.fileUpload)
	mux.HandleFunc("GET /ws", s.ws)
	mux.HandleFunc("GET /api/ui/status", func(w http.ResponseWriter, r *http.Request) {
		fatal, issues := s.UI.Status()
		writeJSON(w, map[string]any{"dir": s.UI.Dir(), "build_error": fatal, "warnings": issues, "runtime_errors": s.UI.ClientErrors(), "git": webui.Status(s.UI.Dir())})
	})
	mux.HandleFunc("POST /api/ui/errors", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Build string             `json:"build"`
			Items []uisite.ErrorItem `json:"items"`
		}
		if err := decode(r, &b); err != nil {
			httpError(w, 400, err.Error())
			return
		}
		s.UI.ReportClientErrors(b.Build, b.Items)
		writeJSON(w, map[string]any{})
	})
	mux.HandleFunc("DELETE /api/ui/errors", func(w http.ResponseWriter, r *http.Request) {
		s.UI.ClearClientErrors()
		writeJSON(w, map[string]any{})
	})
	mux.Handle("/", s.ui())
	return s.guard(gzipped(mux))
}

// guard：令牌 + CSRF + 安全头。远程（中继）进来的请求换一套规则，见 remoteGuard。
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy", cspWith(""))
		if auth.FromRemote(r) {
			s.remoteGuard(w, r, next)
			return
		}
		p := r.URL.Path
		if p == "/auth" || !(strings.HasPrefix(p, "/api/") || p == "/ws") {
			next.ServeHTTP(w, r)
			return
		}
		if !s.authed(r) {
			httpError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get("X-Gerdr") != "1" {
			// 跨站页面发不出自定义头（要 CORS 预检，而我们不放行），这一道就挡住了 CSRF。
			httpError(w, http.StatusForbidden, "missing X-Gerdr header")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) authed(r *http.Request) bool {
	tok := ""
	if c, err := r.Cookie(s.cookieName()); err == nil {
		tok = c.Value
	}
	if a := r.Header.Get("Authorization"); strings.HasPrefix(a, "Bearer ") {
		tok = strings.TrimPrefix(a, "Bearer ")
	}
	return tok != "" && subtle.ConstantTimeCompare([]byte(tok), []byte(s.Token)) == 1
}

// auth：/auth?token=…&next=/ —— 换一个 HttpOnly cookie，然后跳走（令牌不留在地址栏里）。
func (s *Server) auth(w http.ResponseWriter, r *http.Request) {
	tok := r.URL.Query().Get("token")
	if subtle.ConstantTimeCompare([]byte(tok), []byte(s.Token)) != 1 {
		time.Sleep(500 * time.Millisecond)
		httpError(w, http.StatusUnauthorized, "bad token")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: s.cookieName(), Value: s.Token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 400 * 24 * 3600})
	next := r.URL.Query().Get("next")
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		next = "/"
	}
	http.Redirect(w, r, next, http.StatusFound)
}

// installedAt: when the running binary was installed (unix ms), 0 if unknown. Settings shows it next to the version.
func installedAt() int64 {
	if t := version.Installed(); !t.IsZero() {
		return t.UnixMilli()
	}
	return 0
}

func (s *Server) hello(w http.ResponseWriter, r *http.Request) {
	home, _ := os.UserHomeDir()
	writeJSON(w, map[string]any{
		"version":     version.Version,
		"installedAt": installedAt(),
		"agents":      s.Hub.Agents(),
		"sessions":    s.Hub.List(),
		"layout":      s.Layout.Get(),
		"config":      s.Config.Get(),
		"home":        home,
		"pluginsDir":  s.Plugins.Dir(),
		"remoteConns": s.remoteConns.list(),
	})
}

func (s *Server) plugins(w http.ResponseWriter, r *http.Request) {
	type view struct {
		*plugins.Plugin
		Service *plugins.ServiceStatus `json:"service,omitempty"`
	}
	out := []view{}
	for _, p := range s.Plugins.List() {
		v := view{Plugin: p}
		if s.PluginSvc != nil {
			v.Service = s.PluginSvc.Status(p.ID)
		}
		out = append(out, v)
	}
	writeJSON(w, out)
}

func (s *Server) putStore(st *config.Store, name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Data   json.RawMessage `json:"data"`
			Client string          `json:"client"`
		}
		if err := decode(r, &b); err != nil {
			httpError(w, 400, err.Error())
			return
		}
		if err := st.Set(b.Data); err != nil {
			httpError(w, 400, err.Error())
			return
		}
		if name == "config" && s.Secrets != nil {
			s.Secrets.Prune(st.Get())
		}
		// What was stored, not what was sent: a key typed into Settings must not go out to every client.
		s.Hub.Broadcast(name, map[string]any{"data": st.Get(), "client": b.Client})
		if name == "config" && s.OnConfig != nil {
			s.OnConfig()
		}
		writeJSON(w, map[string]any{"ok": true})
	}
}

func (s *Server) createSession(w http.ResponseWriter, r *http.Request) {
	var o struct {
		hub.CreateOptions
		// Open：在界面上打开它。Near 是「放在哪个会话旁边」（agent 开的会话放在它自己旁边），
		// Focus 要不要把焦点切过去（agent 开的默认不切，别打断人）。界面没开着就什么都不发生。
		Open *struct {
			Near  string `json:"near"`
			Focus bool   `json:"focus"`
		} `json:"open"`
	}
	if err := decode(r, &o); err != nil {
		httpError(w, 400, err.Error())
		return
	}
	// 带 open 的是 `gerdr session new`（agent 跑的），界面自己开会话不带
	o.CreateOptions.FromCLI = o.Open != nil
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	sess, err := s.Hub.Create(ctx, o.CreateOptions)
	if err != nil {
		httpError(w, 400, err.Error())
		return
	}
	if o.Open != nil {
		s.Hub.Broadcast("open", map[string]any{"id": sess.Summary().ID, "near": o.Open.Near, "focus": o.Open.Focus})
	}
	writeJSON(w, sess.Summary())
}

func (s *Server) send(r *http.Request, sess *hub.Session) (any, error) {
	var b struct {
		Content []acp.ContentBlock `json:"content"`
		ID      string             `json:"id"`   // client 生成的 UUID：重发同一条时去重
		From    string             `json:"from"` // 别的会话经 gerdr session send 发来的：发送方会话 id
		Now     bool               `json:"now"`  // 在跑时立即插话（截断当前这次生成），默认排到下一个空档
	}
	if err := decode(r, &b); err != nil {
		return nil, err
	}
	if len(b.Content) == 0 {
		return nil, errors.New("empty message")
	}
	if err := s.crossSpace(b.From, sess.ID()); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	return sess.SendFrom(ctx, b.Content, b.ID, b.From, b.Now)
}

func (s *Server) sessionOp(fn func(*http.Request, *hub.Session) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, err := s.Hub.Session(r.PathValue("id"))
		if err != nil {
			httpError(w, 404, err.Error())
			return
		}
		res, err := fn(r, sess)
		var held *hub.HeldError
		if errors.As(err, &held) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(409)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error(), "code": "held_elsewhere", "holders": held.Holders})
			return
		}
		if err != nil {
			httpError(w, 400, err.Error())
			return
		}
		if res == nil {
			res = map[string]any{"ok": true}
		}
		writeJSON(w, res)
	}
}

func (s *Server) importable(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	list, err := s.Hub.ListImportable(ctx, r.URL.Query().Get("agent"), r.URL.Query().Get("cwd"))
	if err != nil {
		httpError(w, 400, err.Error())
		return
	}
	if list == nil {
		list = []acp.SessionInfo{}
	}
	writeJSON(w, list)
}

// dirs：选工作目录用的目录浏览（只列目录名）。
func (s *Server) dirs(w http.ResponseWriter, r *http.Request) {
	home, _ := os.UserHomeDir()
	p := r.URL.Query().Get("path")
	if p == "" {
		p = home
	}
	if strings.HasPrefix(p, "~") {
		p = filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	p = filepath.Clean(p)
	ents, err := os.ReadDir(p)
	if err != nil {
		// 404 tells the path picker "doesn't exist" (created on submit) apart from "not a folder" / no permission
		code := 400
		if os.IsNotExist(err) {
			code = 404
		}
		httpError(w, code, err.Error())
		return
	}
	dirs := []string{}
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		} else if e.Type()&os.ModeSymlink != 0 {
			if st, err := os.Stat(filepath.Join(p, e.Name())); err == nil && st.IsDir() {
				dirs = append(dirs, e.Name())
			}
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return strings.ToLower(dirs[i]) < strings.ToLower(dirs[j]) })
	_, gerr := os.Stat(filepath.Join(p, ".git"))
	writeJSON(w, map[string]any{"path": p, "parent": filepath.Dir(p), "dirs": dirs, "home": home, "git": gerr == nil})
}

// mkdir: creates the folder a new session / workspace is started in when it doesn't exist yet. Parents are created too (a new
// project is often two levels down, ~/dev/new-org/new-repo). Nothing more than an agent or terminal here can already do.
func (s *Server) mkdir(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Path) == "" {
		httpError(w, 400, "path is required")
		return
	}
	p := body.Path
	if !strings.HasPrefix(p, "~") && !filepath.IsAbs(p) {
		httpError(w, 400, "need an absolute path: "+p)
		return
	}
	p = resolvePath(p, "")
	if err := os.MkdirAll(p, 0o755); err != nil {
		httpError(w, 400, err.Error())
		return
	}
	writeJSON(w, map[string]any{"path": p})
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 64 << 10,
	// permessage-deflate：会话日志是 JSON，压完小好几倍。远程（中继 / 手机流量）打开大会话时，
	// 整份日志是一帧，不压的话慢网络上 15 秒写不完就被写期限掐断、重连再推，永远打不开。
	// 中继那段是字节隧道，压缩是浏览器和这里端到端协商的。
	EnableCompression: true,
	CheckOrigin: func(r *http.Request) bool {
		o := r.Header.Get("Origin")
		if o == "" {
			return true // 非浏览器客户端（有令牌就行）
		}
		u, err := url.Parse(o)
		return err == nil && u.Host == r.Host
	},
}

func (s *Server) awakeStatus(w http.ResponseWriter, r *http.Request) {
	if s.Awake == nil {
		writeJSON(w, awake.Status{})
		return
	}
	writeJSON(w, s.Awake.Status())
}

// setAwake：手动时段。{minutes: n} 从现在起 n 分钟；{until: unix 毫秒}；{forever: true}；都没有 = 结束。
func (s *Server) setAwake(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Minutes float64 `json:"minutes"`
		Until   int64   `json:"until"`
		Forever bool    `json:"forever"`
	}
	if err := decode(r, &b); err != nil {
		httpError(w, 400, err.Error())
		return
	}
	if s.Awake == nil {
		httpError(w, 501, "这个进程不管休眠")
		return
	}
	var until time.Time
	switch {
	case b.Forever:
		until = awake.Forever
	case b.Minutes > 0:
		until = time.Now().Add(time.Duration(b.Minutes * float64(time.Minute)))
	case b.Until > 0:
		until = time.UnixMilli(b.Until)
	}
	s.Awake.SetManual(until)
	writeJSON(w, s.Awake.Status())
}

// blob：日志里的图片（推给界面时换成了地址，见 hub/blob.go）。按内容的 sha256 起名，内容不会变：让浏览器一直缓存。
func (s *Server) blob(w http.ResponseWriter, r *http.Request) {
	p := s.Hub.BlobPath(r.PathValue("hash"))
	if p == "" {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(p)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	// 内容是用户贴的 / 工具读的，类型靠嗅：万一是个 HTML 也别让它在我们的源下跑脚本
	w.Header().Set("Content-Security-Policy", "sandbox")
	// The lightbox's download button: blobs have no name of their own, so make one from the hash and the sniffed
	// type (the Android App's DownloadManager names the file from this header, not from the <a download> name).
	if r.URL.Query().Get("download") == "1" {
		head := make([]byte, 512)
		n, _ := io.ReadFull(f, head)
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		name := "image-" + r.PathValue("hash")[:min(8, len(r.PathValue("hash")))]
		if exts, _ := mime.ExtensionsByType(http.DetectContentType(head[:n])); len(exts) > 0 {
			name += exts[len(exts)-1]
		}
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	}
	http.ServeContent(w, r, "", st.ModTime(), f)
}

// ws：推会话列表 / 日志 / 布局 / 配置的变化。客户端发两种消息：{t:"watch", ids:[…], since:{id:seq}}、{t:"ping"}（回 {t:"pong"}）。
// Plus {t:"draft", id, draft:{text, sel, imgs, from}}: the unsent composer content, mirrored to the other devices (hub/draft.go).
func (s *Server) ws(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	// 远程界面开着就一直有这条 /ws（走到这儿已经过了 remoteGuard，是配过对的设备）：按它算「有远程连着」
	if auth.FromRemote(r) {
		defer s.trackRemote(r)()
		if s.Awake != nil {
			defer s.Awake.Hold()()
		}
	}
	sub := s.Hub.Subscribe()
	defer s.Hub.Unsubscribe(sub)
	done := make(chan struct{})
	pong := make(chan struct{}, 1)
	// 读期限：客户端 20 秒一次应用层 ping，浏览器对协议层 ping（下面 25 秒一次）也会自动回 pong，收到哪个都续上。
	// 70 秒什么都没收到就是死连接（手机换网、中继那头断了）：不设的话它一直挂在「正在连接」里、一直挡着睡眠。
	const readWait = 70 * time.Second
	_ = conn.SetReadDeadline(time.Now().Add(readWait))
	conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(readWait)) })
	go func() {
		defer close(done)
		// 读这头断了（客户端关了 / 死了）就把整条连接关掉：写的那头可能正卡在一大帧日志上（写期限 30 秒），
		// 不关的话它要等写完或超时才发现，这期间重连上来的新连接和它叠在一起，「正在连接」里一台手机列好几条（踩过）。
		defer conn.Close()
		conn.SetReadLimit(1 << 20)
		for {
			_, b, err := conn.ReadMessage()
			if err != nil {
				return
			}
			_ = conn.SetReadDeadline(time.Now().Add(readWait))
			var m struct {
				T     string           `json:"t"`
				IDs   []string         `json:"ids"`
				Since map[string]int64 `json:"since"` // 会话 id → 前端已有日志的最后序号，接得上就只补后面的
				ID    string           `json:"id"`    // draft: which session
				Draft hub.Draft        `json:"draft"` // draft: text, selection, attachment refs, writer's client id
			}
			if json.Unmarshal(b, &m) != nil {
				continue
			}
			switch m.T {
			case "watch":
				s.Hub.Watch(sub, m.IDs, m.Since)
			case "draft":
				// The composer text, mirrored to every device looking at this session (hub/draft.go)
				s.Hub.SetDraft(m.ID, m.Draft)
			case "ping":
				// 应用层心跳：浏览器读不到协议层的 ping/pong，只能靠这个判断连接是不是「假在线」。
				select {
				case pong <- struct{}{}:
				default:
				}
			}
		}
	}()
	// 写只在这一个 goroutine 里（gorilla 不许并发写），顺带每 25 秒 ping 一次。
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	var batch []hub.Msg
	flush := time.NewTimer(time.Hour)
	flush.Stop()
	// 写失败（多半是写期限到了还没写完：远程慢、一帧太大）记一笔：连接断了界面只会显示「离线」，原因在这儿查
	write := func(v any) bool {
		b, err := json.Marshal(v)
		if err != nil {
			return false
		}
		start := time.Now()
		_ = conn.SetWriteDeadline(start.Add(30 * time.Second))
		if err := conn.WriteMessage(websocket.TextMessage, b); err != nil {
			if s.Log != nil {
				s.Log.Info("ws write failed", "remote", auth.FromRemote(r), "bytes", len(b), "took", time.Since(start).Round(time.Millisecond), "err", err)
			}
			return false
		}
		return true
	}
	for {
		select {
		case <-done:
			return
		case m, ok := <-sub.C():
			if !ok {
				return
			}
			// 流式片段很碎：攒 30ms 一起发，一帧一个数组。
			batch = append(batch, m)
			if len(batch) == 1 {
				flush.Reset(30 * time.Millisecond)
			}
			if len(batch) >= 256 {
				if !write(batch) {
					return
				}
				batch = nil
			}
		case <-flush.C:
			if len(batch) > 0 {
				if !write(batch) {
					return
				}
				batch = nil
			}
		case <-pong:
			if !write([]map[string]string{{"t": "pong"}}) {
				return
			}
		case <-ping.C:
			_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if conn.WriteMessage(websocket.PingMessage, nil) != nil {
				return
			}
		}
	}
}

// ui：界面。没有打包好的文件 —— 页面、每个源码模块、热更新推送都由 uisite 现编现发（见包注释）。
func (s *Server) ui() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case strings.HasPrefix(p, "/_ui/m/"):
			s.UI.ServeModule(w, r)
		case p == "/_ui/events":
			s.UI.ServeEvents(w, r)
		case strings.HasPrefix(p, "/_esm/"):
			s.ESM.ServeHTTP(w, r)
		case s.UI.ServePublic(w, r):
		case strings.HasPrefix(p, "/api/"):
			// 不认识的接口别落到下面「前端路由」出页面：200 + HTML 会被界面当成成功（旧后端配新界面时踩过：
			// 开终端拿到一页 HTML，布局里记了个空终端，渲染直接崩）。
			httpError(w, 404, "没有这个接口（gerdr 版本旧了？）")
		case path.Ext(p) != "" && !strings.HasSuffix(p, ".html"):
			http.NotFound(w, r)
		default:
			// 前端路由：不认识的路径都出页面。importMap 只能内联，内联脚本靠 nonce 过 CSP。
			nonce := newNonce()
			w.Header().Set("Content-Security-Policy", cspWith("'nonce-"+nonce+"'"))
			s.UI.ServePage(w, r, nonce)
		}
	})
}

func newNonce() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return base64.StdEncoding.EncodeToString(b)
}

func cspWith(extraScript string) string {
	return "default-src 'self'; script-src 'self' " + extraScript + " blob: 'wasm-unsafe-eval'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob: https:; font-src 'self' data:; connect-src 'self'; worker-src 'self' blob:; frame-ancestors 'none'"
}

// ---- helpers ----

func decode(r *http.Request, v any) error {
	b, err := io.ReadAll(io.LimitReader(r.Body, 64<<20))
	if err != nil {
		return err
	}
	if len(b) == 0 {
		return nil
	}
	return json.Unmarshal(b, v)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}

func writeRaw(w http.ResponseWriter, b json.RawMessage) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(b)
}

func httpError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": msg})
}

// Listen 开监听；port 为 0 时随便挑一个。
func Listen(host string, port int) (net.Listener, error) {
	return net.Listen("tcp", net.JoinHostPort(host, itoa(port)))
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// Busy is what a restart of this process would cut off. The desktop shell (desktop/updater.js) and the App's
// attaching backend (cmd/gerdr appAttach) only swap in a new gerdr on their own when everything is zero:
// idle sessions come back on their own after a restart (the next message resumes them), but a running turn
// is interrupted, a permission request is lost, background tasks live only in memory, and terminals are
// gerdr's own child processes (docs/dev/ARCHITECTURE.md, terminals).
type Busy struct {
	Version string `json:"version"`
	Turns   int    `json:"turns"`   // sessions with a turn running
	Waiting int    `json:"waiting"` // sessions waiting on a person (permission / question)
	Tasks   int    `json:"tasks"`   // background tasks still running
	Terms   int    `json:"terms"`   // live terminals
}

func (b Busy) Idle() bool { return b.Turns+b.Waiting+b.Tasks+b.Terms == 0 }

func (s *Server) busy() Busy {
	b := Busy{Version: version.Version}
	for _, x := range s.Hub.List() {
		if x.Turn != nil {
			b.Turns++
		}
		if x.Pending > 0 {
			b.Waiting++
		}
		for _, t := range x.Tasks {
			if t.Status == "running" {
				b.Tasks++
			}
		}
	}
	if s.Terms != nil {
		for _, t := range s.Terms.List() {
			if !t.Exited {
				b.Terms++
			}
		}
	}
	return b
}
