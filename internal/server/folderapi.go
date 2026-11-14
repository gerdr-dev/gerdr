package server

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/gerdr-dev/gerdr/internal/folder"
	"github.com/gerdr-dev/gerdr/internal/i18n"
)

// What `gerdr folder pull / push` needs from the computer on the other side of an artifact folder
// (internal/folder, docs/dev/MULTIHOST.md). The console reaches another computer's through ?host=.
//
//	GET /api/fs/tree?path=          every file under path (ignores applied), with size, mtime, sha256 and mode
//	PUT /api/fs/file?path=&ifMatch= write one file, only if it still is the version the caller last saw
//
// `gerdr cp` uses the same two: tree with all=1 (every file, as cp would; exclude= repeatable, .gerdrignore syntax),
// file with ifMatch=* and mode= (permission bits, octal), checking the sha256 it answers against what it sent.
//
// Nothing an agent or a terminal here couldn't do already: same guard as the rest of /api/fs.
func (s *Server) folderRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/fs/tree", func(w http.ResponseWriter, r *http.Request) {
		p := resolvePath(r.URL.Query().Get("path"), "")
		st, err := os.Stat(p)
		if err != nil {
			code := 400
			if os.IsNotExist(err) {
				code = 404
			}
			httpError(w, code, err.Error())
			return
		}
		if !st.IsDir() {
			httpError(w, 400, i18n.S("不是目录：", "Not a folder: ")+p)
			return
		}
		q := r.URL.Query()
		all := q.Get("all") == "1"
		files, err := folder.WalkWith(p, folder.Options{All: all, Exclude: q["exclude"]})
		if err != nil {
			httpError(w, 400, err.Error())
			return
		}
		if files == nil {
			files = []folder.Entry{}
		}
		// all echoed: an older Gerdr ignores the parameter, and the caller has to know its files were filtered
		writeJSON(w, map[string]any{"path": p, "files": files, "all": all})
	})
	// ifMatch: the sha256 the caller's copy was based on; "none" = the file must not exist yet; "*" = overwrite.
	// A mismatch is 412 with the current sha256: someone changed it here since, and the caller keeps both.
	mux.HandleFunc("PUT /api/fs/file", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		p := q.Get("path")
		if !filepath.IsAbs(p) && (len(p) == 0 || p[0] != '~') {
			httpError(w, 400, "need an absolute path: "+p)
			return
		}
		p = resolvePath(p, "")
		cur := "none"
		if st, err := os.Stat(p); err == nil {
			if st.IsDir() {
				httpError(w, 400, i18n.S("这是个目录：", "That's a directory: ")+p)
				return
			}
			if cur, err = folder.Hash(p, st); err != nil {
				httpError(w, 500, err.Error())
				return
			}
		}
		if want := q.Get("ifMatch"); want != "*" && want != cur {
			writeJSONStatus(w, http.StatusPreconditionFailed, map[string]any{"error": i18n.S("那边已经改过了", "It changed there meanwhile"), "current": cur})
			return
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			httpError(w, 400, err.Error())
			return
		}
		// Written next to it and renamed: a reader never sees half a file, and a failed upload leaves the old one
		b := make([]byte, 6)
		_, _ = rand.Read(b)
		tmp := filepath.Join(filepath.Dir(p), ".gerdr-tmp-"+hex.EncodeToString(b))
		f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			httpError(w, 400, err.Error())
			return
		}
		h := sha256.New()
		_, err = io.Copy(io.MultiWriter(f, h), r.Body)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		var mode int64 = -1
		if m := q.Get("mode"); m != "" && err == nil {
			if mode, err = strconv.ParseInt(m, 8, 32); err == nil {
				err = os.Chmod(tmp, os.FileMode(mode).Perm())
			}
		}
		if err == nil {
			err = os.Rename(tmp, p)
		}
		if err != nil {
			os.Remove(tmp)
			if errors.Is(err, http.ErrBodyReadAfterClose) {
				err = errors.New("upload cut off")
			}
			httpError(w, 400, err.Error())
			return
		}
		out := map[string]any{"path": p, "sha256": hex.EncodeToString(h.Sum(nil))}
		if mode >= 0 {
			out["mode"] = mode
		}
		writeJSON(w, out)
	})
}
