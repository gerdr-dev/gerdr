package server

import (
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"github.com/zbysir/gerdr/internal/auth"
	"github.com/zbysir/gerdr/internal/remote"
)

// 远程访问那条路（云端中继进来的请求）。设计见 docs/dev/REMOTE.md，要点三条：
//
//   - **两条路两套凭据**。本机口认 ~/.gerdr/token（mac App、`gerdr session`、agent 都靠它）；远程口
//     **一律不认它**，只认配对码 / passkey 换来的设备 cookie。令牌文件是 agent 读得到的明文，
//     被 prompt injection 读走是日常风险 —— 它要是在远程也好使，读走就等于整个互联网上的一个 shell。
//   - **没认证的远程请求只拿得到配对页**（pairpage.html，自带的一页，不是 React 界面）。界面源码、
//     esm 代理（会往磁盘里缓存任意 npm 包）、插件，一个字节都不给没配过对的人。
//   - **配对码只在本机出**（/api/remote/pair-code 远程一律 403）：它造的是一份不随创造者一起被撤销的
//     凭据，远程能出码的话，手机被人拿去一次就能留下一台你踢不掉的设备。

// RemoteHandler 盖上「从远程进来的」的章。只给隧道上那个 http.Server 用 —— 判据是**落在哪个
// 监听上**，不是源 IP 或 Host（见 auth.FromRemote）。
func RemoteHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, auth.MarkRemote(r))
	})
}

// remotePublic：没认证也能调的口 —— 配对页自己得能加载和提交。
func remotePublic(r *http.Request) bool {
	switch r.URL.Path {
	case "/api/remote/whoami", "/api/remote/pair",
		"/api/remote/passkey/login/begin", "/api/remote/passkey/login/finish":
		return true
	}
	return false
}

// remoteGuard 代替 guard 里令牌那一段。走到这儿的请求一定来自隧道。
func (s *Server) remoteGuard(w http.ResponseWriter, r *http.Request, next http.Handler) {
	rm := s.Remote
	if rm == nil || !rm.Cfg.Enabled() {
		http.NotFound(w, r)
		return
	}
	// Host 白名单：DNS rebinding 的防线。隧道那头只该有一个名字（中继按 SNI 转过来的就是它）。
	if !sameHost(r.Host, rm.Cfg.Host) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusMisdirectedRequest)
		_, _ = w.Write([]byte("Host 不对：这里只服务 " + rm.Cfg.Host + "\n"))
		return
	}
	// 写接口一律要自定义头（跨站页面发不出来：要 CORS 预检，而我们不答），外加 Origin 同源。
	// cookie 是浏览器自动带的，SameSite=Strict 之外再叠这两道。配对 / passkey 登录也算：
	// 跨站页面替你提交一个配对码，就能把**它的**设备配进来。
	if r.Method != http.MethodGet && r.Method != http.MethodHead && (r.Header.Get("X-Gerdr") != "1" || !originOK(r)) {
		httpError(w, http.StatusForbidden, "跨站请求被拒")
		return
	}
	p := r.URL.Path
	// 本机专用的口：令牌换 cookie、出配对码、解熔断。远程连 404 都不给区分。
	// 证书配置里有 DNS 服务商的密钥，也只在本机改。
	if p == "/auth" || p == "/api/remote/pair-code" || p == "/api/remote/unlock" || p == "/api/remote/cert" || p == "/api/service" || p == "/api/awake/lid-setup" ||
		p == "/api/remote/stepup/pending" || strings.HasPrefix(p, "/api/remote/stepup/approve/") {
		httpError(w, http.StatusForbidden, "这个操作只能在跑 gerdr 的那台机器上做")
		return
	}
	if r.Method == http.MethodGet && r.URL.Query().Get("pair") != "" && !strings.HasPrefix(p, "/api/") {
		s.enterPair(w, r)
		return
	}
	if remotePublic(r) {
		next.ServeHTTP(w, r)
		return
	}
	id := rm.Auth.Authenticate(r)
	need := ""
	if id == nil {
		need = "pair"
	} else if s.reauthNeeded(id) {
		need = "passkey"
	}
	if need != "" {
		if strings.HasPrefix(p, "/api/") || p == "/ws" || strings.HasPrefix(p, "/_") || (path0Ext(p) && !strings.HasSuffix(p, ".html")) {
			// need 让前端区分「没配对」和「配过但要重新验一次」：一个要配对码，一个只要点一下 Face ID
			writeJSONStatus(w, http.StatusUnauthorized, map[string]any{"error": "这台设备还没配对", "need": need})
			return
		}
		s.servePairPage(w, r)
		return
	}
	next.ServeHTTP(w, r)
}

