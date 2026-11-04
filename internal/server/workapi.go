package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gerdr-dev/gerdr/internal/uisite"
	"github.com/gerdr-dev/gerdr/internal/work"
)

// Work view. Every project (a layout "space": its Code workspaces plus its own Work) has a Work
// directory (work.Runner.Dir) holding its Work project (ui/), local functions (local/) and the Work
// agent's CLAUDE.md. Requests name the project with ?project= / "project"; empty = the default one.

// mountWork compiles a Work's UI (a project's own, or one of its panels: work.SplitKey) into gerdr's own page
// (uisite mounts): WorkView draws it as a second React root, not a frame. Every Work is mounted at start
// (MountWorks) and on first use.
func (s *Server) mountWork(key string) (string, error) {
	if key == "" {
		key = work.DefaultProject
	}
	dir, err := s.Work.Ensure(key)
	if err != nil {
		return "", err
	}
	if s.UI != nil {
		s.UI.Mount(mountName(key), filepath.Join(dir, "ui"))
	}
	return dir, nil
}

// mountName: a Work's mount in the page. A mount name is one path segment (@m/<name>/…), so a panel's "/" is "~"
// (no project or panel name can hold either); the page maps it back (ui/src/lib/mounts.ts).
func mountName(key string) string { return strings.Replace(key, "/", "~", 1) }

// MountWorks mounts the Work UI of every project that has one, so a page loads with them all (each loads its modules
// only when shown).
func (s *Server) MountWorks() {
	if s.Work == nil || s.UI == nil {
		return
	}
	for _, p := range s.Work.Projects() {
		if _, err := s.mountWork(p); err != nil {
			s.Log.Warn("work: mount", "project", p, "err", err)
		}
	}
}

