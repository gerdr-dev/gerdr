package server

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/gerdr-dev/gerdr/internal/i18n"
	"github.com/gerdr-dev/gerdr/internal/uisite"
	"github.com/gerdr-dev/gerdr/internal/webui"
)

// uiRoutes: the status and error reports of the UI site (the rescue panel, `gerdr ui status`).
func (s *Server) uiRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/ui/status", func(w http.ResponseWriter, r *http.Request) {
		fatal, issues := s.UI.Status()
		writeJSON(w, map[string]any{"dir": s.UI.Dir(), "build_error": fatal, "warnings": issues, "runtime_errors": s.UI.ClientErrors(), "git": webui.Status(s.UI.Dir())})
	})
	// Settings → Labs → Production React: get its packages ready before the page reloads onto them (uisite/prod.go)
	mux.HandleFunc("POST /api/ui/prod-react", func(w http.ResponseWriter, r *http.Request) {
		st := s.UI.PrepareProd(60 * time.Second)
		writeJSON(w, map[string]any{"ready": st == "", "state": st})
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
	// The UI reports something it saw take too long (an Esc whose words came back late…) into gerdr.log, next to the
	// backend's own timing of the same step: a slowness that only happens on the user's own machine (and doesn't
	// reproduce on a copy) can then be split into server, network and a busy page. Only numbers, a dozen at most.
	mux.HandleFunc("POST /api/ui/slow", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			What    string             `json:"what"`
			Session string             `json:"session"`
			Ms      map[string]float64 `json:"ms"`
		}
		if err := decode(r, &b); err != nil {
			httpError(w, 400, err.Error())
			return
		}
		args := []any{"what", oneLineCap(b.What, 40), "session", oneLineCap(b.Session, 80)}
		keys := make([]string, 0, len(b.Ms))
		for k := range b.Ms {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for i, k := range keys {
			if i == 12 {
				break
			}
			args = append(args, oneLineCap(k, 24), int64(b.Ms[k]))
		}
		s.Log.Warn("ui slow", args...)
		writeJSON(w, map[string]any{})
	})
	mux.HandleFunc("DELETE /api/ui/errors", func(w http.ResponseWriter, r *http.Request) {
		s.Log.Info("ui errors dismissed in the rescue panel", "count", len(s.UI.ClientErrors()))
		s.UI.ClearClientErrors()
		writeJSON(w, map[string]any{})
	})
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
			httpError(w, 404, i18n.S("没有这个接口（Gerdr 版本旧了？）", "No such endpoint (is Gerdr out of date?)"))
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

// media-src blob:: a video picked in the composer previews (thumbnail, lightbox) from an object URL while it uploads,
// and on phones until the message is sent; without it media fell back to default-src and the video never loaded.
// https: same as img-src: an agent's reply can embed a video or audio file by its web address.
func cspWith(extraScript string) string {
	return "default-src 'self'; script-src 'self' " + extraScript + " blob: 'wasm-unsafe-eval'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob: https:; media-src 'self' blob: https:; font-src 'self' data:; connect-src 'self'; worker-src 'self' blob:; frame-ancestors 'none'"
}

func oneLineCap(t string, n int) string {
	t = strings.Join(strings.Fields(t), " ")
	if r := []rune(t); len(r) > n {
		t = string(r[:n])
	}
	return t
}
