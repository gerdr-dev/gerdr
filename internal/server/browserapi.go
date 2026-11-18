package server

import (
	"image"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gorilla/websocket"

	"github.com/gerdr-dev/gerdr/internal/auth"
	"github.com/gerdr-dev/gerdr/internal/chromeext"
	"github.com/gerdr-dev/gerdr/internal/i18n"
	"github.com/gerdr-dev/gerdr/internal/screen"
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
	// open a page: in the project's own browser (the Browser pane shows it), or with chrome / profile in the user's
	// Chrome through the extension. The project is the caller's (ResolveSpace) unless given.
	mux.HandleFunc("POST /api/browser/open", func(w http.ResponseWriter, r *http.Request) {
		b := br(w)
		if b == nil {
			return
		}
		var in struct {
			URL     string `json:"url"`
			Chrome  bool   `json:"chrome"`
			Profile string `json:"profile"`
			Why     string `json:"why"`
			Focused bool   `json:"focused"`
			Width   int    `json:"width"`
			Height  int    `json:"height"`
			Caller
			// NoWait: return as soon as the page is open (the pane's own address bar), not after its first load
			NoWait bool `json:"noWait"`
		}
		if err := decode(r, &in); err != nil {
			httpError(w, http.StatusBadRequest, err.Error())
			return
		}
		var id int
		if in.Chrome || in.Profile != "" || s.ProjBrowser == nil {
			t, err := b.Open(r.Context(), chromeext.OpenOptions{URL: in.URL, Profile: in.Profile, Why: in.Why, Focused: in.Focused, Width: in.Width, Height: in.Height})
			if err != nil {
				httpError(w, http.StatusBadGateway, err.Error())
				return
			}
			id = t.ID
		} else {
			project := s.browserProject(in.Caller)
			tab, err := s.ProjBrowser.Open(r.Context(), project, in.URL, in.Width, in.Height)
			if err != nil {
				httpError(w, http.StatusBadGateway, err.Error())
				return
			}
			id = tab
			// the UI puts the Browser pane beside the agent that opened it (ui/src/lib/actions/browser.ts)
			s.Hub.Broadcast("browser_open", map[string]any{"tab": id, "project": project, "session": in.Caller.Session})
		}
		// wait for the first load; a page that never settles is still open and usable
		if !in.NoWait {
			b.Act(r.Context(), id, "wait", chromeext.ActArgs{Load: true, Timeout: 30000})
		}
		for _, t := range b.Tabs() {
			if t.ID == id {
				writeJSON(w, t)
				return
			}
		}
		httpError(w, http.StatusBadGateway, i18n.S("页面刚打开就关了", "The page closed right after opening"))
	})
	// the desktop App is about to start its browser on its data directory, where projects' profiles are while it's
	// installed: the headless browser on it must let go first (tools/chromeshell, internal/projbrowser location.go)
	mux.HandleFunc("POST /api/browser/release", func(w http.ResponseWriter, r *http.Request) {
		if s.ProjBrowser != nil {
			s.ProjBrowser.Release()
		}
		writeJSON(w, map[string]any{"ok": true})
	})
	// the Browser pane: a tab's screen and input over the remote screen's stream (screenstream.go)
	mux.HandleFunc("GET /api/browser/view", func(w http.ResponseWriter, r *http.Request) {
		b := br(w)
		if b == nil {
			return
		}
		id, _ := strconv.Atoi(r.URL.Query().Get("tab"))
		v, err := b.View(id)
		if err != nil {
			httpError(w, http.StatusNotFound, err.Error())
			return
		}
		defer v.Close()
		conn, err := screenUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		// whole JPEGs to a viewer on this computer, changed tiles over the relay (screenstream.go)
		remote := auth.FromRemote(r)
		slog.Info("browser pane stream", "tab", id, "remote", remote, "query", r.URL.RawQuery)
		defer slog.Info("browser pane stream ended", "tab", id)
		streamPictures(conn, r, browserSource{v: v, remote: remote}, !remote)
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

// browserProject: the project a browser page opens in: the caller's, else the default one.
func (s *Server) browserProject(c Caller) string {
	if c.Session != "" && c.SessionCwd == "" && s.Hub != nil {
		if self, err := s.Hub.Session(c.Session); err == nil {
			c.SessionCwd = self.Summary().Cwd
		}
	}
	if p := ResolveSpace(s.Layout.Get(), c, s.workProject()); p != "" {
		return p
	}
	return DefaultSpace
}

// browserSource: a tab as the stream's picture (chromeext.View).
type browserSource struct {
	v      *chromeext.View
	remote bool // over the relay (a phone): an App page is laid out for it while the App doesn't show it
}

func (b browserSource) Grab(w int) (*image.RGBA, error)            { return b.v.Grab(w) }
func (b browserSource) Cursor() (float64, float64, bool)           { return 0, 0, false }
func (b browserSource) Do(in screen.Input) error                   { return b.v.Do(in) }
func (b browserSource) Off() string                                { return b.v.Gone() }
func (b browserSource) Changed() <-chan struct{}                   { return b.v.Changed() }
func (b browserSource) Size(w, h int)                              { b.v.SizeFrom(w, h, b.remote) }
func (b browserSource) Selection() string                          { return b.v.Selection() }
func (b browserSource) Raw(w int) ([]byte, int, int, int64, error) { return b.v.Raw(w) }
func (b browserSource) Page() *screen.Page {
	p := b.v.Page()
	return &p
}

var browserUpgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return r.Header.Get("Origin") == "" }}
