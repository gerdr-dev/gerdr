// Package server：HTTP API + WebSocket + 内嵌的前端。
//
// 这个服务能让人（经由 agent）在这台机器上跑任意命令，所以**永远要登录令牌**，哪怕只听
// 127.0.0.1 —— 本机端口不一定只有本机连得到（隧道、端口转发），从进程里看不出来。
// 远程（云端中继）进来的请求换一套凭据（设备 cookie + passkey，不认令牌），见 remoteapi.go 和 docs/dev/REMOTE.md。
package server

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/coder/acp-go-sdk"

	"github.com/gerdr-dev/gerdr/internal/addons"
	"github.com/gerdr-dev/gerdr/internal/auth"
	"github.com/gerdr-dev/gerdr/internal/awake"
	"github.com/gerdr-dev/gerdr/internal/config"
	"github.com/gerdr-dev/gerdr/internal/doctor"
	"github.com/gerdr-dev/gerdr/internal/events"
	"github.com/gerdr-dev/gerdr/internal/ext"
	"github.com/gerdr-dev/gerdr/internal/hub"
	"github.com/gerdr-dev/gerdr/internal/i18n"
	"github.com/gerdr-dev/gerdr/internal/peer"
	"github.com/gerdr-dev/gerdr/internal/remote"
	"github.com/gerdr-dev/gerdr/internal/service"
	"github.com/gerdr-dev/gerdr/internal/term"
	"github.com/gerdr-dev/gerdr/internal/uisite"
	"github.com/gerdr-dev/gerdr/internal/version"
	"github.com/gerdr-dev/gerdr/internal/work"
)

