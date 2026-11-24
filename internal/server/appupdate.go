package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/gerdr-dev/gerdr/internal/auth"
	"github.com/gerdr-dev/gerdr/internal/i18n"
	"github.com/gerdr-dev/gerdr/internal/version"
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

// appUpdate keeps every App page's report, not just the last one. Several pages report at once (each window, and
// pages the shell no longer pushes to, which keep re-sending the status they last got), and taking the last writer
// made a phone's Settings → 版本 flip between them every few seconds, between versions and check times hours apart.
// So the one shown is picked: the report whose running backend is this one, then the latest check.
type appUpdate struct {
	mu      sync.Mutex
	reports map[string]appUpdateReport // by client id
	at      time.Time                  // test hook: when set, every report counts as made at this time
}

type appUpdateReport struct {
	status json.RawMessage
	at     time.Time
	// from the status, for picking
	running   string
	checkedAt int64
}

func (a *appUpdate) put(client string, status json.RawMessage, now time.Time) {
	var st struct {
		Running   string `json:"running"`
		CheckedAt int64  `json:"checkedAt"`
	}
	_ = json.Unmarshal(status, &st)
	if a.reports == nil {
		a.reports = map[string]appUpdateReport{}
	}
	a.reports[client] = appUpdateReport{status: status, at: now, running: st.Running, checkedAt: st.CheckedAt}
}

// pick: the report to show, or none when no App page reported lately; drops the gone ones. Caller holds mu.
func (a *appUpdate) pick(now time.Time) (json.RawMessage, string) {
	var best appUpdateReport
	client := ""
	for c, r := range a.reports {
		at := r.at
		if !a.at.IsZero() {
			at = a.at
		}
		if now.Sub(at) > appUpdateFresh {
			delete(a.reports, c)
			continue
		}
		if client == "" || better(r, best, c, client) {
			best, client = r, c
		}
	}
	return best.status, client
}

// better: r (from client c) is the truer report than b (from client bc). Ties go to the client id, so the pick is
// stable and doesn't wander with map order.
func better(r, b appUpdateReport, c, bc string) bool {
	if ra, ba := r.running == version.Version, b.running == version.Version; ra != ba {
		return ra
	}
	if r.checkedAt != b.checkedAt {
		return r.checkedAt > b.checkedAt
	}
	return c < bc
}

func (a *appUpdate) get(now time.Time) (json.RawMessage, string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.pick(now)
}

func (s *Server) appUpdateRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/app-update", func(w http.ResponseWriter, r *http.Request) {
		st, client := s.appUpdate.get(time.Now())
		writeJSON(w, map[string]any{"status": st, "client": client})
	})
	mux.HandleFunc("PUT /api/app-update", func(w http.ResponseWriter, r *http.Request) {
		// The App is always on this machine: a remote device reporting an App status would only be lying
		if auth.FromRemote(r) {
			httpError(w, http.StatusForbidden, i18n.S("只有电脑上的 App 能报更新状态", "Only the desktop app can report update status"))
			return
		}
		var b struct {
			Client string          `json:"client"`
			Status json.RawMessage `json:"status"`
		}
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil || b.Client == "" || len(b.Status) == 0 || b.Status[0] != '{' {
			httpError(w, 400, i18n.S("要 client 和 status", "client and status are required"))
			return
		}
		a := &s.appUpdate
		a.mu.Lock()
		before, beforeClient := a.pick(time.Now())
		a.put(b.Client, b.Status, time.Now())
		st, client := a.pick(time.Now())
		a.mu.Unlock()
		if (client != beforeClient || !bytes.Equal(st, before)) && s.Hub != nil {
			s.Hub.Broadcast("app_update", map[string]any{"status": st, "client": client})
		}
		writeJSON(w, map[string]any{})
	})
	mux.HandleFunc("POST /api/app-update/op", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Op    string `json:"op"`
			On    bool   `json:"on"`
			Force bool   `json:"force"` // apply: the person agreed to cut off what is running
		}
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
			httpError(w, 400, err.Error())
			return
		}
		if b.Op != "check" && b.Op != "apply" && b.Op != "auto" {
			httpError(w, 400, i18n.S("op 只能是 check / apply / auto", "op must be check, apply or auto"))
			return
		}
		st, client := s.appUpdate.get(time.Now())
		if client == "" {
			httpError(w, http.StatusConflict, i18n.S("电脑上的 Gerdr App 没开着窗口，没法检查更新", "The Gerdr app on the computer has no window open, so it can't check for updates"))
			return
		}
		// Applying while busy makes the shell ask in a dialog on the computer, which nobody at a phone can answer. So
		// the phone asks instead: a 409 with busy (what would be cut off) makes it show its own confirmation, and a
		// confirmed click comes back with force, which the shell takes as already answered (internal/desktop/app.go).
		if b.Op == "apply" && !b.Force && s.Hub != nil && s.applyCutsOff(st) {
			if x := s.busy(); x.Turns+x.Waiting+x.Tasks+x.Terms > 0 {
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(http.StatusConflict)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"code":  "busy",
					"error": i18n.S("还有 agent 或终端里的程序在跑，现在更新会打断它们", "Agents or programs in terminals are running, and updating now would interrupt them"),
					"busy":  x,
				})
				return
			}
		}
		if s.Hub != nil {
			s.Hub.Broadcast("app_update_op", map[string]any{"client": client, "op": b.Op, "on": b.On, "force": b.Force})
		}
		writeJSON(w, map[string]any{})
	})
}

// applyCutsOff: whether applying the App's pending update would stop running agents. A new App with the background
// service on doesn't: the agents run in the service, which carries on while the windows close and reopen (the shell
// skips its own question then too, internal/desktop/app.go applyUpdate).
func (s *Server) applyCutsOff(status json.RawMessage) bool {
	var st struct {
		Pending *struct {
			Kind string `json:"kind"`
		} `json:"pending"`
	}
	_ = json.Unmarshal(status, &st)
	if st.Pending != nil && st.Pending.Kind == "app" && s.Service != nil && s.Service.Installed() {
		return false
	}
	return true
}
