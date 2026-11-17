package server

import (
	"net/http"

	"github.com/gorilla/websocket"

	"github.com/gerdr-dev/gerdr/internal/chromeext"
	"github.com/gerdr-dev/gerdr/internal/i18n"
)

// /api/browser: the user's own Chrome through Gerdr's browser extension (internal/chromeext,
// docs/dev/CHROME-EXT.md), for `gerdr browser`. Same reach as /api/exec: whoever can run commands here could drive
// the extension anyway; the extension's own limits (allowed sites, its tabs only) hold either way.
func (s *Server) browserRoutes(mux *http.ServeMux) {
	br := func(w http.ResponseWriter) *chromeext.Bridge {
		if s.Browser == nil {
			httpError(w, http.StatusNotFound, i18n.S("这个 Gerdr 没开浏览器桥", "The browser bridge is off in this Gerdr"))
		}
		return s.Browser
	}
	// the native host (the gerdr program Chrome starts for the extension, chromeext.RunHost) connects here with the
	// CLI's token; a web page can't, it can't set the Authorization header on a WebSocket
	mux.HandleFunc("GET /api/browser/ext", func(w http.ResponseWriter, r *http.Request) {
		b := br(w)
		if b == nil {
			return
		}
		ws, err := browserUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		b.Serve(r.Context(), ws)
	})
	mux.HandleFunc("GET /api/browser", func(w http.ResponseWriter, r *http.Request) {
		b := br(w)
		if b == nil {
			return
		}
		writeJSON(w, map[string]any{"profiles": b.Profiles(), "tabs": b.Tabs()})
	})
	mux.HandleFunc("POST /api/browser/open", func(w http.ResponseWriter, r *http.Request) {
		b := br(w)
		if b == nil {
			return
		}
		var in struct {
			URL     string `json:"url"`
			Profile string `json:"profile"`
			Why     string `json:"why"`
			Focused bool   `json:"focused"`
			Width   int    `json:"width"`
			Height  int    `json:"height"`
		}
		if err := decode(r, &in); err != nil {
			httpError(w, http.StatusBadRequest, err.Error())
			return
		}
		t, err := b.Open(r.Context(), chromeext.OpenOptions{URL: in.URL, Profile: in.Profile, Why: in.Why, Focused: in.Focused, Width: in.Width, Height: in.Height})
		if err != nil {
			httpError(w, http.StatusBadGateway, err.Error())
			return
		}
		// wait for the first load; a page that never settles is still open and usable
		b.Act(r.Context(), t.ID, "wait", chromeext.ActArgs{Load: true, Timeout: 30000})
		writeJSON(w, t)
	})
	mux.HandleFunc("POST /api/browser/close", func(w http.ResponseWriter, r *http.Request) {
		b := br(w)
		if b == nil {
			return
		}
		var in struct {
			Tab  int  `json:"tab"`
			Keep bool `json:"keep"`
		}
		if err := decode(r, &in); err != nil {
			httpError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := b.Close(in.Tab, in.Keep); err != nil {
			httpError(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	})
	// one action in a window (chromeext.Act): {tab (0 = the one opened last), action, args}
	mux.HandleFunc("POST /api/browser/act", func(w http.ResponseWriter, r *http.Request) {
		b := br(w)
		if b == nil {
			return
		}
		var in struct {
			Tab    int               `json:"tab"`
			Action string            `json:"action"`
			Args   chromeext.ActArgs `json:"args"`
		}
		if err := decode(r, &in); err != nil {
			httpError(w, http.StatusBadRequest, err.Error())
			return
		}
		res, err := b.Act(r.Context(), in.Tab, in.Action, in.Args)
		if err != nil {
			httpError(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, res)
	})
}

var browserUpgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return r.Header.Get("Origin") == "" }}
