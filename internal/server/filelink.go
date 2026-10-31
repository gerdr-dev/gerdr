package server

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Cookie-less links to one file, for the file viewer's "open externally" button in the Android / desktop App.
// Those hand the address to the system browser, which has no gerdr cookie and so gets a 401. The page asks for a
// link first (POST /api/fs/link, behind the normal auth) and opens /f/<token>/<name>, which needs no cookie.
//
// The token is random and kept in memory rather than a signed JWT: nothing to keep secret on disk, a restart
// revokes every link, and it is bound to the one file. It works for linkTTL from issue, flat: a video still
// seeking after that gets 404s and has to be opened again.
//
// Whoever has the URL can read that file until then, so it never goes anywhere but the browser it is opened in:
// the page doesn't show or copy it, and Referrer-Policy is no-referrer.

const (
	linkTTL = 5 * time.Minute
	linkMax = 1000 // live links at most; a page asking for more than that is misbehaving
)

type fileLink struct {
	path string
	exp  time.Time
}

type fileLinks struct {
	mu sync.Mutex
	m  map[string]*fileLink
}

func (l *fileLinks) issue(path string, now time.Time) (string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.m == nil {
		l.m = map[string]*fileLink{}
	}
	for k, v := range l.m {
		if now.After(v.exp) {
			delete(l.m, k)
		}
	}
	if len(l.m) >= linkMax {
		return "", false
	}
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	tok := base64.RawURLEncoding.EncodeToString(b)
	l.m[tok] = &fileLink{path: path, exp: now.Add(linkTTL)}
	return tok, true
}

func (l *fileLinks) lookup(tok string, now time.Time) (string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	v := l.m[tok]
	if v == nil || now.After(v.exp) {
		return "", false
	}
	return v.path, true
}

// fileLinkIssue: POST /api/fs/link {path, cwd} → {url: "/f/<token>/<name>"}. The name is only there so the
// browser and whatever app it hands the file to show a real file name; the server goes by the token.
func (s *Server) fileLinkIssue(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path string `json:"path"`
		Cwd  string `json:"cwd"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, 400, err.Error())
		return
	}
	p := resolvePath(body.Path, body.Cwd)
	st, err := os.Stat(p)
	if err != nil {
		httpError(w, 404, "没有这个文件："+p)
		return
	}
	if st.IsDir() {
		httpError(w, 400, "这是个目录："+p)
		return
	}
	tok, ok := s.links.issue(p, time.Now())
	if !ok {
		httpError(w, http.StatusTooManyRequests, "链接太多了，过几分钟再试")
		return
	}
	writeJSON(w, map[string]string{"url": "/f/" + tok + "/" + url.PathEscape(filepath.Base(p))})
}

// fileLinkServe: GET /f/<token>/<name>. Public on both doors (guard lets non-/api paths through locally,
// remotePublic remotely): the token is the credential.
func (s *Server) fileLinkServe(w http.ResponseWriter, r *http.Request) {
	p, ok := s.links.lookup(r.PathValue("token"), time.Now())
	if !ok {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("链接已过期，回 gerdr 里再点一次「在外部打开」\n"))
		return
	}
	serveRaw(w, r, p, r.URL.Query().Get("download") == "1")
}
