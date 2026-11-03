package server

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gerdr-dev/gerdr/internal/screen"
)

// Settings → Labs → Remote screen (internal/screen): a screenshot of this computer and clicks / keys sent to it.
// Off unless config.remoteScreen is on. Remote devices may use it: that is the point, and a paired device can already
// run any command in a terminal, so this adds no power it didn't have. Asking macOS for the grants is local only
// (remoteGuard): its prompts show on this computer's screen.
func (s *Server) screenRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/screen", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, s.screenStatus(screen.GetStatus()))
	})
	mux.HandleFunc("GET /api/screen/shot", func(w http.ResponseWriter, r *http.Request) {
		if !s.screenOn() {
			httpError(w, http.StatusForbidden, "remote screen is off (Settings → Labs)")
			return
		}
		maxW, _ := strconv.Atoi(r.URL.Query().Get("w"))
		if maxW <= 0 || maxW > 4000 {
			maxW = 1600
		}
		// q: JPEG quality, the UI's Data saver / Standard / Sharp (45 / 60 / 75)
		q, _ := strconv.Atoi(r.URL.Query().Get("q"))
		if q <= 0 || q > 100 {
			q = 60
		}
		b, err := screen.Capture(maxW, q)
		if err != nil {
			httpError(w, http.StatusConflict, err.Error())
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(b)
	})
	mux.HandleFunc("POST /api/screen/input", func(w http.ResponseWriter, r *http.Request) {
		if !s.screenOn() {
			httpError(w, http.StatusForbidden, "remote screen is off (Settings → Labs)")
			return
		}
		var in screen.Input
		if err := decode(r, &in); err != nil {
			httpError(w, 400, err.Error())
			return
		}
		// Never log the input: typed text is often a password.
		if err := screen.Do(in); err != nil {
			httpError(w, http.StatusConflict, err.Error())
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	})
	mux.HandleFunc("POST /api/screen/request-access", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, s.screenStatus(screen.RequestAccess()))
	})
}

type screenStatus struct {
	screen.Status
	Enabled bool `json:"enabled"`
}

func (s *Server) screenStatus(st screen.Status) screenStatus {
	return screenStatus{Status: st, Enabled: s.screenOn()}
}

// screenOn reads the Labs switch from config.json at each request: switching it off takes effect at once.
func (s *Server) screenOn() bool {
	var c struct {
		RemoteScreen bool `json:"remoteScreen"`
	}
	_ = json.Unmarshal(s.Config.Get(), &c)
	return c.RemoteScreen
}
