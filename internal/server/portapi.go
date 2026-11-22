package server

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/gerdr-dev/gerdr/internal/auth"
	"github.com/gerdr-dev/gerdr/internal/i18n"
	"github.com/gerdr-dev/gerdr/internal/portproxy"
)

// Local ports through the relay: a phone opens `https://8765.<remote host>/` for what an agent started on
// 127.0.0.1:8765 here (internal/portproxy; docs/dev/REMOTE.md "Local ports through the relay").

// portProxy builds the proxy over the remote service (nil when this process doesn't run remote access).
func (s *Server) portProxy() *portproxy.Proxy {
	if s.Remote == nil {
		return nil
	}
	return &portproxy.Proxy{
		Domain: s.Remote.PortDomain,
		// Gerdr's own ports: proxying itself would put the agent's page and Gerdr on one origin after all
		Deny:   func(p int) bool { return p == s.Port || p == s.Remote.Config().LanPort },
		Auth:   s.Remote.Auth,
		Reauth: s.reauthNeeded,
	}
}

// withPorts sends requests for a port subdomain to the proxy before guard: none of the main site's rules or
// security headers (its CSP would break the proxied page) apply there, the proxy signs people in itself. Only
// requests through the remote listeners: on this computer localhost is already the right address.
func (s *Server) withPorts(next http.Handler) http.Handler {
	if s.ports == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth.FromRemote(r) {
			if _, ok := s.ports.Port(r.Host); ok {
				s.ports.ServeHTTP(w, r)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// portEntry is the main site's door `/_port/<port>?to=/x`: a signed-in device gets a ticket and goes on to
// `https://<port>.<host>/_gerdr/auth?t=…`. Not signed in, remoteGuard shows the pairing page here, which comes back
// to this address once in (pairpage.html enter). On this computer it just goes to localhost.
func (s *Server) portEntry(w http.ResponseWriter, r *http.Request) {
	port, err := strconv.Atoi(r.PathValue("port"))
	to := portproxy.SafePath(r.URL.Query().Get("to"))
	if err != nil || s.ports == nil || !s.ports.Allowed(port) {
		httpError(w, http.StatusNotFound, i18n.S("这个端口不代理", "This port isn't proxied"))
		return
	}
	if !auth.FromRemote(r) {
		http.Redirect(w, r, fmt.Sprintf("http://localhost:%d%s", port, to), http.StatusFound)
		return
	}
	id := s.Remote.Auth.Authenticate(r)
	if id == nil || s.ports.URL(port, "") == "" {
		httpError(w, http.StatusNotFound, i18n.S("端口代理没开（还没有覆盖子域名的证书）", "The port proxy is off (no certificate covering the subdomains yet)"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, s.ports.Back(port, s.ports.Mint(id.Device.ID, port), to), http.StatusFound)
}

// portLink: `POST /api/port-link {port, to}` → the subdomain address with a ticket in it. For the phone Apps, which
// open links in the system browser: that browser has no device cookie, so the redirect through /_port/ would end on
// the pairing page; the ticket (60 s, single use, this port only) signs it into that one subdomain instead.
// Through the remote listeners only; on this computer it is just localhost.
func (s *Server) portLink(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Port int    `json:"port"`
		To   string `json:"to"`
	}
	if err := decode(r, &in); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	to := portproxy.SafePath(in.To)
	if s.ports == nil || !s.ports.Allowed(in.Port) {
		httpError(w, http.StatusNotFound, i18n.S("这个端口不代理", "This port isn't proxied"))
		return
	}
	if !auth.FromRemote(r) {
		writeJSON(w, map[string]any{"url": fmt.Sprintf("http://localhost:%d%s", in.Port, to)})
		return
	}
	id := s.Remote.Auth.Authenticate(r)
	if id == nil || s.ports.URL(in.Port, "") == "" {
		httpError(w, http.StatusNotFound, i18n.S("端口代理没开（还没有覆盖子域名的证书）", "The port proxy is off (no certificate covering the subdomains yet)"))
		return
	}
	writeJSON(w, map[string]any{"url": s.ports.Back(in.Port, s.ports.Mint(id.Device.ID, in.Port), to)})
}
