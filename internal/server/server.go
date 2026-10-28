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
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/coder/acp-go-sdk"
	"github.com/gorilla/websocket"

	"github.com/zbysir/gerdr/internal/auth"
	"github.com/zbysir/gerdr/internal/config"
	"github.com/zbysir/gerdr/internal/ext"
	"github.com/zbysir/gerdr/internal/hub"
	"github.com/zbysir/gerdr/internal/plugins"
	"github.com/zbysir/gerdr/internal/remote"
	"github.com/zbysir/gerdr/internal/service"
	"github.com/zbysir/gerdr/internal/term"
	"github.com/zbysir/gerdr/internal/uisite"
	"github.com/zbysir/gerdr/internal/version"
	"github.com/zbysir/gerdr/internal/webui"
)

type Server struct {
	Hub     *hub.Hub
	Token   string
	Config  *config.Store
	Layout  *config.Store
	Plugins *plugins.Manager
	Ext     *ext.Manager
	UI      *uisite.Renderer
	ESM     *uisite.ESM
	Log     *slog.Logger
	// OnConfig：界面上存了 config.json 之后（agents 那一节要热换）
	OnConfig func()
	// Remote：远程访问（设备表、passkey、中继）。nil = 这个进程不管远程（测试里）。
	Remote *remote.Service
	// Service：后台常驻（launchd）。nil = 不管（测试里）。
	Service *service.Manager
	// Terms：界面里的终端。nil = 不开（测试里）。
	Terms *term.Manager
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
	mux := http.NewServeMux()
	mux.HandleFunc("GET /auth", s.auth)
	mux.HandleFunc("GET /api/hello", s.hello)
	mux.HandleFunc("GET /api/plugins", s.plugins)
	mux.HandleFunc("GET /api/config", func(w http.ResponseWriter, r *http.Request) { writeRaw(w, s.Config.Get()) })
	mux.HandleFunc("PUT /api/config", s.putStore(s.Config, "config"))
	mux.HandleFunc("GET /api/layout", func(w http.ResponseWriter, r *http.Request) { writeRaw(w, s.Layout.Get()) })
	mux.HandleFunc("PUT /api/layout", s.putStore(s.Layout, "layout"))
	mux.HandleFunc("POST /api/sessions", s.createSession)
	mux.HandleFunc("DELETE /api/sessions/{id}", s.sessionOp(func(r *http.Request, sess *hub.Session) (any, error) {
		return nil, s.Hub.Delete(r.Context(), sess.ID())
	}))
	mux.HandleFunc("POST /api/sessions/{id}/send", s.sessionOp(s.send))
	mux.HandleFunc("POST /api/sessions/{id}/cancel", s.sessionOp(func(r *http.Request, sess *hub.Session) (any, error) {
		return nil, sess.Cancel(r.Context())
	}))
	// 全部会话，最近有动静的在前（给外面的程序；界面用的是 /api/hello 那一大包）
	mux.HandleFunc("GET /api/sessions", func(w http.ResponseWriter, r *http.Request) {
		list := s.Hub.List()
		sort.SliceStable(list, func(i, j int) bool { return list[i].UpdatedAt > list[j].UpdatedAt })
		writeJSON(w, list)
	})
	mux.HandleFunc("GET /api/pending", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, s.Hub.Pending()) })
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
		var b struct{ Title string }
		if err := decode(r, &b); err != nil {
			return nil, err
		}
		sess.Rename(b.Title)
		return nil, nil
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
	mux.HandleFunc("POST /api/sessions/{id}/open", s.sessionOp(func(r *http.Request, sess *hub.Session) (any, error) {
		var b struct {
			Near  string `json:"near"`
			Focus bool   `json:"focus"`
		}
		_ = decode(r, &b)
		s.Hub.Broadcast("open", map[string]any{"id": sess.Summary().ID, "near": b.Near, "focus": b.Focus})
		return nil, nil
	}))
	mux.HandleFunc("GET /api/sessions/{id}", s.sessionOp(func(r *http.Request, sess *hub.Session) (any, error) {
		return sess.Summary(), nil
	}))
	// resolve：agent 跑的 `gerdr session current` 拿自己的 agent 会话 id 换 gerdr 会话。
	mux.HandleFunc("GET /api/resolve", func(w http.ResponseWriter, r *http.Request) {
		for _, agent := range []string{"claude", "codex"} {
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
	s.importRoutes(mux)
	if s.Remote != nil {
		s.remoteRoutes(mux)
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
	mux.HandleFunc("GET /api/fs/stat", s.fileStat)
	mux.HandleFunc("GET /api/fs/raw", s.fileRaw)
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
	mux.Handle("/", s.ui())
	return s.guard(mux)
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

func (s *Server) hello(w http.ResponseWriter, r *http.Request) {
	home, _ := os.UserHomeDir()
	writeJSON(w, map[string]any{
		"version":    version.Version,
		"agents":     s.Hub.Agents(),
		"sessions":   s.Hub.List(),
		"layout":     s.Layout.Get(),
		"config":     s.Config.Get(),
		"home":       home,
		"pluginsDir": s.Plugins.Dir(),
	})
}

func (s *Server) plugins(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.Plugins.List())
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
		s.Hub.Broadcast(name, map[string]any{"data": b.Data, "client": b.Client})
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
	}
	if err := decode(r, &b); err != nil {
		return nil, err
	}
	if len(b.Content) == 0 {
		return nil, errors.New("empty message")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	return sess.SendFrom(ctx, b.Content, b.ID, b.From)
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
		httpError(w, 400, err.Error())
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

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 64 << 10,
	CheckOrigin: func(r *http.Request) bool {
		o := r.Header.Get("Origin")
		if o == "" {
			return true // 非浏览器客户端（有令牌就行）
		}
		u, err := url.Parse(o)
		return err == nil && u.Host == r.Host
	},
}

// ws：推会话列表 / 日志 / 布局 / 配置的变化。客户端发两种消息：{t:"watch", ids:[…]}、{t:"ping"}（回 {t:"pong"}）。
func (s *Server) ws(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	sub := s.Hub.Subscribe()
	defer s.Hub.Unsubscribe(sub)
	done := make(chan struct{})
	pong := make(chan struct{}, 1)
	go func() {
		defer close(done)
		conn.SetReadLimit(1 << 20)
		for {
			_, b, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var m struct {
				T   string   `json:"t"`
				IDs []string `json:"ids"`
			}
			if json.Unmarshal(b, &m) != nil {
				continue
			}
			switch m.T {
			case "watch":
				s.Hub.Watch(sub, m.IDs)
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
	write := func(v any) bool {
		_ = conn.SetWriteDeadline(time.Now().Add(15 * time.Second))
		return conn.WriteJSON(v) == nil
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
