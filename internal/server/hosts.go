package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gerdr-dev/gerdr/internal/i18n"
	"github.com/gerdr-dev/gerdr/internal/peer"
)

// Other computers this Gerdr drives (docs/dev/MULTIHOST.md). The routing sits in front of the mux: a request about
// a host's session ("<host>~<id>") or carrying ?host= goes to that host, everything else is served here as before.

func (s *Server) hostRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/hosts", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, s.hostInfos()) })
	mux.HandleFunc("POST /api/hosts", func(w http.ResponseWriter, r *http.Request) {
		if s.Peers == nil {
			httpError(w, 501, "no hosts here")
			return
		}
		var b struct {
			Invite string `json:"invite"` // the host's "https://name/?peer=CODE", or just its address
			Code   string `json:"code"`
			Name   string `json:"name"`
		}
		if err := decode(r, &b); err != nil {
			httpError(w, 400, err.Error())
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		info, err := s.Peers.Add(ctx, b.Invite, b.Code, b.Name)
		if err != nil {
			httpError(w, 400, err.Error())
			return
		}
		writeJSON(w, info)
	})
	mux.HandleFunc("DELETE /api/hosts/{id}", func(w http.ResponseWriter, r *http.Request) {
		if s.Peers == nil || !s.Peers.Remove(r.PathValue("id")) {
			httpError(w, 404, peer.ErrNoHost.Error())
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	})
	mux.HandleFunc("POST /api/hosts/{id}/rename", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Name string `json:"name"`
		}
		_ = decode(r, &b)
		if s.Peers == nil || !s.Peers.Rename(r.PathValue("id"), b.Name) {
			httpError(w, 400, peer.ErrNoHost.Error())
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	})
}

func (s *Server) hostInfos() []peer.Info {
	if s.Peers == nil {
		return []peer.Info{}
	}
	return s.Peers.Infos()
}

// hostSessions: every host's sessions as this console shows them. maxAge: how old a cached list may be.
func (s *Server) hostSessions(ctx context.Context, maxAge time.Duration) []json.RawMessage {
	if s.Peers == nil || len(s.Peers.Hosts()) == 0 {
		return nil
	}
	return s.Peers.Sessions(ctx, maxAge)
}

// hostRoute: which host a request is for, and the path / query it has there. "" = this computer.
func (s *Server) hostRoute(r *http.Request) (host, path string, query url.Values) {
	if s.Peers == nil {
		return "", "", nil
	}
	p, q := r.URL.Path, r.URL.Query()
	switch {
	case strings.HasPrefix(p, "/h/"):
		// /h/<host>/<path on the host>: images in a host session's log, its files
		rest := strings.TrimPrefix(p, "/h/")
		i := strings.IndexByte(rest, '/')
		if i <= 0 {
			return "", "", nil
		}
		return rest[:i], rest[i:], q
	case strings.HasPrefix(p, "/api/sessions/"), strings.HasPrefix(p, "/api/commands/"):
		pre := "/api/sessions/"
		if strings.HasPrefix(p, "/api/commands/") {
			pre = "/api/commands/"
		}
		rest := strings.TrimPrefix(p, pre)
		seg, tail, _ := strings.Cut(rest, "/")
		h, id := peer.Split(seg)
		if h == "" {
			return "", "", nil
		}
		if tail != "" {
			tail = "/" + tail
		}
		return h, pre + id + tail, q
	case strings.HasPrefix(p, "/api/") && q.Get("host") != "":
		// ?host=: the directory browser, the history list and the like, asked of another computer
		h := q.Get("host")
		q.Del("host")
		return h, p, q
	}
	return "", "", nil
}

// hostProxy sends the request on to the host and passes the answer back, ids and URLs rewritten.
func (s *Server) hostProxy(w http.ResponseWriter, r *http.Request, host, path string, query url.Values) {
	h, ok := s.Peers.Host(host)
	if !ok {
		httpError(w, 404, peer.ErrNoHost.Error())
		return
	}
	var body io.Reader
	if r.Body != nil && r.Method != http.MethodGet && r.Method != http.MethodHead {
		b, err := io.ReadAll(io.LimitReader(r.Body, 64<<20))
		if err != nil {
			httpError(w, 400, err.Error())
			return
		}
		if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") || (len(b) > 0 && b[0] == '{') {
			b = peer.RewriteBody(host, b)
		}
		body = bytes.NewReader(b)
	}
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	resp, err := s.Peers.Do(r.Context(), host, r.Method, path, body, r.Header)
	if err != nil {
		httpError(w, 502, i18n.S("连不上那台电脑：", "Can't reach that computer: ")+err.Error())
		return
	}
	defer resp.Body.Close()
	for _, k := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "Cache-Control", "ETag", "Last-Modified", "Content-Disposition", "Content-Security-Policy"} {
		if v := resp.Header.Get(k); v != "" {
			w.Header().Set(k, v)
		}
	}
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
		return
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 256<<20))
	if err != nil {
		httpError(w, 502, err.Error())
		return
	}
	b = rewriteHostJSON(h, b)
	w.Header().Del("Content-Length")
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(b)
}

