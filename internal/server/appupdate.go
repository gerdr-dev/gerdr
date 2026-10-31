package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/gerdr-dev/gerdr/internal/auth"
)

// Settings → 版本 on a device without the desktop App's shell (a phone over remote access, a plain browser).
// The App's self-update lives in the shell (desktop/updater.js), which only talks to its own windows. Rather than
// teach the shell a new channel (any change to its files makes the next release a full App download for everyone,
// docs/dev/UPDATE.md), a page inside the App relays: it PUTs the status the shell gives it here, and picks up the
// buttons pressed elsewhere from the "app_update_op" broadcast, addressed to it by client id, and hands them to
// the shell. So it works while the App has a window open, and says so when it hasn't.
//
//	PUT  /api/app-update      {client, status}  from the App's page, local door only
//	GET  /api/app-update      → {status, client} (status null when no App window reported lately)
//	POST /api/app-update/op   {op: check|apply|auto, on}  → broadcast to the reporting page

// appUpdateFresh: the App's page re-reports every 30s; past this the window is taken to be gone.
const appUpdateFresh = 90 * time.Second

type appUpdate struct {
	mu     sync.Mutex
	status json.RawMessage
	client string
	at     time.Time
}

func (a *appUpdate) get(now time.Time) (json.RawMessage, string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.status == nil || now.Sub(a.at) > appUpdateFresh {
		return nil, ""
	}
	return a.status, a.client
}

func (s *Server) appUpdateRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/app-update", func(w http.ResponseWriter, r *http.Request) {
		st, client := s.appUpdate.get(time.Now())
		writeJSON(w, map[string]any{"status": st, "client": client})
	})
	mux.HandleFunc("PUT /api/app-update", func(w http.ResponseWriter, r *http.Request) {
		// The App is always on this machine: a remote device reporting an App status would only be lying
		if auth.FromRemote(r) {
			httpError(w, http.StatusForbidden, "只有电脑上的 App 能报更新状态")
			return
		}
		var b struct {
			Client string          `json:"client"`
			Status json.RawMessage `json:"status"`
		}
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil || b.Client == "" || len(b.Status) == 0 || b.Status[0] != '{' {
			httpError(w, 400, "要 client 和 status")
			return
		}
		a := &s.appUpdate
		a.mu.Lock()
		changed := a.client != b.Client || !bytes.Equal(a.status, b.Status) || time.Since(a.at) > appUpdateFresh
		a.status, a.client, a.at = b.Status, b.Client, time.Now()
		a.mu.Unlock()
		if changed && s.Hub != nil {
			s.Hub.Broadcast("app_update", map[string]any{"status": b.Status, "client": b.Client})
		}
		writeJSON(w, map[string]any{})
	})
	mux.HandleFunc("POST /api/app-update/op", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Op string `json:"op"`
			On bool   `json:"on"`
		}
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
			httpError(w, 400, err.Error())
			return
		}
		if b.Op != "check" && b.Op != "apply" && b.Op != "auto" {
			httpError(w, 400, "op 只能是 check / apply / auto")
			return
		}
		_, client := s.appUpdate.get(time.Now())
		if client == "" {
			httpError(w, http.StatusConflict, "电脑上的 Gerdr App 没开着窗口，没法检查更新")
			return
		}
		// Applying while busy makes the shell ask in a dialog on the computer, which nobody at a phone can answer.
		// With automatic updates on it goes ahead by itself once idle anyway.
		if b.Op == "apply" && s.Hub != nil {
			if x := s.busy(); x.Turns+x.Waiting+x.Tasks+x.Terms > 0 {
				httpError(w, http.StatusConflict, "还有 agent 在跑或终端开着，现在更新会打断它们：等空闲了再点")
				return
			}
		}
		if s.Hub != nil {
			s.Hub.Broadcast("app_update_op", map[string]any{"client": client, "op": b.Op, "on": b.On})
		}
		writeJSON(w, map[string]any{})
	})
}