func path0Ext(p string) bool {
	i := strings.LastIndexByte(p, '/')
	return strings.Contains(p[i+1:], ".")
}

func sameHost(reqHost, want string) bool {
	h := reqHost
	if x, port, err := net.SplitHostPort(reqHost); err == nil {
		if port != "443" {
			return false
		}
		h = x
	}
	return strings.EqualFold(strings.TrimSuffix(h, "."), want)
}

// originOK：带了 Origin 就必须同源。浏览器在跨源请求和 WebSocket 握手上一定会带这个头。
func originOK(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return true
	}
	u, err := url.Parse(o)
	return err == nil && strings.EqualFold(u.Host, r.Host)
}

// enterPair：扫码进来的 `/?pair=CODE` —— 换成 cookie，然后 302 把码从地址栏里抹掉
// （浏览器历史、书签同步、截图都不再是泄露渠道）。
func (s *Server) enterPair(w http.ResponseWriter, r *http.Request) {
	rm := s.Remote
	q := r.URL.Query()
	code := q.Get("pair")
	q.Del("pair")
	// 已经有设备凭据了就什么都不做，只把 URL 洗干净 —— 否则同一个链接每打开一次就多一台设备
	if rm.Auth.Authenticate(r) == nil {
		release, ok := s.gateCheck(w, r)
		if !ok {
			return
		}
		defer release()
		ip := auth.ClientIP(r)
		if _, token, err := rm.Auth.Redeem(code, r.UserAgent(), ip); err != nil {
			rm.Gate.Fail(ip)
			q.Set("e", "code") // 配对页据此说一句「码不对或者过期了」
		} else {
			rm.Gate.Reset(ip)
			rm.Auth.IssueCookie(w, token)
		}
	}
	// 只回到根路径：把 r.URL.Path 原样回填进 Location 的话，`//evil.com?pair=x` 就是一个开放重定向
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, (&url.URL{Path: "/", RawQuery: q.Encode()}).String(), http.StatusFound)
}

// gateCheck 在每次猜凭据之前过一下闸：熔断 / 封锁中直接 429；同一个 IP 已经有一次在验、或者全局在验的满了，
// 也 429（并发请求绕过退避的那个洞，见 auth.Gate.Enter）；否则先睡一会儿（拖慢在线猜解）。
// 放行时返回 release，验完（成功失败都一样）要调。
func (s *Server) gateCheck(w http.ResponseWriter, r *http.Request) (release func(), ok bool) {
	g := s.Remote.Gate
	locked := func() bool {
		if !g.Locked() {
			return false
		}
		writeJSONStatus(w, http.StatusTooManyRequests, map[string]any{"error": "失败次数太多，已暂停接受新设备。在跑 gerdr 的机器上执行 gerdr remote unlock"})
		return true
	}
	busy := func() {
		w.Header().Set("Retry-After", "2")
		writeJSONStatus(w, http.StatusTooManyRequests, map[string]any{"error": "太快了，等一下再试"})
	}
	if locked() {
		return nil, false
	}
	ip := auth.ClientIP(r)
	if !g.Enter(ip) {
		busy()
		return nil, false
	}
	delay, blocked, retry := g.Check(ip)
	if blocked {
		g.Leave(ip)
		w.Header().Set("Retry-After", itoa(int(retry.Seconds())+1))
		writeJSONStatus(w, http.StatusTooManyRequests, map[string]any{"error": "试错太多次了，" + retry.Round(time.Minute).String() + " 后再试"})
		return nil, false
	}
	if delay > 0 {
		time.Sleep(delay)
	}
	// 睡的这一会儿别人可能已经把全局熔断打跳了
	if locked() {
		g.Leave(ip)
		return nil, false
	}
	if !g.Slot() {
		g.Leave(ip)
		busy()
		return nil, false
	}
	return func() { g.Unslot(); g.Leave(ip) }, true
}

//go:embed pairpage.html
var pairPage string

func (s *Server) servePairPage(w http.ResponseWriter, r *http.Request) {
	nonce := newNonce()
	w.Header().Set("Content-Security-Policy", cspWith("'nonce-"+nonce+"'"))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(strings.ReplaceAll(pairPage, "{{NONCE}}", nonce)))
}

/* ------------------------------------------------------------------ /api/remote/* */