// rewriteHostJSON: a JSON answer from a host. A session summary (or a list of them) gets its ids prefixed; anything
// else only its URLs.
func rewriteHostJSON(h peer.Host, b []byte) []byte {
	isSummary := func(m map[string]json.RawMessage) bool {
		_, a := m["agent"]
		_, c := m["cwd"]
		_, i := m["id"]
		return a && c && i
	}
	t := bytes.TrimSpace(b)
	if len(t) > 0 && t[0] == '{' {
		var m map[string]json.RawMessage
		if json.Unmarshal(t, &m) == nil && isSummary(m) {
			if out := peer.RewriteSummary(h, t); out != nil {
				return out
			}
		}
	}
	if len(t) > 0 && t[0] == '[' {
		var list []map[string]json.RawMessage
		if json.Unmarshal(t, &list) == nil && len(list) > 0 && isSummary(list[0]) {
			var raws []json.RawMessage
			_ = json.Unmarshal(t, &raws)
			out := make([]json.RawMessage, 0, len(raws))
			for _, r := range raws {
				if x := peer.RewriteSummary(h, r); x != nil {
					out = append(out, x)
				}
			}
			if nb, err := json.Marshal(out); err == nil {
				return nb
			}
		}
	}
	return peer.RewriteURLs(h.ID, b)
}

// createOnHost: POST /api/sessions with "host": the conversation is started on that computer. The UI places it in
// its own layout as usual; "open" (the CLI's placement) is the console's business and isn't sent.
func (s *Server) createOnHost(w http.ResponseWriter, r *http.Request, host string, body []byte) {
	var m map[string]any
	_ = json.Unmarshal(body, &m)
	delete(m, "open")
	delete(m, "host")
	b, _ := json.Marshal(m)
	r2 := r.Clone(r.Context())
	r2.Body = io.NopCloser(bytes.NewReader(b))
	r2.Header.Set("Content-Type", "application/json")
	s.hostProxy(w, r2, host, "/api/sessions", nil)
}

// withHosts wraps the mux: requests for another computer are proxied, the rest fall through.
func (s *Server) withHosts(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.Peers == nil {
			next.ServeHTTP(w, r)
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/sessions" {
			b, err := io.ReadAll(io.LimitReader(r.Body, 64<<20))
			if err != nil {
				httpError(w, 400, err.Error())
				return
			}
			var x struct {
				Host string `json:"host"`
			}
			_ = json.Unmarshal(b, &x)
			if x.Host != "" {
				s.createOnHost(w, r, x.Host, b)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(b))
			next.ServeHTTP(w, r)
			return
		}
		if host, path, q := s.hostRoute(r); host != "" {
			s.hostProxy(w, r, host, path, q)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// hostWS: the per-connection upstreams of one console /ws (see peer.Upstream).
type hostWS struct {
	ups map[string]*peer.Upstream
	out chan json.RawMessage
}

func (s *Server) openHostWS(ctx context.Context) *hostWS {
	if s.Peers == nil {
		return nil
	}
	hosts := s.Peers.Hosts()
	if len(hosts) == 0 {
		return nil
	}
	hw := &hostWS{ups: map[string]*peer.Upstream{}, out: make(chan json.RawMessage, 1024)}
	for _, h := range hosts {
		hw.ups[h.ID] = s.Peers.Connect(ctx, h.ID, hw.out)
	}
	return hw
}

// watch splits a watch list by computer: returns the ids / seqs for this one, hands the rest to the upstreams.
func (hw *hostWS) watch(ids []string, since map[string]int64) ([]string, map[string]int64) {
	if hw == nil {
		return ids, since
	}
	local, lsince := []string{}, map[string]int64{}
	per := map[string][]string{}
	psince := map[string]map[string]int64{}
	for _, id := range ids {
		h, rest := peer.Split(id)
		if h == "" {
			local = append(local, id)
			if v, ok := since[id]; ok {
				lsince[id] = v
			}
			continue
		}
		per[h] = append(per[h], rest)
		if v, ok := since[id]; ok {
			if psince[h] == nil {
				psince[h] = map[string]int64{}
			}
			psince[h][rest] = v
		}
	}
	for h, u := range hw.ups {
		u.Watch(append([]string{}, per[h]...), psince[h])
	}
	return local, lsince
}

// draft: a composer draft for a host session goes to that host. false = it's a local one.
func (hw *hostWS) draft(id string, d any) bool {
	if hw == nil {
		return false
	}
	h, rest := peer.Split(id)
	if h == "" {
		return false
	}
	if u := hw.ups[h]; u != nil {
		u.Send(map[string]any{"t": "draft", "id": rest, "draft": d})
	}
	return true
}

func (hw *hostWS) C() <-chan json.RawMessage {
	if hw == nil {
		return nil
	}
	return hw.out
}
