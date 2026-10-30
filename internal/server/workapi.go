package server

import (
	"net/http"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/gerdr-dev/gerdr/internal/uisite"
	"github.com/gerdr-dev/gerdr/internal/work"
)

// Work view. Every project (a layout "space": its Code workspaces plus its own Work) has a Work
// directory (work.Runner.Dir) holding its Work project (ui/), local functions (local/) and the Work
// agent's CLAUDE.md. Requests name the project with ?project= / "project"; empty = the default one.

type workUIs struct {
	mu sync.Mutex
	m  map[string]*uisite.Renderer
}

// workUI is the project's Work site, served under /work/<project>/; started on first use and watched
// for edits from then on.
func (s *Server) workUI(project string) (*uisite.Renderer, error) {
	if project == "" {
		project = work.DefaultProject
	}
	dir, err := s.Work.Ensure(project)
	if err != nil {
		return nil, err
	}
	s.workUIs.mu.Lock()
	defer s.workUIs.mu.Unlock()
	if s.workUIs.m == nil {
		s.workUIs.m = map[string]*uisite.Renderer{}
	}
	ui := s.workUIs.m[project]
	if ui == nil {
		ui = uisite.NewAt(filepath.Join(dir, "ui"), filepath.Join(s.Dir, "cache", "work-ui", project), "/work/"+project, s.Log)
		go ui.Watch(nil)
		s.workUIs.m[project] = ui
	}
	return ui, nil
}

func (s *Server) workRoutes(mux *http.ServeMux) {
	if s.Work == nil {
		return
	}
	mux.HandleFunc("GET /api/work", func(w http.ResponseWriter, r *http.Request) {
		dir, err := s.Work.Ensure(r.URL.Query().Get("project"))
		if err != nil {
			httpError(w, 400, err.Error())
			return
		}
		writeJSON(w, map[string]any{"dir": dir})
	})
	// The project's saved Code layout (its workspaces), used when a project is created with a name that
	// existed before, so its Code comes back with its Work. {} when it has none.
	mux.HandleFunc("GET /api/projects/{name}/layout", func(w http.ResponseWriter, r *http.Request) {
		p, err := s.Work.Layout(r.PathValue("name"))
		if err != nil {
			httpError(w, 400, err.Error())
			return
		}
		if p == nil {
			writeJSON(w, map[string]any{})
			return
		}
		writeJSON(w, p)
	})
	mux.HandleFunc("POST /api/work/run", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Project string `json:"project"`
			Fn      string `json:"fn"`
			Input   any    `json:"input"`
		}
		if err := decode(r, &b); err != nil {
			httpError(w, 400, err.Error())
			return
		}
		res, err := s.Work.Run(r.Context(), b.Project, b.Fn, b.Input)
		if err != nil {
			httpError(w, 500, err.Error())
			return
		}
		writeJSON(w, map[string]any{"result": res})
	})
	// `gerdr work status`: the project's Work site build error and runtime errors, like `gerdr ui status`.
	mux.HandleFunc("GET /api/work/ui/status", func(w http.ResponseWriter, r *http.Request) {
		ui, err := s.workUI(r.URL.Query().Get("project"))
		if err != nil {
			httpError(w, 400, err.Error())
			return
		}
		fatal, issues := ui.Status()
		writeJSON(w, map[string]any{"dir": ui.Dir(), "build_error": fatal, "warnings": issues, "runtime_errors": ui.ClientErrors()})
	})
	mux.HandleFunc("/work/", s.serveWorkUI)
	mux.HandleFunc("/work", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/work/"+work.DefaultProject+"/", http.StatusFound)
	})
}

// serveWorkUI serves /work/<project>/…: the project's Work site (a React site like gerdr's own ui/, no
// build step). gerdr shows it in a same-origin frame next to the Work agent, so unlike every other page
// it may be framed — by gerdr itself only. It calls /api/work/* with the same login cookie.
func (s *Server) serveWorkUI(w http.ResponseWriter, r *http.Request) {
	project, rest, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/work/"), "/")
	if rest == "" && !strings.HasSuffix(r.URL.Path, "/") {
		http.Redirect(w, r, "/work/"+project+"/", http.StatusFound)
		return
	}
	ui, err := s.workUI(project)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	base := ui.Base()
	p := r.URL.Path
	switch {
	case strings.HasPrefix(p, base+"/_ui/m/"):
		ui.ServeModule(w, r)
	case p == base+"/_ui/events":
		ui.ServeEvents(w, r)
	case p == base+"/api/ui/errors" && r.Method == http.MethodPost:
		if !s.authed(r) {
			httpError(w, 401, "unauthorized")
			return
		}
		var b struct {
			Build string             `json:"build"`
			Items []uisite.ErrorItem `json:"items"`
		}
		if err := decode(r, &b); err != nil {
			httpError(w, 400, err.Error())
			return
		}
		ui.ReportClientErrors(b.Build, b.Items)
		writeJSON(w, map[string]any{})
	case ui.ServePublic(w, r):
	case path.Ext(p) != "" && !strings.HasSuffix(p, ".html"):
		http.NotFound(w, r)
	default:
		nonce := newNonce()
		w.Header().Set("Content-Security-Policy", strings.Replace(cspWith("'nonce-"+nonce+"'"), "frame-ancestors 'none'", "frame-ancestors 'self'", 1))
		ui.ServePage(w, r, nonce)
	}
}
