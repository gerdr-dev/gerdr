package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"github.com/gerdr-dev/gerdr/internal/auth"
	"github.com/gerdr-dev/gerdr/internal/i18n"
	"github.com/gerdr-dev/gerdr/internal/peer"
)

// Other computers this Gerdr drives (docs/dev/MULTIHOST.md). The routing sits in front of the mux: a request about
// a host's session ("<host>:<id>") or carrying ?host= goes to that host, everything else is served here as before.

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
	case strings.HasPrefix(p, "/api/sessions/"), strings.HasPrefix(p, "/api/commands/"), strings.HasPrefix(p, "/api/options/"), strings.HasPrefix(p, "/api/terms/"):
		// a host's session, command list, option list or terminal: "<host>:<id>" (terminals: termOnHost)
		pre := "/api/sessions/"
		for _, x := range []string{"/api/commands/", "/api/options/", "/api/terms/"} {
			if strings.HasPrefix(p, x) {
				pre = x
			}
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
	// File contents pass as they are, both ways: an uploaded or viewed .json is a file, not an API payload
	isFile := path == "/api/fs/upload" || path == "/api/fs/raw" || path == "/api/fs/file" || strings.HasPrefix(path, "/f/")
	var body io.Reader
	if r.Body != nil && r.Method != http.MethodGet && r.Method != http.MethodHead {
		if isFile {
			body = r.Body // streamed: an attachment can be far bigger than an API body
		} else {
			b, err := io.ReadAll(io.LimitReader(r.Body, 64<<20))
			if err != nil {
				httpError(w, 400, err.Error())
				return
			}
			body = bytes.NewReader(peer.RewriteBody(host, b))
		}
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
	if isFile || !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
		return
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 256<<20))
	if err != nil {
		httpError(w, 502, err.Error())
		return
	}
	b = s.rewriteHostJSON(h, path, b)
	w.Header().Del("Content-Length")
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(b)
}

