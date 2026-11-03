package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/gerdr-dev/gerdr/internal/uisite"
	"github.com/gerdr-dev/gerdr/internal/work"
)

// Work view. Every project (a layout "space": its Code workspaces plus its own Work) has a Work
// directory (work.Runner.Dir) holding its Work project (ui/), local functions (local/) and the Work
// agent's CLAUDE.md. Requests name the project with ?project= / "project"; empty = the default one.

type workUIs struct {
	mu   sync.Mutex
	m    map[string]*uisite.Renderer
	stop map[string]chan struct{} // ends each one's Watch (a deleted project's)
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
		ui.UseESM(s.ESM)
		stop := make(chan struct{})
		go ui.Watch(stop)
		s.workUIs.m[project] = ui
		if s.workUIs.stop == nil {
			s.workUIs.stop = map[string]chan struct{}{}
		}
		s.workUIs.stop[project] = stop
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
	// Delete a project: its directory moves to the trash (work.Runner.Remove), Work and all. Only once
	// it's closed — the UI closes it and saves the layout first — or the next layout save would write its
	// layout copy straight back and its open panes would point into a deleted directory.
	mux.HandleFunc("DELETE /api/projects/{name}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if layoutHasSpace(s.Layout.Get(), name) {
			httpError(w, http.StatusConflict, "project is still open; close it first")
			return
		}
		s.workUIs.mu.Lock()
		if stop := s.workUIs.stop[name]; stop != nil {
			close(stop)
			delete(s.workUIs.stop, name)
		}
		delete(s.workUIs.m, name)
		s.workUIs.mu.Unlock()
		trash, err := s.Work.Remove(name)
		if err != nil {
			httpError(w, 400, err.Error())
			return
		}
		_ = os.RemoveAll(filepath.Join(s.Dir, "cache", "work-ui", name)) // only a build cache
		writeJSON(w, map[string]any{"trash": trash})
	})
	// Undo a delete: the directory comes back from the trash (the UI puts the project's workspaces back after).
	mux.HandleFunc("POST /api/projects/{name}/restore", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Trash string `json:"trash"`
		}
		if json.NewDecoder(r.Body).Decode(&b) != nil || b.Trash == "" {
			httpError(w, 400, "trash is required")
			return
		}
		if err := s.Work.Restore(r.PathValue("name"), b.Trash); err != nil {
			httpError(w, http.StatusConflict, err.Error())
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	})
	// Local functions (internal/work):
	//
	//	GET  /api/work/functions?project=        what can be called
	//	POST /api/work/run {project, fn, input}  run; with Accept: text/event-stream the reply streams the run (start,
	//	                                         progress / log, then result or error), otherwise it waits and returns {result}
	//	GET  /api/work/runs/{id}                 pick a run up again (SSE from its first event): a reloaded page
	//	POST /api/work/runs/{id}/abort           stop it
	//	GET  /api/work/logs?project=&fn=&id=     past runs, or one run's log
	//	GET  /api/work/schedules?project=        schedules/ with each one's last run and next time (work/schedule.go)
	//	POST /api/work/schedules/{id}/{run|pause|resume}?project=
	//
	// A run doesn't belong to the request: closing the page or the CLI doesn't stop it (abort does).
	mux.HandleFunc("GET /api/work/functions", func(w http.ResponseWriter, r *http.Request) {
		list, err := s.Work.List(r.URL.Query().Get("project"))
		if err != nil {
			httpError(w, 400, err.Error())
			return
		}
		writeJSON(w, map[string]any{"list": list})
	})
	mux.HandleFunc("POST /api/work/run", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Project string `json:"project"`
			Fn      string `json:"fn"`
			Input   any    `json:"input"`
			From    string `json:"from"` // the agent conversation running it (`gerdr work run`): ctx.caller.session
		}
		if err := decode(r, &b); err != nil {
			httpError(w, 400, err.Error())
			return
		}
		// An agent running it from its repository (not a Work directory) gets the default project from its cwd; the
		// project its conversation is in is the one it works for (a worker the lb board started: lb)
		if b.From != "" && (b.Project == "" || b.Project == work.DefaultProject) {
			if sp, ok := s.sessionSpace(SessionSpaces(s.Layout.Get()), b.From); ok && sp != "" {
				b.Project = sp
			}
		}
		run := s.Work.StartAs(b.Project, b.Fn, b.Input, b.From)
		if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
			streamRun(w, r, run)
			return
		}
		// Older pages and callers: wait for the end
		res, err := run.Wait(r.Context())
		if err != nil {
			httpError(w, 500, err.Error())
			return
		}
		writeJSON(w, map[string]any{"result": res})
	})
	mux.HandleFunc("GET /api/work/runs/{id}", func(w http.ResponseWriter, r *http.Request) {
		run := s.Work.Get(r.PathValue("id"))
		if run == nil {
			httpError(w, 404, "no such run (it ended more than 10 minutes ago: see /api/work/logs)")
			return
		}
		streamRun(w, r, run)
	})
	mux.HandleFunc("POST /api/work/runs/{id}/abort", func(w http.ResponseWriter, r *http.Request) {
		run := s.Work.Get(r.PathValue("id"))
		if run == nil {
			httpError(w, 404, "no such run (it may have ended long ago)")
			return
		}
		run.Abort()
		writeJSON(w, map[string]any{"ok": true})
	})
	mux.HandleFunc("GET /api/work/logs", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if id := q.Get("id"); id != "" {
			lines, err := s.Work.LogOf(q.Get("project"), id)
			if err != nil {
				httpError(w, 404, err.Error())
				return
			}
			writeJSON(w, map[string]any{"lines": lines})
			return
		}
		limit, _ := strconv.Atoi(q.Get("limit"))
		if limit <= 0 {
			limit = 20
		}
		list, err := s.Work.Logs(q.Get("project"), q.Get("fn"), limit)
		if err != nil {
			httpError(w, 400, err.Error())
			return
		}
		writeJSON(w, map[string]any{"list": list})
	})
	// Work tools (work/tools.go), served to agents over MCP by `gerdr work mcp`:
	//	GET  /api/work/tools?project=                       the declared tools
	//	POST /api/work/tools/{name}?project= {session, args} run one for that conversation (ctx.caller.session)
	mux.HandleFunc("GET /api/work/tools", func(w http.ResponseWriter, r *http.Request) {
		list, err := s.Work.Tools(r.URL.Query().Get("project"))
		if err != nil {
			httpError(w, 400, err.Error())
			return
		}
		writeJSON(w, map[string]any{"list": list})
	})
	mux.HandleFunc("POST /api/work/tools/{name}", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Session string `json:"session"`
			Args    any    `json:"args"`
		}
		if err := decode(r, &b); err != nil {
			httpError(w, 400, err.Error())
			return
		}
		res, err := s.Work.CallTool(r.Context(), r.URL.Query().Get("project"), r.PathValue("name"), b.Session, b.Args)
		if err != nil {
			httpError(w, 500, err.Error())
			return
		}
		writeJSON(w, map[string]any{"result": res})
	})
	mux.HandleFunc("GET /api/work/schedules", func(w http.ResponseWriter, r *http.Request) {
		list, err := s.Work.Schedules(r.URL.Query().Get("project"))
		if err != nil {
			httpError(w, 400, err.Error())
			return
		}
		writeJSON(w, map[string]any{"list": list})
	})
	mux.HandleFunc("POST /api/work/schedules/{id}/{op}", func(w http.ResponseWriter, r *http.Request) {
		project, id := r.URL.Query().Get("project"), r.PathValue("id")
		var err error
		switch r.PathValue("op") {
		case "run":
			err = s.Work.RunSchedule(project, id)
		case "pause":
			err = s.Work.PauseSchedule(project, id, true)
		case "resume":
			err = s.Work.PauseSchedule(project, id, false)
		default:
			httpError(w, 404, "run, pause or resume")
			return
		}
		if err != nil {
			httpError(w, 400, err.Error())
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	})
	// `gerdr work secret set <name>`: ctx.secrets.get's keys, in secrets.json under work/ (never served back, only names)
	mux.HandleFunc("GET /api/work/secrets", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"list": s.Secrets.Names(workSecretPrefix)})
	})
	mux.HandleFunc("POST /api/work/secrets", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Name  string `json:"name"`
			Value string `json:"value"` // "" deletes
		}
		if err := decode(r, &b); err != nil || b.Name == "" || strings.ContainsAny(b.Name, "/ \n") {
			httpError(w, 400, "a name (no spaces or slashes) and a value are needed")
			return
		}
		if err := s.Secrets.Set(workSecretPrefix+b.Name, b.Value); err != nil {
			httpError(w, 500, err.Error())
			return
		}
		writeJSON(w, map[string]any{"ok": true})
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

