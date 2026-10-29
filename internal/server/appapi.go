package server

import (
	"encoding/json"
	"net"
	"net/http"
	"time"

	"github.com/gerdr-dev/gerdr/internal/auth"
	"github.com/gerdr-dev/gerdr/internal/config"
)

// 安卓 App（android/）要的那几个口。设计见 docs/dev/ANDROID.md：
//
//	GET  /.well-known/assetlinks.json   认 App 的签名证书 → App 的 WebView 里能用这个域名的 passkey
//	GET  /api/remote/lan                 局域网直连的候选地址（页面拿去递给 App 的壳）
//	POST /api/remote/cli/*               passkey 的退路：在浏览器里批准（device flow，auth/cliapprove.go）
//	GET  /api/app/notices | agents       后台通知的长轮询（appwatch.go）

func (s *Server) appRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /.well-known/assetlinks.json", s.assetLinks)
	mux.HandleFunc("GET /api/remote/lan", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"origins": s.Remote.LanOrigins()})
	})
	mux.HandleFunc("POST /api/remote/cli/{op}", s.apiCLI)
	mux.HandleFunc("GET /api/app/notices", s.appNotices)
	mux.HandleFunc("GET /api/app/agents", s.appAgents)
}

// assetLinks：Digital Asset Links。安卓的 Credential Manager 在 App 里（包括 App 的 WebView）给某个域名刷 passkey 之前，
// 先来这儿确认「这个域名认这个 App」—— 认的是**签名证书**，不是包名：别人改个包名重签，这里对不上就刷不了。
//
// 一个上架的通用 App 本来绑不了每个用户自己的域名（herdr-web 当初因此只能开浏览器批准）。但 gerdr 自己就是这个域名的
// 服务器，自己发这份文件就行。证书列表在 config.Remote.AndroidCerts（官方那把 + 自己加的 debug 钥匙）。
//
// 没认证也给（remotePublic）：系统来取它时不带任何凭据；里面只有公开的证书指纹。
func (s *Server) assetLinks(w http.ResponseWriter, r *http.Request) {
	if s.Remote == nil || !s.Remote.Cfg.Enabled() {
		http.NotFound(w, r)
		return
	}
	cfg := s.Remote.Cfg
	type target struct {
		Namespace   string   `json:"namespace"`
		Site        string   `json:"site,omitempty"`
		PackageName string   `json:"package_name,omitempty"`
		Certs       []string `json:"sha256_cert_fingerprints,omitempty"`
	}
	type stmt struct {
		Relation []string `json:"relation"`
		Target   target   `json:"target"`
	}
	out := []stmt{{
		Relation: []string{"delegate_permission/common.get_login_creds"},
		Target:   target{Namespace: "android_app", PackageName: config.AndroidPackage, Certs: cfg.AndroidCerts},
	}}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "max-age=3600")
	_ = json.NewEncoder(w).Encode(out)
}

/* ------------------------------------------------------------------ device flow */

