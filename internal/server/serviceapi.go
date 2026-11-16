package server

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gerdr-dev/gerdr/internal/i18n"
	"github.com/gerdr-dev/gerdr/internal/version"
)

// 后台常驻（launchd，见 internal/service）：设置里那个开关。只在本机口上（remoteGuard 拦着）——
// 远程能关掉后台服务的话，手机上点一下就把自己连着的那个 gerdr 停了。

func (s *Server) serviceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/service", func(w http.ResponseWriter, r *http.Request) {
		m := s.Service
		writeJSON(w, map[string]any{"why": m.Why(), "installed": m.Installed(), "pid": m.PID(), "self": m.Self, "log": m.LogPath()})
	})
	mux.HandleFunc("POST /api/service", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			On bool `json:"on"`
		}
		if err := decode(r, &b); err != nil {
			httpError(w, http.StatusBadRequest, err.Error())
			return
		}
		m := s.Service
		if err := m.SetOptedOut(!b.On); err != nil {
			httpError(w, http.StatusInternalServerError, err.Error())
			return
		}
		switch {
		case b.On && m.Installed() && m.PID() > 0:
		case b.On:
			// 装上之后服务马上起来，拿不到锁（本进程占着）就等着。开了就**始终由后台跑**：本进程是 App 起的后端的话
			// 当场让位（先把响应发出去），App 重起一个只连后台的后端。终端里起的 gerdr 不让位，它退出时后台接手。
			if err := m.Install(); err != nil {
				httpError(w, http.StatusInternalServerError, err.Error())
				return
			}
			if s.OnServiceInstalled != nil {
				writeJSON(w, map[string]any{"ok": true, "handover": true})
				go func() {
					time.Sleep(300 * time.Millisecond)
					s.OnServiceInstalled()
				}()
				return
			}
		case m.Self:
			// 关的就是本进程：先把响应发出去，再让 launchd 停掉我们。App 那边等着接班的后端随即拿到锁接上
			writeJSON(w, map[string]any{"ok": true})
			go func() {
				time.Sleep(300 * time.Millisecond)
				_ = m.Uninstall()
			}()
			return
		default:
			if err := m.Uninstall(); err != nil {
				httpError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		writeJSON(w, map[string]any{"ok": true})
	})
}

// restartRoutes: not under serviceRoutes, which only exist with the launchd service around: the App's own backend
// (background service off) can be restarted too.
func (s *Server) restartRoutes(mux *http.ServeMux) {
	// Settings → Versions → "Restart Gerdr": the only way to restart the backend from the UI (the App only swaps in a
	// new one by itself when nothing is running). busy is what the restart cuts off, for the confirmation.
	mux.HandleFunc("GET /api/restart", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"can": s.Restart != nil, "busy": s.busy()})
	})
	// The App's watcher (cmd/gerdr/attach.go) is about to restart this backend for an update: windows say "restarting
	// to update" instead of "connection lost, retrying in N s", which read as the network being down.
	mux.HandleFunc("POST /api/restarting", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Reason string `json:"reason"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Reason != "update" {
			body.Reason = "restart"
		}
		s.announceRestart(body.Reason)
		writeJSON(w, map[string]any{"ok": true})
	})
	mux.HandleFunc("POST /api/restart", func(w http.ResponseWriter, r *http.Request) {
		if s.Restart == nil {
			httpError(w, http.StatusConflict, i18n.S("这个 Gerdr 是在终端里起的，没人会把它重新拉起来：回那个终端里重启它", "This Gerdr was started in a terminal and nothing would start it again: restart it in that terminal"))
			return
		}
		s.announceRestart("restart")
		writeJSON(w, map[string]any{"ok": true})
		// The answer goes out first, then the shutdown (same as handing over to the service)
		go func() {
			time.Sleep(300 * time.Millisecond)
			s.Restart()
		}()
	})
}

// announceRestart tells every window this backend is about to go down on purpose (reason "update" or "restart"),
// with the version going away so a window outside the App can say "updated to" once the new one answers. Waits a
// moment so /ws (which batches for 30ms) has sent it before the process is stopped.
func (s *Server) announceRestart(reason string) {
	if s.Hub == nil {
		return
	}
	s.Hub.Broadcast("restarting", map[string]string{"reason": reason, "version": version.Version})
	time.Sleep(150 * time.Millisecond)
}