// workSecretPrefix: local functions' keys in secrets.json, apart from gerdr's own (llm/*) so a function can't read those
const workSecretPrefix = "work/"

// streamRun sends a run's events as SSE from its first one until it ends or the client goes away (the run goes on).
func streamRun(w http.ResponseWriter, r *http.Request, run *work.Run) {
	flusher, _ := w.(http.Flusher)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Gerdr-Run", run.ID)
	w.WriteHeader(http.StatusOK)
	send := func(e any) {
		b, err := json.Marshal(e)
		if err != nil {
			b, _ = json.Marshal(work.Event{Type: "error", Data: map[string]any{"message": "the result can't be turned into JSON: " + err.Error()}})
		}
		_, _ = w.Write([]byte("data: " + string(b) + "\n\n"))
	}
	send(work.Event{Type: "start", Data: map[string]any{"id": run.ID, "fn": run.Fn}})
	n := 0
	for {
		evs, done, wake := run.Since(n)
		for _, e := range evs {
			send(e)
		}
		n += len(evs)
		if flusher != nil {
			flusher.Flush()
		}
		if done {
			return
		}
		select {
		case <-wake:
		case <-r.Context().Done():
			return
		}
	}
}

// layoutHasSpace: does the saved layout still have a workspace in project `space`?
func layoutHasSpace(raw json.RawMessage, space string) bool {
	var l struct {
		Workspaces []struct {
			Space string `json:"space"`
		} `json:"workspaces"`
	}
	if json.Unmarshal(raw, &l) != nil {
		return false
	}
	for _, w := range l.Workspaces {
		sp := w.Space
		if sp == "" {
			sp = work.DefaultProject
		}
		if sp == space {
			return true
		}
	}
	return false
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
