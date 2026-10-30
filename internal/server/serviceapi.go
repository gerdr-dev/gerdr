package server

import (
	"net/http"
	"time"
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