// rewriteHostJSON: a JSON answer from a host. A session summary (or a list of them) gets its ids prefixed, a log
// its senders (peer.RewriteEvents); anything else only its URLs.
func (s *Server) rewriteHostJSON(h peer.Host, path string, b []byte) []byte {
	if strings.HasPrefix(path, "/api/terms") {
		return prefixID(h.ID, b) // a terminal's info: its id as this computer calls it
	}
	if strings.HasSuffix(path, "/events") {
		return peer.RewriteEvents(h.ID, b)
	}
	if strings.HasSuffix(path, "/events/older") {
		var o map[string]json.RawMessage
		if json.Unmarshal(b, &o) == nil && o["events"] != nil {
			o["events"] = peer.RewriteEvents(h.ID, o["events"])
			if nb, err := json.Marshal(o); err == nil {
				return nb
			}
		}
	}
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
			if out := s.Peers.RewriteSummary(h, t); out != nil {
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
				if x := s.Peers.RewriteSummary(h, r); x != nil {
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
	var o struct {
		Cwd      string      `json:"cwd"`
		ReportTo string      `json:"reportTo"`
		Open     *createOpen `json:"open"`
	}
	_ = json.Unmarshal(body, &o)
	// Started by a panel's conversation: the panel's, there as here (adoptPanel), and not placed in Code here
	var tags map[string]string
	if t, ok := m["tags"].(map[string]any); ok {
		tags = map[string]string{}
		for k, v := range t {
			if sv, ok := v.(string); ok {
				tags[k] = sv
			}
		}
	}
	owner := s.adoptPanel(o.Open, &tags)
	if owner != "" {
		m["tags"] = tags
	}
	delete(m, "open")
	delete(m, "host")
	// A report target here or on a third computer is kept here (peer.Manager.ReportTo); one on that host goes along
	keep := ""
	if h, _ := peer.Split(o.ReportTo); o.ReportTo != "" && h != host {
		if err := s.reportTarget(o.ReportTo); err != nil {
			httpError(w, 400, err.Error())
			return
		}
		keep = o.ReportTo
		delete(m, "reportTo")
	}
	b, _ := json.Marshal(m)
	resp, err := s.Peers.Do(r.Context(), host, "POST", "/api/sessions", bytes.NewReader(peer.RewriteBody(host, b)), http.Header{"Content-Type": {"application/json"}})
	if err != nil {
		httpError(w, 502, i18n.S("连不上那台电脑：", "Can't reach that computer: ")+err.Error())
		return
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	h, _ := s.Peers.Host(host)
	if resp.StatusCode != 200 {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(rb)
		return
	}
	var sum struct {
		ID  string `json:"id"`
		Cwd string `json:"cwd"`
	}
	_ = json.Unmarshal(rb, &sum)
	id := peer.Join(host, sum.ID)
	if keep != "" {
		_ = s.Peers.SetReportTo(id, keep)
	}
	out := s.Peers.RewriteSummary(h, rb)
	s.Peers.NoteSession(host, out)
	// `gerdr session new --host`: it shows on this computer's screen, next to the agent that asked (a panel's: in the panel)
	if owner == "" {
		if err := s.placeCreated(o.Open, id, o.Cwd, sum.Cwd); err != nil {
			httpError(w, 400, err.Error())
			return
		}
	}
	writeJSON(w, json.RawMessage(out))
}

// reportNext: where id reports to, wherever that is kept: here (its Record), for a host session the target kept here
// (peer.Manager.ReportTo) or else its own one on that host (from its summary). ok: id is a session anyone knows.
func (s *Server) reportNext(id string) (next string, ok bool) {
	if sess, err := s.Hub.Session(id); err == nil {
		return sess.Summary().ReportTo, true
	}
	if s.Peers == nil {
		return "", false
	}
	if to := s.Peers.ReportTo(id); to != "" {
		return to, true
	}
	return s.Peers.Known(id)
}

// reportLoop refuses from → to when the reports from to on lead back to from (any length: a ↔ b, or a → b → c → a
// across three computers). A report starts a turn whose end is reported in turn: a loop wakes its sessions forever.
func (s *Server) reportLoop(from, to string) error {
	if from == to {
		return errors.New(i18n.S("会话不能回报给自己", "a session can't report to itself"))
	}
	seen := map[string]bool{}
	for cur := to; cur != "" && !seen[cur]; {
		seen[cur] = true
		next, ok := s.reportNext(cur)
		if !ok {
			return nil
		}
		if next == from {
			return errors.New(i18n.S("回报会绕回自己（这条链最后又回报给这个会话）", "reports would go round in a loop (the chain comes back to this session)"))
		}
		cur = next
	}
	return nil
}

// reportTarget: to can be reported to from another computer's session (it's here, or a known host session).
func (s *Server) reportTarget(to string) error {
	if _, err := s.Hub.Session(to); err == nil {
		return nil
	}
	if _, ok := s.Peers.Known(to); ok {
		return nil
	}
	return errors.New(i18n.S("没有这个会话：", "No such session: ") + to)
}

// hostMeta: tags / report target of a host session. Tags and a target on the same host are the host's; a target
// here or elsewhere is kept here (it can't be stored there, see peer/reports.go). Setting one clears the other.
func (s *Server) hostMeta(w http.ResponseWriter, r *http.Request, id string) {
	host, rest := peer.Split(id)
	var b struct {
		Tags     map[string]string `json:"tags"`
		ReportTo *string           `json:"reportTo"`
	}
	raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err := json.Unmarshal(raw, &b); err != nil && len(raw) > 0 {
		httpError(w, 400, err.Error())
		return
	}
	fwd := map[string]any{}
	if len(b.Tags) > 0 {
		fwd["tags"] = b.Tags
	}
	if b.ReportTo != nil {
		to := *b.ReportTo
		// The whole chain from the target on, wherever each step is kept: the host only sees its own part of it
		if to != "" {
			if err := s.reportLoop(id, to); err != nil {
				httpError(w, 400, err.Error())
				return
			}
		}
		if h, _ := peer.Split(to); to == "" || h == host {
			_ = s.Peers.SetReportTo(id, "")
			fwd["reportTo"] = to
		} else {
			if err := s.reportTarget(to); err != nil {
				httpError(w, 400, err.Error())
				return
			}
			if err := s.Peers.SetReportTo(id, to); err != nil {
				httpError(w, 500, err.Error())
				return
			}
			fwd["reportTo"] = "" // its own (same-host) target, if any, gives way
		}
	}
	method, path := "GET", "/api/sessions/"+rest
	var body io.Reader
	if len(fwd) > 0 {
		fb, _ := json.Marshal(fwd)
		method, path, body = "POST", path+"/meta", bytes.NewReader(fb) // hostProxy rewrites the ids (once)
	}
	r2 := r.Clone(r.Context())
	r2.Method = method
	r2.Body = io.NopCloser(body)
	if body == nil {
		r2.Body = http.NoBody
	}
	r2.Header.Set("Content-Type", "application/json")
	s.hostProxy(w, r2, host, path, nil)
}

// hostOpen / hostClose: `gerdr session open / close` on a host session is about this computer's screen (the host's
// own UI isn't the one the person looks at); closing also stops the agent over there, like closing a pane does.
func (s *Server) hostOpen(w http.ResponseWriter, r *http.Request, id string) {
	var b struct {
		Near  string `json:"near"`
		Focus bool   `json:"focus"`
	}
	_ = decode(r, &b)
	s.Hub.Broadcast("open", map[string]any{"id": id, "near": b.Near, "focus": b.Focus})
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) hostClose(w http.ResponseWriter, r *http.Request, id string) {
	host, rest := peer.Split(id)
	var b struct {
		AfterTurn bool `json:"afterTurn"`
	}
	_ = decode(r, &b)
	if b.AfterTurn {
		// Its turn may still be running: the host closes it when that ends (Hub.CloseAfterTurn there), and its
		// session_removed / close reaches this screen through the stream
		r2 := r.Clone(r.Context())
		r2.Body = io.NopCloser(strings.NewReader(`{"afterTurn":true}`))
		r2.Header.Set("Content-Type", "application/json")
		s.hostProxy(w, r2, host, "/api/sessions/"+rest+"/close", nil)
		return
	}
	s.Hub.Broadcast("close", map[string]any{"id": id})
	resp, err := s.Peers.Do(r.Context(), host, "POST", "/api/sessions/"+rest+"/shutdown", strings.NewReader("{}"), http.Header{"Content-Type": {"application/json"}})
	if err == nil {
		resp.Body.Close()
	}
	writeJSON(w, map[string]any{"ok": true})
}

// withHosts wraps the mux: requests for another computer are proxied, the rest fall through.
func (s *Server) withHosts(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.Peers == nil {
			next.ServeHTTP(w, r)
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/terms" {
			if b, host := bodyHost(w, r); b != nil && host != "" {
				s.termOnHost(w, r, host, b)
				return
			}
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
			// Carrying on a conversation of another computer (hostmove.go)
			if _, _, move := moveTarget(b); move {
				s.moveConversation(w, r, b)
				return
			}
			if x.Host != "" {
				s.createOnHost(w, r, x.Host, b)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(b))
			next.ServeHTTP(w, r)
			return
		}
		// Operations whose meaning is this computer's (its screen, reports kept here) are handled here
		if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/sessions/") {
			seg, op, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/api/sessions/"), "/")
			if h, _ := peer.Split(seg); h != "" {
				switch op {
				case "meta":
					s.hostMeta(w, r, seg)
					return
				case "open":
					s.hostOpen(w, r, seg)
					return
				case "close":
					s.hostClose(w, r, seg)
					return
				}
			}
		}
		if host, path, q := s.hostRoute(r); host != "" {
			if websocket.IsWebSocketUpgrade(r) {
				s.hostWSProxy(w, r, host, path, q)
				return
			}
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

// plainUpgrader: the same origin check as /ws, no permessage-deflate. What a host streams through here is the
// remote screen's JPEG tiles, which don't compress (screenstream.go turns it off for the same reason).
var plainUpgrader = websocket.Upgrader{ReadBufferSize: 4096, WriteBufferSize: 64 << 10, CheckOrigin: upgrader.CheckOrigin}

// hostWSProxy: a WebSocket to another computer (`/api/screen/stream?host=`: driving that computer's screen from
// here). Dialled first, so a host that refuses (screen control off there, offline) answers the browser with a real
// status instead of an upgrade that closes at once.
func (s *Server) hostWSProxy(w http.ResponseWriter, r *http.Request, host, path string, query url.Values) {
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	up, resp, err := s.Peers.DialWS(r.Context(), host, path, false)
	if err != nil {
		code := 502
		if resp != nil && resp.StatusCode >= 400 {
			code = resp.StatusCode
		}
		httpError(w, code, i18n.S("连不上那台电脑：", "Can't reach that computer: ")+err.Error())
		return
	}
	client, err := plainUpgrader.Upgrade(w, r, nil)
	if err != nil {
		up.Close()
		return
	}
	// Counts as a remote device looking (keeps this computer awake) the same way the screen itself would
	if auth.FromRemote(r) && s.Awake != nil {
		defer s.Awake.Hold()()
	}
	peer.Pipe(client, up)
}

// bodyHost reads a JSON body for its "host" (and puts it back for whoever reads it next). nil = it failed (answered).
func bodyHost(w http.ResponseWriter, r *http.Request) ([]byte, string) {
	b, err := io.ReadAll(io.LimitReader(r.Body, 64<<20))
	if err != nil {
		httpError(w, 400, err.Error())
		return nil, ""
	}
	r.Body = io.NopCloser(bytes.NewReader(b))
	var x struct {
		Host string `json:"host"`
	}
	_ = json.Unmarshal(b, &x)
	return b, x.Host
}

// termOnHost: POST /api/terms with "host": a terminal on that computer (its shell runs there). Its id comes back as
// "<host>:<id>", so input, resize, kill and its WebSocket (/api/terms/<host>:<id>/ws) route there like a session's.
func (s *Server) termOnHost(w http.ResponseWriter, r *http.Request, host string, body []byte) {
	var m map[string]any
	_ = json.Unmarshal(body, &m)
	delete(m, "host")
	// "from" is a terminal (split from it: the shell's directory); one on another computer means nothing there
	if f, _ := m["from"].(string); f != "" {
		if h, id := peer.Split(f); h == host {
			m["from"] = id
		} else {
			delete(m, "from")
		}
	}
	b, _ := json.Marshal(m)
	resp, err := s.Peers.Do(r.Context(), host, "POST", "/api/terms", bytes.NewReader(b), http.Header{"Content-Type": {"application/json"}})
	if err != nil {
		httpError(w, 502, i18n.S("连不上那台电脑：", "Can't reach that computer: ")+err.Error())
		return
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == 200 {
		rb = prefixID(host, rb)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(rb)
}

// prefixID: a JSON object's top-level "id" as this computer calls the thing (terminal infos).
func prefixID(host string, b []byte) []byte {
	var m map[string]any
	if json.Unmarshal(b, &m) != nil || m == nil {
		return b
	}
	if id, _ := m["id"].(string); id != "" {
		m["id"] = peer.Join(host, id)
	}
	out, err := json.Marshal(m)
	if err != nil {
		return b
	}
	return out
}