// apiCLI：passkey 的退路（App 的 WebView 刷不了 passkey 时）。流程和为什么第一次登录也能走这条，见 auth/cliapprove.go。
//
//	start    发起方（App）。不要求认证；带着设备 cookie 来 = 给这台设备重验
//	poll     发起方拿轮询令牌来问
//	peek     批准方（已认证）：输了显示码，先看一眼是谁
//	approve  批准方：远程那条路上要**刚过了一把 passkey**（403 reason=stepup 让前端去刷）；本机口凭令牌
//	deny     批准方：拒绝
func (s *Server) apiCLI(w http.ResponseWriter, r *http.Request) {
	rm := s.Remote
	if rm == nil || !rm.Cfg.Enabled() {
		httpError(w, http.StatusConflict, "还没开远程访问")
		return
	}
	switch op := r.PathValue("op"); op {
	case "start":
		if !auth.FromRemote(r) {
			httpError(w, http.StatusConflict, "本机不用登录")
			return
		}
		// 一把 passkey 都没有：远程那边没人批得了（本机口能批，但人多半不在电脑前）—— 让 App 直接说「用配对码」
		if rm.Passkeys.Count() == 0 {
			writeJSONStatus(w, http.StatusConflict, map[string]any{"error": "这台 gerdr 还没注册 passkey，只能用配对码", "reason": "nopasskey"})
			return
		}
		device := ""
		if _, err := r.Cookie(auth.CookieName); err == nil {
			id := rm.Auth.Authenticate(r)
			if id == nil {
				httpError(w, http.StatusUnauthorized, "这份凭据不认了（被撤销或过期了）")
				return
			}
			device = id.Device.ID
		}
		ip := auth.ClientIP(r)
		req, poll, err := rm.Auth.StartCLI(r.UserAgent(), ip, isLoopback(ip), device)
		if err != nil {
			httpError(w, http.StatusTooManyRequests, err.Error())
			return
		}
		writeJSON(w, map[string]any{"id": req.ID, "poll": poll, "code": req.Code, "expires": req.Expires, "url": rm.URL() + "/?cli"})

	case "poll":
		var b struct{ ID, Poll string }
		if err := decode(r, &b); err != nil {
			httpError(w, http.StatusBadRequest, err.Error())
			return
		}
		res, err := rm.Auth.PollCLI(b.ID, b.Poll)
		if err != nil {
			httpError(w, http.StatusGone, err.Error())
			return
		}
		out := map[string]any{"state": res.State}
		if res.Device != nil {
			out["label"], out["deviceId"] = res.Device.Label, res.Device.ID
		}
		if res.Token != "" {
			out["token"] = res.Token
		}
		writeJSON(w, out)

	case "peek", "approve", "deny":
		// 走到这儿已经过了 guard：本机口是令牌，远程是配过对、不欠重验的设备
		var b struct{ Code string }
		if err := decode(r, &b); err != nil {
			httpError(w, http.StatusBadRequest, err.Error())
			return
		}
		// 显示码只有 40 位：批准方虽然已认证，猜码也照样进限速（码错了算一次失败）
		release, ok := s.gateCheck(w, r)
		if !ok {
			return
		}
		defer release()
		ip := auth.ClientIP(r)
		var req auth.CLIRequest
		var err error
		switch op {
		case "peek":
			req, err = rm.Auth.PeekCLI(b.Code)
		case "deny":
			err = rm.Auth.DenyCLI(b.Code)
		case "approve":
			// 先确认码是真的再要 passkey：码输错了别让人白刷一次指纹
			if req, err = rm.Auth.PeekCLI(b.Code); err == nil {
				if s.cliStepUp(w, r) {
					return
				}
				req, err = rm.Auth.ApproveCLI(b.Code)
			}
		}
		if err != nil {
			if err == auth.ErrCLIUnknown {
				rm.Gate.Fail(ip)
			}
			httpError(w, http.StatusNotFound, err.Error())
			return
		}
		rm.Gate.Reset(ip)
		writeJSON(w, req)

	default:
		httpError(w, http.StatusNotFound, "没有这个接口")
	}
}

// cliStepUp：远程批准必须是**刚过了一把 passkey** 的人 —— 光有一个登录着的浏览器不够（那正是「手机被拿去一次」）。
// 本机口不设这道：本机凭令牌就能出配对码，批准给出的能力和出码一样（auth/cliapprove.go）。挡住了返回 true，响应已写出。
func (s *Server) cliStepUp(w http.ResponseWriter, r *http.Request) bool {
	if !auth.FromRemote(r) {
		return false
	}
	if id := s.Remote.Auth.Authenticate(r); id != nil && time.Since(id.PasskeyAt) <= stepUpWindow {
		return false
	}
	writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "批准之前先用 passkey 验一次", "reason": "stepup"})
	return true
}

func isLoopback(ip string) bool {
	p := net.ParseIP(ip)
	return p != nil && p.IsLoopback()
}
