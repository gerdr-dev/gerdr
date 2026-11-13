package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gerdr-dev/gerdr/internal/artifacts"
	"github.com/gerdr-dev/gerdr/internal/hub"
	"github.com/gerdr-dev/gerdr/internal/i18n"
)

// Artifacts (docs/dev/ARTIFACTS.md): a session's artifacts folder and its records, and the file operations the
// folder view needs on top of the rest of /api/fs.
//
//	GET  /api/artifacts?session=|dir=   → {dir, exists, id?, items}: where they go; records when it exists
//	POST /api/artifacts {session|dir, kind, label, url, path, text, supersedes} → the record (creates the folder)
//	GET  /api/fs/list?path=             → {path, entries}: one level, Gerdr's own files left out
//	POST /api/fs/move {from, to}        → rename / move; refuses to overwrite
//	POST /api/fs/trash {path}           → into the Trash
//
// Same guard as the rest of /api/fs: whoever holds the token can already have an agent run any command.

// ArtifactsPlace is where a session's artifacts go, worked out the same way for the agent's prompt
// (hub.Options.Artifacts) and for this API.
type ArtifactsPlace func(rec hub.Record) (dir string, owner artifacts.Owner, project string)

// SessionArtifacts: the folder whoever started the session gave it (a panel's task: ctx.sessions.start artifactsDir),
// "" for none. Code sessions have none: an artifacts folder is a board's way of keeping a task's things together, and
// Code stays as it was (2026-11-13, the user: "code模式下不要有任何我们新加的文件夹产物的逻辑").
func SessionArtifacts(project func(hub.Record) string) ArtifactsPlace {
	return func(rec hub.Record) (string, artifacts.Owner, string) {
		return rec.ArtifactsDir, artifacts.Owner{}, project(rec)
	}
}