func (s *Server) remoteRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/remote/whoami", s.remoteWhoami)
	mux.HandleFunc("POST /api/remote/pair", s.remotePair)
	mux.HandleFunc("GET /api/remote/status", func(w http.ResponseWriter, r *http.Request) {
		st := s.Remote.Status()
		writeJSON(w, map[string]any{"status": st, "remote": auth.FromRemote(r)})
	})
	// 出配对码：本机专用（remoteGuard 在远程那一侧已经拦了）。
	mux.HandleFunc("POST /api/remote/pair-code", func(w http.ResponseWriter, r *http.Request) {
		rm := s.Remote
		if !rm.Cfg.Enabled() {
			httpError(w, http.StatusConflict, "还没开远程访问：先 gerdr remote join（见设置 → 远程访问）")
			return
		}
		code, exp := rm.Auth.MintCode()
		u := rm.PairURL(code)
		out := map[string]any{"code": code, "expires": exp, "url": u}
		if png, err := qrcode.Encode(u, qrcode.Medium, 320); err == nil {
			out["qr"] = "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
		}
		writeJSON(w, out)
	})
	// 证书来源（DNS 服务商 + 凭据，或者两个文件）：读的时候凭据只给名字；改了不用重启，远程那条路当场重来。
	mux.HandleFunc("GET /api/remote/cert", func(w http.ResponseWriter, r *http.Request) {
		c, err := s.Remote.CertConfig()
		if err != nil {
			httpError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, c)
	})
	mux.HandleFunc("POST /api/remote/cert", func(w http.ResponseWriter, r *http.Request) {
		var in remote.CertInput
		if err := decode(r, &in); err != nil {
			httpError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := s.Remote.SaveCert(in); err != nil {
			httpError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	})
	mux.HandleFunc("POST /api/remote/unlock", func(w http.ResponseWriter, r *http.Request) {
		s.Remote.Gate.Unlock()
		writeJSON(w, map[string]any{"ok": true})
	})
	mux.HandleFunc("POST /api/remote/logout", func(w http.ResponseWriter, r *http.Request) {
		// 退出就是撤销这台设备，不留半截凭据。本机那一侧没有设备可退。
		if id := s.Remote.Auth.Authenticate(r); id != nil && auth.FromRemote(r) {
			s.Remote.Auth.Revoke(id.Device.ID)
			s.Remote.Auth.ClearCookie(w)
		}
		writeJSON(w, map[string]any{"ok": true})
	})
	mux.HandleFunc("GET /api/remote/connections", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, s.remoteConns.list())
	})
	mux.HandleFunc("GET /api/remote/devices", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"devices": s.Remote.Auth.Devices(), "me": s.myDevice(r)})
	})
	mux.HandleFunc("DELETE /api/remote/devices", func(w http.ResponseWriter, r *http.Request) {
		// passkey 一起清：只清设备的话，任何一把还留着的 passkey 都能立刻换回一份新凭据
		// （passkey 登录那条口按设计不要求认证）—— 见 auth.RevokeEverything。
		n, keys := auth.RevokeEverything(s.Remote.Auth, s.Remote.Passkeys)
		if auth.FromRemote(r) {
			s.Remote.Auth.ClearCookie(w) // 包括自己
		}
		writeJSON(w, map[string]any{"revoked": n, "passkeys": keys})
	})
	mux.HandleFunc("DELETE /api/remote/devices/{id}", func(w http.ResponseWriter, r *http.Request) {
		label, ok := s.Remote.Auth.Revoke(r.PathValue("id"))
		if !ok {
			httpError(w, 404, "没有这台设备")
			return
		}
		if s.myDevice(r) == r.PathValue("id") {
			s.Remote.Auth.ClearCookie(w)
		}
		writeJSON(w, map[string]any{"revoked": 1, "label": label})
	})
	mux.HandleFunc("GET /api/remote/passkeys", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"passkeys": s.Remote.Passkeys.List(), "available": s.passkeyOK(r), "url": s.Remote.URL()})
	})
	mux.HandleFunc("DELETE /api/remote/passkeys/{id}", func(w http.ResponseWriter, r *http.Request) {
		label, ok := s.Remote.Passkeys.Delete(r.PathValue("id"))
		if !ok {
			httpError(w, 404, "没有这把 passkey")
			return
		}
		writeJSON(w, map[string]any{"deleted": 1, "label": label})
	})
	mux.HandleFunc("POST /api/remote/passkey/{action}/{step}", s.remotePasskey)

	// 到电脑上批准（auth/approve.go）：远程设备发起 / 查状态；本机列出 / 批准（本机专用，remoteGuard 拦着远程）。
	mux.HandleFunc("POST /api/remote/stepup/request", func(w http.ResponseWriter, r *http.Request) {
		id := s.Remote.Auth.Authenticate(r)
		if id == nil || !auth.FromRemote(r) {
			httpError(w, http.StatusUnauthorized, "这台设备还没配对")
			return
		}
		a := s.Remote.Approvals.Request(id.Device.ID, id.Label)
		// 推给开着的界面：本机那边弹通知 / toast（sync.ts 的 "stepup"），不然人不知道要回电脑前点
		if s.Hub != nil {
			s.Hub.Broadcast("stepup", map[string]any{"label": id.Label, "code": a.Code})
		}
		writeJSON(w, map[string]any{"id": a.ID, "code": a.Code, "expires": a.Expires})
	})
	mux.HandleFunc("GET /api/remote/stepup/request/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := s.Remote.Auth.Authenticate(r)
		if id == nil {
			httpError(w, http.StatusUnauthorized, "这台设备还没配对")
			return
		}
		writeJSON(w, map[string]any{"state": s.Remote.Approvals.State(r.PathValue("id"), id.Device.ID)})
	})
	mux.HandleFunc("GET /api/remote/stepup/pending", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"requests": s.Remote.Approvals.Pending()})
	})
	mux.HandleFunc("POST /api/remote/stepup/approve/{id}", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			OK bool `json:"ok"`
		}
		if err := decode(r, &b); err != nil {
			httpError(w, http.StatusBadRequest, err.Error())
			return
		}
		dev, found := s.Remote.Approvals.Decide(r.PathValue("id"), b.OK)
		if !found {
			httpError(w, http.StatusNotFound, "这个请求已经过期了")
			return
		}
		if b.OK {
			s.Remote.Auth.GrantStepUp(dev)
		}
		writeJSON(w, map[string]any{"ok": true})
	})
}