type Server struct {
	Dir     string // 数据目录（~/.gerdr）
	Hub     *hub.Hub
	Token   string
	Config  *config.Store
	Secrets *config.Secrets // API keys moved out of config.json (config/secrets.go)
	Layout  *config.Store
	// Unread: sessions that finished unseen, shared by every device (unread.go). nil = off (tests).
	Unread *config.Store
	Addons *addons.Manager
	// AddonSvc runs the add-ons' services (addon.json); RunAddonServices starts it.
	AddonSvc *addons.Services
	Ext      *ext.Manager
	// Work：the Work view's local functions (internal/work). nil = off (tests).
	Work *work.Runner
	// Events: the event store (internal/events, docs/dev/EVENTS.md); RunEvents wires its subscribers. nil = off.
	Events *events.Store
	UI     *uisite.Renderer
	ESM    *uisite.ESM
	Log    *slog.Logger
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
	Doctor *doctor.Doctor
	// Peers: other computers this Gerdr drives (hosts.go, docs/dev/MULTIHOST.md). nil = off (tests).
	Peers       *peer.Manager
	remoteConns remoteConns // 正在连着的远程界面（remoteconns.go）
	appWatch    appWatch    // 安卓 App 后台通知的长轮询（appwatch.go）
	links       fileLinks   // cookie-less links to one file, for opening it in the system browser (filelink.go)
	appUpdate   appUpdate   // the desktop App's update status, relayed by its page for other devices (appupdate.go)
	// OnServiceInstalled：刚打开后台常驻、本进程是 App 起的后端时让位（见 cmd/gerdr 的 handover）。
	OnServiceInstalled func()
	// Restart: shut this process down so it starts again (POST /api/restart). Set only where something brings it
	// back: the launchd service (KeepAlive) and the App's own backend (the shell restarts it on exitHandover).
	// nil for a gerdr started in a terminal: nothing would start it again.
	Restart func()
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
	if s.Peers != nil && s.Hub != nil {
		// A session here may report to one on another computer; the hub's loop check walks on through it (hosts.go)
		s.Hub.Elsewhere = func(id string) (string, bool) {
			if _, err := s.Hub.Session(id); err == nil {
				return "", false
			}
			return s.reportNext(id)
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /auth", s.auth)
	mux.HandleFunc("GET /api/hello", s.hello)
	s.addonRoutes(mux)
	s.eventRoutes(mux)
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
			httpError(w, 501, i18n.S("这个进程不管休眠", "This process doesn't manage sleep"))
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
	mux.HandleFunc("POST /api/unread", s.postUnread)
	mux.HandleFunc("POST /api/layout/op", s.layoutOp) // gerdr workspace / tab / pane (layoutapi.go)
	s.sessionRoutes(mux)
	s.hostRoutes(mux)
	s.folderRoutes(mux)
	// 账号的用量额度（claude / codex 各一份）。?force=1：人点了刷新，绕过缓存（hub 里仍有最小间隔）
	mux.HandleFunc("GET /api/usage", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, s.Hub.Usage(r.Context(), r.URL.Query().Get("force") == "1"))
	})
	// One line per finished turn in [since, until) (unix ms; until defaults to now), oldest first (hub/turns.go):
	// add-ons count what they want from it. "turn" is broadcast with each new one.
	mux.HandleFunc("GET /api/turns", func(w http.ResponseWriter, r *http.Request) {
		since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
		until, _ := strconv.ParseInt(r.URL.Query().Get("until"), 10, 64)
		writeJSON(w, s.Hub.Turns(since, until))
	})
	mux.HandleFunc("GET /api/pending", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, s.Hub.Pending()) })
	mux.HandleFunc("GET /api/busy", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, s.busy()) })
	s.appUpdateRoutes(mux)
	debugRoutes(mux)
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
	// A session's mode / option lists by the key in its summary (hub optSets): content-hashed, cacheable for good.
	mux.HandleFunc("GET /api/options/{key}", func(w http.ResponseWriter, r *http.Request) {
		set, ok := s.Hub.OptionSet(r.PathValue("key"))
		if !ok {
			httpError(w, 404, "no such option set")
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
		_ = json.NewEncoder(w).Encode(set)
	})
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
	mux.HandleFunc("GET /api/importable", s.importable)
	s.extRoutes(mux)
	s.mcpAuthRoutes(mux)
	s.workRoutes(mux)
	s.llmRoutes(mux)
	s.memoryRoutes(mux)
	s.importRoutes(mux)
	s.doctorRoutes(mux)
	s.screenRoutes(mux)
	if s.Remote != nil {
		s.remoteRoutes(mux)
		s.appRoutes(mux)
	}
	if s.Service != nil {
		s.serviceRoutes(mux)
	}
	s.restartRoutes(mux)
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
	s.uiRoutes(mux)
	mux.Handle("/", s.ui())
	return s.guard(gzipped(s.withHosts(mux)))
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
		// /h/<host>/… is another computer's API through this one (hosts.go): as guarded as /api/
		if p == "/auth" || !(strings.HasPrefix(p, "/api/") || p == "/ws" || strings.HasPrefix(p, "/h/")) {
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
	// ?only=sessions,layout: just those parts. `gerdr session …` (agents run it all the time) needs the sessions and
	// the layout only; the rest (agents with their availability checks, config, add-ons…) was built for nothing.
	var only map[string]bool
	if o := r.URL.Query().Get("only"); o != "" {
		only = map[string]bool{}
		for _, k := range strings.Split(o, ",") {
			only[strings.TrimSpace(k)] = true
		}
	}
	out := map[string]any{"version": version.Version}
	part := func(k string, f func() any) {
		if only == nil || only[k] {
			out[k] = f()
		}
	}
	home, _ := os.UserHomeDir()
	part("installedAt", func() any { return installedAt() })
	part("agents", func() any { return s.Hub.Agents() })
	part("sessions", func() any { return s.allSessions(r.Context(), 5*time.Second) })
	part("hosts", func() any { return s.hostInfos() })
	part("layout", func() any { return s.Layout.Get() })
	part("config", func() any { return s.Config.Get() })
	part("unread", func() any { return s.unreadSet() })
	part("home", func() any { return home })
	part("addonsDir", func() any { return s.Addons.Dir() })
	part("remoteConns", func() any { return s.remoteConns.list() })
	writeJSON(w, out)
}

// allSessions: this computer's sessions and every host's (hosts.go). A host's list may be up to maxAge old.
func (s *Server) allSessions(ctx context.Context, maxAge time.Duration) []any {
	local := s.Hub.List()
	out := make([]any, 0, len(local))
	for _, x := range local {
		out = append(out, x)
	}
	for _, x := range s.hostSessions(ctx, maxAge) {
		out = append(out, x)
	}
	return out
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
		httpError(w, 501, i18n.S("这个进程不管休眠", "This process doesn't manage sleep"))
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