// SpaceName is the name a project shows (its id when it has none).
func SpaceName(raw json.RawMessage, id string) string {
	type layout struct {
		Spaces []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"spaces"`
	}
	var l struct {
		layout
		Data layout `json:"data"`
	}
	_ = json.Unmarshal(raw, &l)
	for _, sp := range append(l.Spaces, l.Data.Spaces...) {
		if sp.ID == id && strings.TrimSpace(sp.Name) != "" {
			return sp.Name
		}
	}
	return id
}

type artifactsTarget struct {
	dir     string
	owner   artifacts.Owner
	project string
	session string
}

// artifactsTarget: the folder a request is about, by session or by dir.
func (s *Server) artifactsTarget(session, dir string) (artifactsTarget, error) {
	if dir != "" {
		if !filepath.IsAbs(dir) && !strings.HasPrefix(dir, "~") {
			return artifactsTarget{}, errors.New("need an absolute path: " + dir)
		}
		return artifactsTarget{dir: resolvePath(dir, "")}, nil
	}
	if session == "" {
		return artifactsTarget{}, errors.New(i18n.S("要给 session 或 dir", "session or dir is required"))
	}
	if s.ArtifactsOf == nil {
		return artifactsTarget{}, errors.New("artifacts are off")
	}
	sess, err := s.Hub.Session(session)
	if err != nil {
		return artifactsTarget{}, err
	}
	d, o, p := s.ArtifactsOf(sess.Record())
	if d == "" {
		return artifactsTarget{}, errors.New(i18n.S("这个会话没有产物文件夹：只有面板（看板）开的任务对话才有", "This session has no artifacts folder: only task conversations a panel (board) started have one"))
	}
	return artifactsTarget{dir: d, owner: o, project: p, session: session}, nil
}

func (s *Server) artifactsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/artifacts", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		t, err := s.artifactsTarget(q.Get("session"), q.Get("dir"))
		if err != nil {
			httpError(w, 400, err.Error())
			return
		}
		out := map[string]any{"dir": t.dir, "exists": false, "items": []artifacts.Item{}, "root": s.Artifacts.Root()}
		if st, err := os.Stat(t.dir); err == nil && st.IsDir() {
			out["exists"] = true
			// A folder the agent made by itself (mkdir, then wrote into it) gets its identity on first sight
			if f, err := s.Artifacts.Ensure(t.dir, t.owner, t.project); err == nil {
				out["id"] = f.ID
			}
			items, err := artifacts.Items(t.dir)
			if err != nil {
				httpError(w, 500, err.Error())
				return
			}
			out["items"] = items
		}
		writeJSON(w, out)
	})
	mux.HandleFunc("POST /api/artifacts", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Session string `json:"session"`
			Dir     string `json:"dir"`
			artifacts.Item
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			httpError(w, 400, err.Error())
			return
		}
		t, err := s.artifactsTarget(body.Session, body.Dir)
		if err != nil {
			httpError(w, 400, err.Error())
			return
		}
		it := body.Item
		if it.Kind == artifacts.KindPath {
			it.Path = resolvePath(it.Path, "")
		}
		if it.Kind == artifacts.KindFile && filepath.IsAbs(it.Path) {
			if rel, err := filepath.Rel(t.dir, it.Path); err == nil && !strings.HasPrefix(rel, "..") {
				it.Path = rel
			}
		}
		if err := it.Validate(); err != nil {
			httpError(w, 400, err.Error())
			return
		}
		if it.Kind == artifacts.KindFile {
			if _, err := os.Stat(filepath.Join(t.dir, it.Path)); err != nil {
				httpError(w, 400, i18n.S("文件夹里没有这个文件：", "No such file in the folder: ")+it.Path)
				return
			}
		}
		if _, err := s.Artifacts.Ensure(t.dir, t.owner, t.project); err != nil {
			httpError(w, 500, err.Error())
			return
		}
		if it.Session == "" {
			it.Session = t.session
		}
		saved, err := artifacts.Record(t.dir, it)
		if err != nil {
			httpError(w, 400, err.Error())
			return
		}
		writeJSON(w, map[string]any{"dir": t.dir, "item": saved})
	})
	mux.HandleFunc("GET /api/fs/list", func(w http.ResponseWriter, r *http.Request) {
		p := resolvePath(r.URL.Query().Get("path"), "")
		es, err := artifacts.List(p)
		if err != nil {
			code := 400
			if os.IsNotExist(err) {
				code = 404
			}
			httpError(w, code, err.Error())
			return
		}
		type entry struct {
			artifacts.Entry
			Mime string `json:"mime,omitempty"`
		}
		out := make([]entry, 0, len(es))
		for _, e := range es {
			m := ""
			if !e.Dir {
				m = fileMime(filepath.Join(p, e.Name))
			}
			out = append(out, entry{e, m})
		}
		writeJSON(w, map[string]any{"path": p, "entries": out})
	})
	mux.HandleFunc("POST /api/fs/move", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			From string `json:"from"`
			To   string `json:"to"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !absLike(body.From) || !absLike(body.To) {
			httpError(w, 400, "from and to must be absolute paths")
			return
		}
		from, to := resolvePath(body.From, ""), resolvePath(body.To, "")
		if _, err := os.Lstat(to); err == nil {
			httpError(w, 409, i18n.S("那里已经有同名的了：", "Something with that name is already there: ")+to)
			return
		}
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			httpError(w, 400, err.Error())
			return
		}
		if err := os.Rename(from, to); err != nil {
			httpError(w, 400, err.Error())
			return
		}
		writeJSON(w, map[string]any{"path": to})
	})
	mux.HandleFunc("POST /api/fs/trash", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Path string `json:"path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !absLike(body.Path) {
			httpError(w, 400, "path must be absolute")
			return
		}
		p := resolvePath(body.Path, "")
		if home, _ := os.UserHomeDir(); p == home || p == "/" || p == s.Artifacts.Root() {
			httpError(w, 400, "refusing to trash "+p)
			return
		}
		if _, err := os.Lstat(p); err != nil {
			httpError(w, 404, err.Error())
			return
		}
		if err := s.Artifacts.Trash(p); err != nil {
			httpError(w, 400, err.Error())
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	})
}

func absLike(p string) bool { return filepath.IsAbs(p) || p == "~" || strings.HasPrefix(p, "~/") }
