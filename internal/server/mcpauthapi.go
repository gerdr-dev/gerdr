package server

import (
	"context"
	"html/template"
	"net/http"
	"sync"
	"time"

	"github.com/gerdr-dev/gerdr/internal/agents/gerdrnative"
	"github.com/gerdr-dev/gerdr/internal/ext"
	"github.com/gerdr-dev/gerdr/internal/mcpauth"
	"github.com/gerdr-dev/gerdr/native"
)

// MCP OAuth (Settings → MCP): sign-in state of every remote MCP server gerdr knows about — gerdr's
// own list plus the ones configured in Claude Code (~/.claude.json, user level) — and the
// Authorize / Sign out buttons. How tokens are picked is in internal/mcpauth.

type mcpAuthView struct {
	mcpauth.Status
	From string `json:"from"` // gerdr | claude: whose config lists the server
}

// remoteMCP: every remote server, gerdr's first; the same name + URL in both lists shows once.
func (s *Server) remoteMCP() []mcpAuthView {
	seen := map[string]bool{}
	var out []mcpAuthView
	add := func(from string, list []ext.MCPServer) {
		for _, m := range list {
			if !m.Remote() || m.URL == "" || seen[m.Name+"\x00"+m.URL] {
				continue
			}
			seen[m.Name+"\x00"+m.URL] = true
			out = append(out, mcpAuthView{Status: mcpauth.Status{Name: m.Name, URL: m.URL}, From: from})
		}
	}
	if s.Ext != nil {
		add("gerdr", s.Ext.Settings().MCP)
	}
	add("claude", ext.ClaudeMCP(""))
	return out
}

// findMCP: a server by name (and url, when given) in gerdr's list, then Claude Code's, then Codex's.
func (s *Server) findMCP(name, u string) (ext.MCPServer, bool) {
	var lists [][]ext.MCPServer
	if s.Ext != nil {
		lists = append(lists, s.Ext.Settings().MCP)
	}
	lists = append(lists, ext.ClaudeMCP(""), ext.CodexMCP())
	for _, l := range lists {
		for _, m := range l {
			if m.Name == name && (u == "" || m.URL == u) {
				return m, true
			}
		}
	}
	return ext.MCPServer{}, false
}

func (s *Server) findRemoteMCP(name, u string) (ext.MCPServer, bool) {
	var lists [][]ext.MCPServer
	if s.Ext != nil {
		lists = append(lists, s.Ext.Settings().MCP)
	}
	lists = append(lists, ext.ClaudeMCP(""))
	for _, l := range lists {
		for _, m := range l {
			if m.Name == name && m.Remote() && (u == "" || m.URL == u) {
				return m, true
			}
		}
	}
	return ext.MCPServer{}, false
}

func (s *Server) mcpAuthRoutes(mux *http.ServeMux) {
	st := mcpauth.Default
	if st == nil {
		return
	}
	mux.HandleFunc("GET /api/mcp/auth", func(w http.ResponseWriter, r *http.Request) {
		maxAge := 2 * time.Minute
		if r.URL.Query().Get("refresh") == "1" {
			maxAge = 0
		}
		list := s.remoteMCP()
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		var wg sync.WaitGroup
		for i := range list {
			m, _ := s.findRemoteMCP(list[i].Name, list[i].URL)
			wg.Add(1)
			go func() {
				defer wg.Done()
				list[i].Status = st.Check(ctx, m.Name, m.URL, m.Headers, maxAge)
			}()
		}
		wg.Wait()
		writeJSON(w, nz(list))
	})
	mux.HandleFunc("POST /api/mcp/auth/{name}/start", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			URL string `json:"url"`
		}
		_ = decode(r, &b)
		m, ok := s.findRemoteMCP(r.PathValue("name"), b.URL)
		if !ok {
			httpError(w, 404, "no such remote MCP server")
			return
		}
		scheme := "http"
		if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
			scheme = "https"
		}
		u, err := st.Begin(r.Context(), m.Name, m.URL, scheme+"://"+r.Host+"/mcp-oauth/callback")
		if err != nil {
			httpError(w, 502, err.Error())
			return
		}
		writeJSON(w, map[string]string{"url": u})
	})
	// Reconnect one server: drop gerdr's own connections to it (native agents, Work functions) and connect again to report
	// how it went, and ask every live Claude Code process to reconnect it too (mcp_reconnect). Remote or local alike.
	mux.HandleFunc("POST /api/mcp/{name}/reconnect", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			URL string `json:"url"`
		}
		_ = decode(r, &b)
		m, ok := s.findMCP(r.PathValue("name"), b.URL)
		if !ok {
			httpError(w, 404, "no such MCP server")
			return
		}
		if m.Remote() {
			st.Invalidate(m.Name, m.URL)
		}
		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()
		res := map[string]any{}
		if tools, err := native.ReconnectMCP(ctx, gerdrnative.MCPServer(m)); err != nil {
			res["error"] = err.Error()
		} else {
			res["tools"] = tools
		}
		if s.Hub != nil {
			res["agents"] = s.Hub.ReconnectMCP(ctx, m.Name)
		}
		writeJSON(w, res)
	})
	mux.HandleFunc("DELETE /api/mcp/auth/{name}", func(w http.ResponseWriter, r *http.Request) {
		if err := st.Logout(r.PathValue("name")); err != nil {
			httpError(w, 500, err.Error())
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	})
	// Outside /api on purpose: the authorization server redirects here cross-site, so the
	// SameSite=Strict login cookie isn't sent. The one-time state (random, held in memory for
	// 15 minutes) is what ties the request to an authorization this gerdr started.
	mux.HandleFunc("GET /mcp-oauth/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		var name string
		var err error
		if e := q.Get("error"); e != "" {
			err = errString(e + " " + q.Get("error_description"))
		} else {
			name, err = st.Finish(r.Context(), q.Get("state"), q.Get("code"))
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'")
		msg := ""
		if err != nil {
			w.WriteHeader(400)
			msg = err.Error()
		}
		_ = callbackPage.Execute(w, map[string]string{"Name": name, "Error": msg})
	})
}

type errString string

func (e errString) Error() string { return string(e) }

var callbackPage = template.Must(template.New("cb").Parse(`<!doctype html><meta charset="utf-8"><title>gerdr · MCP</title>
<style>body{font:15px/1.6 system-ui,sans-serif;display:grid;place-items:center;height:100vh;margin:0;color:#333}@media(prefers-color-scheme:dark){body{background:#111;color:#ddd}}</style>
<div>{{if .Error}}<p>Authorization failed: {{.Error}}</p><p>Go back to gerdr → Settings → Agent → MCP and try again.</p>
{{else}}<p>Signed in to <b>{{.Name}}</b>. You can close this window and go back to gerdr.</p><script>setTimeout(()=>window.close(),800)</script>{{end}}</div>`))