func (s *Server) workRoutes(mux *http.ServeMux) {
	if s.Work == nil {
		return
	}
	mux.HandleFunc("GET /api/work", func(w http.ResponseWriter, r *http.Request) {
		dir, err := s.mountWork(r.URL.Query().Get("project"))
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
		if s.UI != nil {
			s.UI.Mount(mountName(name), "")
			for _, p := range s.Work.Panels(name) {
				s.UI.Mount(mountName(work.PanelKey(name, p)), "")
			}
		}
		trash, err := s.Work.Remove(name)
		if err != nil {
			httpError(w, 400, err.Error())
			return
		}
		writeJSON(w, map[string]any{"trash": trash})
	})
	// Where the project's Work came from, and replacing it (work/source.go): a published Work app from a git
	// repository, the built-in blank one, or a pull of the repository it came from. The old one goes to the trash.
	mux.HandleFunc("GET /api/work/source", func(w http.ResponseWriter, r *http.Request) {
		src, err := s.Work.Source(r.URL.Query().Get("project"))
		if err != nil {
			httpError(w, 400, err.Error())
			return
		}
		writeJSON(w, src)
	})
	// A project's panels beyond its Work (work/source.go): install one from a git repository, add a blank one, remove
	// one (to the trash). Adding never replaces anything.
	mux.HandleFunc("POST /api/work/install", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Project string `json:"project"`
			URL     string `json:"url"`
			Name    string `json:"name"`
		}
		if err := decode(r, &b); err != nil {
			httpError(w, 400, err.Error())
			return
		}
		key, err := s.Work.Install(r.Context(), b.Project, b.URL, b.Name)
		if err != nil {
			httpError(w, 400, err.Error())
			return
		}
		s.Log.Info("work: panel installed", "key", key, "url", b.URL)
		_, _ = s.mountWork(key)
		writeJSON(w, map[string]any{"key": key})
	})
	// A project's panels for the rail and Settings → Panels: its own Work (unless removed) and the rest, each with what
	// its ui.json says (title, icon, color) and where it came from.
	mux.HandleFunc("GET /api/work/panels", func(w http.ResponseWriter, r *http.Request) {
		project := r.URL.Query().Get("project")
		if project == "" {
			project = work.DefaultProject
		}
		type panel struct {
			Key    string         `json:"key"`
			Name   string         `json:"name"`
			Config map[string]any `json:"config"`
			Source work.Source    `json:"source"`
		}
		list := []panel{}
		add := func(key, name string) {
			dir, err := s.Work.Dir(key)
			if err != nil {
				return
			}
			p := panel{Key: key, Name: name, Config: map[string]any{}}
			if raw, err := os.ReadFile(filepath.Join(dir, "ui", "ui.json")); err == nil {
				_ = json.Unmarshal(raw, &p.Config)
			}
			p.Source, _ = s.Work.Source(key)
			list = append(list, p)
		}
		removed := s.Work.WorkRemoved(project)
		if !removed {
			add(project, "")
		}
		for _, n := range s.Work.Panels(project) {
			add(work.PanelKey(project, n), n)
		}
		writeJSON(w, map[string]any{"panels": list, "ownRemoved": removed})
	})
	// Undo a removal: the panel comes back from the trash as it was.
	mux.HandleFunc("POST /api/work/panels/unremove", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Key   string `json:"key"`
			Trash string `json:"trash"`
		}
		if err := decode(r, &b); err != nil {
			httpError(w, 400, err.Error())
			return
		}
		if err := s.Work.UnremovePanel(b.Key, b.Trash); err != nil {
			httpError(w, 409, err.Error())
			return
		}
		_, _ = s.mountWork(b.Key)
		writeJSON(w, map[string]any{"ok": true})
	})
	mux.HandleFunc("POST /api/work/panels/restore", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Project string `json:"project"`
		}
		if err := decode(r, &b); err != nil {
			httpError(w, 400, err.Error())
			return
		}
		if err := s.Work.RestoreWork(b.Project); err != nil {
			httpError(w, 400, err.Error())
			return
		}
		_, _ = s.mountWork(b.Project)
		writeJSON(w, map[string]any{"ok": true})
	})
	mux.HandleFunc("POST /api/work/panels", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Project string `json:"project"`
			Name    string `json:"name"`
		}
		if err := decode(r, &b); err != nil {
			httpError(w, 400, err.Error())
			return
		}
		key, err := s.Work.NewPanel(b.Project, b.Name)
		if err != nil {
			httpError(w, 400, err.Error())
			return
		}
		_, _ = s.mountWork(key)
		writeJSON(w, map[string]any{"key": key})
	})
	// A panel's name and its icon on the rail: "title", "icon" / "color" in its ui/ui.json (icon: the same kinds as an
	// agent's, a glyph or line icon key or "sprite:<key>"; color: a palette key). Written into the site's own file, so
	// they travel with the panel when it's published. The panel's directory keeps its name.
	setUIConfig := func(w http.ResponseWriter, key string, vals map[string]string) {
		dir, err := s.Work.Dir(key)
		if err != nil {
			httpError(w, 400, err.Error())
			return
		}
		p := filepath.Join(dir, "ui", "ui.json")
		cfg := map[string]any{}
		if raw, err := os.ReadFile(p); err == nil {
			if err := json.Unmarshal(raw, &cfg); err != nil {
				httpError(w, 400, "ui.json is invalid: "+err.Error())
				return
			}
		} else if !os.IsNotExist(err) {
			httpError(w, 500, err.Error())
			return
		}
		for k, v := range vals {
			if v = strings.TrimSpace(v); v == "" || len(v) > 64 {
				delete(cfg, k)
			} else {
				cfg[k] = v
			}
		}
		out, _ := json.MarshalIndent(cfg, "", "  ")
		if err := os.WriteFile(p, append(out, '\n'), 0o644); err != nil {
			httpError(w, 500, err.Error())
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	}
	mux.HandleFunc("POST /api/work/panels/icon", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Key   string `json:"key"`
			Icon  string `json:"icon"`
			Color string `json:"color"`
		}
		if err := decode(r, &b); err != nil {
			httpError(w, 400, err.Error())
			return
		}
		setUIConfig(w, b.Key, map[string]string{"icon": b.Icon, "color": b.Color})
	})
	mux.HandleFunc("POST /api/work/panels/title", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Key   string `json:"key"`
			Title string `json:"title"`
		}
		if err := decode(r, &b); err != nil {
			httpError(w, 400, err.Error())
			return
		}
		if strings.TrimSpace(b.Title) == "" {
			httpError(w, 400, "a panel needs a name")
			return
		}
		setUIConfig(w, b.Key, map[string]string{"title": b.Title})
	})
	mux.HandleFunc("POST /api/work/panels/remove", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Key string `json:"key"`
		}
		if err := decode(r, &b); err != nil {
			httpError(w, 400, err.Error())
			return
		}
		trash, err := s.Work.RemovePanel(b.Key)
		if err != nil {
			httpError(w, 400, err.Error())
			return
		}
		if s.UI != nil {
			s.UI.Mount(mountName(b.Key), "")
		}
		s.Log.Info("work: panel removed", "key", b.Key, "trash", trash)
		writeJSON(w, map[string]any{"trash": trash})
	})
	mux.HandleFunc("POST /api/work/update", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Project string `json:"project"`
		}
		if err := decode(r, &b); err != nil {
			httpError(w, 400, err.Error())
			return
		}
		out, err := s.Work.Update(r.Context(), b.Project)
		if err != nil {
			httpError(w, 400, err.Error())
			return
		}
		writeJSON(w, map[string]any{"output": out})
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
	mux.HandleFunc("GET /api/work/triggers", func(w http.ResponseWriter, r *http.Request) {
		list, err := s.Work.Triggers(r.URL.Query().Get("project"))
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
		project := r.URL.Query().Get("project")
		if project == "" {
			project = work.DefaultProject
		}
		dir, err := s.mountWork(project)
		if err != nil || s.UI == nil {
			httpError(w, 400, fmt.Sprint(err))
			return
		}
		fatal, runtime, _ := s.UI.MountStatus(mountName(project))
		if runtime == nil {
			runtime = []uisite.ClientError{}
		}
		writeJSON(w, map[string]any{"dir": filepath.Join(dir, "ui"), "build_error": fatal, "warnings": []any{}, "runtime_errors": runtime})
	})
	mux.HandleFunc("/work/", s.serveWorkUI)
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

// serveWorkUI serves /work/<project>/<file>: the files in the project's Work UI public/ (images a page links to). The
// pages themselves are mounted into gerdr's own page (mountWork).
func (s *Server) serveWorkUI(w http.ResponseWriter, r *http.Request) {
	project, rest, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/work/"), "/")
	dir, err := s.Work.Dir(project)
	if err != nil || rest == "" {
		http.NotFound(w, r)
		return
	}
	p := filepath.Join(dir, "ui", "public", filepath.FromSlash(path.Clean("/"+rest)))
	if fi, err := os.Stat(p); err != nil || fi.IsDir() {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, p)
}