func (s *Server) myDevice(r *http.Request) string {
	if !auth.FromRemote(r) {
		return ""
	}
	if id := s.Remote.Auth.Authenticate(r); id != nil {
		return id.Device.ID
	}
	return ""
}

func (s *Server) remoteWhoami(w http.ResponseWriter, r *http.Request) {
	rm := s.Remote
	out := map[string]any{
		"remote":   auth.FromRemote(r),
		"passkeys": rm.Passkeys.Count(),
		// 配对页据此决定要不要显示「用 passkey 登录」。**按当前 origin 算**，见 passkeyOK
		"passkeyAvailable": s.passkeyOK(r),
		"ttlDays":          int(rm.Cfg.DeviceTTL / (24 * time.Hour)),
	}
	if !auth.FromRemote(r) {
		out["authed"] = true // 走到这儿说明令牌那道已经过了
		writeJSON(w, out)
		return
	}
	id := rm.Auth.Authenticate(r)
	out["authed"] = id != nil && !s.reauthNeeded(id)
	if id != nil {
		out["label"], out["deviceId"] = id.Label, id.Device.ID
		if s.reauthNeeded(id) {
			out["need"] = "passkey"
		}
		// 每次开页面顺手把 cookie 的 Max-Age 续上（浏览器那边最长只认 400 天）
		if c, err := r.Cookie(auth.CookieName); err == nil {
			rm.Auth.IssueCookie(w, c.Value)
		}
	}
	writeJSON(w, out)
}

// remotePair：手输配对码（扫不了码的设备，或者二维码过期了）。只在远程那一侧有意义。
func (s *Server) remotePair(w http.ResponseWriter, r *http.Request) {
	rm := s.Remote
	if !auth.FromRemote(r) {
		httpError(w, http.StatusConflict, "本机不用配对")
		return
	}
	var b struct{ Code string }
	if err := decode(r, &b); err != nil {
		httpError(w, 400, err.Error())
		return
	}
	release, ok := s.gateCheck(w, r)
	if !ok {
		return
	}
	defer release()
	ip := auth.ClientIP(r)
	dev, token, err := rm.Auth.Redeem(b.Code, r.UserAgent(), ip)
	if err != nil {
		rm.Gate.Fail(ip)
		httpError(w, http.StatusUnauthorized, err.Error())
		return
	}
	rm.Gate.Reset(ip)
	rm.Auth.IssueCookie(w, token)
	writeJSON(w, map[string]any{"ok": true, "label": dev.Label})
}

/* ------------------------------------------------------------------ passkey */

// passkeyOK 这个请求所在的 origin 上能不能做 passkey。**按请求算**：本机口（http://127.0.0.1）
// 和远程口（https://主机名）是两个 origin，WebAuthn 只认 RPID 那一个。见 auth.UsableOn。
func (s *Server) passkeyOK(r *http.Request) bool {
	return auth.FromRemote(r) && s.Remote.Passkeys.Available() && auth.UsableOn(s.Remote.Cfg.Host, r.Host)
}

// stepUpWindow：再注册一把 passkey 之前，那次生物验证的新鲜度要求。
const stepUpWindow = 5 * time.Minute

func (s *Server) reauthNeeded(id *auth.Ident) bool {
	return auth.ReauthNeeded(id, s.Remote.Cfg.ReauthAfter, s.Remote.Passkeys.Count(), time.Now())
}

func (s *Server) remotePasskey(w http.ResponseWriter, r *http.Request) {
	rm := s.Remote
	action, step := r.PathValue("action"), r.PathValue("step")
	if !s.passkeyOK(r) {
		// 本机口上直接拒，别让 WebAuthn 库在后面报一个看不懂的 origin 错
		where := "用远程地址打开"
		if u := rm.URL(); u != "" {
			where = "换 " + u + " 打开"
		}
		httpError(w, http.StatusConflict, "这个地址上用不了 passkey（它绑在远程访问的域名上）："+where)
		return
	}
	switch action + "/" + step {
	case "register/begin", "register/finish":
		id := rm.Auth.Authenticate(r)
		if id == nil {
			httpError(w, http.StatusUnauthorized, "这台设备还没配对")
			return
		}
		// begin 和 finish **两处都要挡**：光挡 begin 的话，自己拼一个 finish 请求就绕过去了。
		// 用 403 + reason=stepup 不用 401：401 会把整个界面弹回登录屏，这儿只是「再验一下指纹」。
		if auth.StepUpNeeded(id, stepUpWindow, rm.Passkeys.Count(), time.Now()) {
			writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "再加一把 passkey 之前，先用已有的那把验一次", "reason": "stepup"})
			return
		}
		if step == "begin" {
			opts, ceremony, err := rm.Passkeys.BeginRegister()
			if err != nil {
				httpError(w, 400, err.Error())
				return
			}
			writeJSON(w, map[string]any{"options": opts, "ceremony": ceremony})
			return
		}
		k, err := rm.Passkeys.FinishRegister(r.URL.Query().Get("c"), r.UserAgent(), r)
		if err != nil {
			httpError(w, 400, err.Error())
			return
		}
		// 刚做过生物验证，顺手给当前设备续上 —— 否则注册完这一刻就会被要求重验
		rm.Auth.MarkVerified(id.Device.ID)
		writeJSON(w, map[string]any{"ok": true, "id": k.ID, "label": k.Label})

	case "login/begin":
		release, ok := s.gateCheck(w, r)
		if !ok {
			return
		}
		release() // begin 只是发个挑战，不验东西：过一下闸（封锁中就别给）就放
		opts, ceremony, err := rm.Passkeys.BeginLogin()
		if err != nil {
			httpError(w, 400, err.Error())
			return
		}
		writeJSON(w, map[string]any{"options": opts, "ceremony": ceremony})

	case "login/finish":
		release, ok := s.gateCheck(w, r)
		if !ok {
			return
		}
		defer release()
		ip := auth.ClientIP(r)
		k, err := rm.Passkeys.FinishLogin(r.URL.Query().Get("c"), r)
		if err != nil {
			rm.Gate.Fail(ip)
			httpError(w, http.StatusUnauthorized, err.Error())
			return
		}
		rm.Gate.Reset(ip)
		// 已经是配过对的设备：只是刷新「上次验证」时间，不新建设备
		if id := rm.Auth.Authenticate(r); id != nil {
			rm.Auth.MarkVerified(id.Device.ID)
			writeJSON(w, map[string]any{"ok": true, "label": id.Label, "passkey": k.Label})
			return
		}
		// 新设备：passkey 直接换一份设备凭据，不用回机器前拿配对码
		dev, token := rm.Auth.NewDevice(r.UserAgent(), ip, "passkey")
		rm.Auth.MarkVerified(dev.ID)
		rm.Auth.IssueCookie(w, token)
		writeJSON(w, map[string]any{"ok": true, "label": dev.Label, "passkey": k.Label})

	default:
		httpError(w, 404, "没有这个接口")
	}
}

func writeJSONStatus(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
