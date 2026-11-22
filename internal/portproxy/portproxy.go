// Package portproxy serves what agents start on this computer (`http://127.0.0.1:8765`) at
// `https://8765.<remote host>/`, so a phone using Gerdr through the relay can open it, behind Gerdr's own sign-in.
//
// Ported from herdr-web's internal/portproxy, where this has run since 2026-10; the design and the traps are in
// docs/dev/REMOTE.md "Local ports through the relay". Three rules carry the weight:
//
//  1. **One subdomain per port, never the main site's origin.** The proxied page is the agent's, running any JS;
//     on Gerdr's origin it could call /api/ with the device cookie and type into any session. A subdomain is
//     another origin, and the main site's device cookie is host-only, so it is never even sent here. (A path
//     prefix on the main site was rejected for that, and because dev servers' absolute paths and HMR break there.)
//  2. **The subdomain signs people in by borrowing from the main site**: not signed in → the main site's
//     `/_port/<port>`, which runs its own checks and mints a 60 s, single-use ticket bound to that port → back here
//     to trade it for a cookie valid on this subdomain only. **Every request checks the device again**
//     (auth.IdentByID): a revoked device, or one due to re-verify, loses the subdomains at once.
//  3. **That cookie is never passed on to the proxied server** (it is a credential for this subdomain, and the
//     server is the agent's).
//
// Tickets and sessions live in memory only: after a restart the subdomain goes round the main site once more, and
// with the main site's cookie still there nobody notices.
package portproxy

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gerdr-dev/gerdr/internal/auth"
	"github.com/gerdr-dev/gerdr/internal/i18n"
)

// CookieName is the subdomain's session cookie. The `__Host-` prefix makes the browser keep it Secure, Path=/ and
// without Domain (it belongs to this one subdomain): another subdomain can neither set nor shadow it.
const CookieName = "__Host-gerdr_port"

// AuthPath is where a subdomain trades a ticket for its cookie: a path no dev server uses.
const AuthPath = "/_gerdr/auth"

// EntryPath is the main site's door that mints tickets: `/_port/<port>?to=<path>`.
const EntryPath = "/_port/"

const (
	ticketTTL  = 60 * time.Second
	sessionTTL = 12 * time.Hour
)

type Proxy struct {
	// Domain is the name ports hang under (`8765.<Domain>`), host[:port] as phones open the main site; "" = off
	// (no certificate covering the subdomains yet, or remote access off). A func: it changes without a restart.
	Domain func() string
	// Deny: ports never proxied (Gerdr's own: proxying itself would undo the separate-origin rule).
	Deny func(port int) bool
	Auth *auth.Store
	// Reauth: whether this device is due to verify again (the main site's rule, server.reauthNeeded).
	Reauth func(*auth.Ident) bool

	mu       sync.Mutex
	tickets  map[string]grant
	sessions map[string]grant
}

type grant struct {
	device string
	port   int
	exp    time.Time
}

// split cuts a Host header into the name and the port, with 443 (the default for https) as no port.
func split(h string) (name, port string) {
	h = strings.ToLower(strings.TrimSuffix(h, "."))
	if n, p, err := net.SplitHostPort(h); err == nil {
		name, port = strings.TrimSuffix(n, "."), p
	} else {
		name = h
	}
	if port == "443" {
		port = ""
	}
	return name, port
}

// Port reads a port subdomain off a Host header: `8765.<Domain>` → 8765. Only one plain number label counts, no
// deeper levels, no "08765" / "+8765".
func (p *Proxy) Port(host string) (int, bool) {
	if p == nil || p.Domain == nil {
		return 0, false
	}
	d := p.Domain()
	if d == "" {
		return 0, false
	}
	dn, dp := split(d)
	hn, hp := split(host)
	if hp != dp {
		return 0, false
	}
	label, ok := strings.CutSuffix(hn, "."+dn)
	if !ok || label == "" {
		return 0, false
	}
	n, err := strconv.Atoi(label)
	if err != nil || strconv.Itoa(n) != label {
		return 0, false
	}
	return n, p.Allowed(n)
}

// Allowed: not below 1024 (system services) and not Gerdr's own ports.
//
// Why not only ports a session is listening on: whoever gets this far has passed the main site's sign-in, and
// behind the main site is a shell; any local port is a curl away for them. The limits guard against slips and
// against "going round to ourselves" (proxying Gerdr would void the separate-origin rule).
func (p *Proxy) Allowed(port int) bool {
	return port >= 1024 && port <= 65535 && (p.Deny == nil || !p.Deny(port))
}

// URL is a port's address on its subdomain ("" when the proxy is off).
func (p *Proxy) URL(port int, path string) string {
	d := p.Domain()
	if d == "" {
		return ""
	}
	return fmt.Sprintf("https://%d.%s%s", port, d, path)
}

func randHex() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (p *Proxy) gcLocked(now time.Time) {
	for k, t := range p.tickets {
		if now.After(t.exp) {
			delete(p.tickets, k)
		}
	}
	for k, s := range p.sessions {
		if now.After(s.exp) {
			delete(p.sessions, k)
		}
	}
}

// Mint: the main site gives a signed-in device a ticket into port (60 s, single use, that port only).
func (p *Proxy) Mint(device string, port int) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.tickets == nil {
		p.tickets = map[string]grant{}
	}
	now := time.Now()
	p.gcLocked(now)
	t := randHex()
	p.tickets[t] = grant{device: device, port: port, exp: now.Add(ticketTTL)}
	return t
}

func (p *Proxy) redeem(t string, port int) (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	for k, v := range p.tickets {
		if subtle.ConstantTimeCompare([]byte(k), []byte(t)) == 1 {
			delete(p.tickets, k) // single use: a wrong port spends it too, nothing left to try again with
			if v.port != port || now.After(v.exp) {
				return "", false
			}
			if p.sessions == nil {
				p.sessions = map[string]grant{}
			}
			s := randHex()
			p.sessions[s] = grant{device: v.device, port: port, exp: now.Add(sessionTTL)}
			return s, true
		}
	}
	return "", false
}

// authed: the request's subdomain session still holds — it exists, is for this port, its device is still there
// and not due to verify again.
func (p *Proxy) authed(r *http.Request, port int) bool {
	c, err := r.Cookie(CookieName)
	if err != nil || c.Value == "" {
		return false
	}
	p.mu.Lock()
	s, ok := p.sessions[c.Value]
	p.mu.Unlock()
	if !ok || s.port != port || time.Now().After(s.exp) {
		return false
	}
	id := p.Auth.IdentByID(s.device)
	if id == nil {
		return false // revoked / expired: its subdomains go with it
	}
	return p.Reauth == nil || !p.Reauth(id)
}

// SafePath lets through only a path on this site: starting with /, not // or /\ (protocol-relative, another site).
func SafePath(to string) string {
	if !strings.HasPrefix(to, "/") || strings.HasPrefix(to, "//") || strings.HasPrefix(to, "/\\") {
		return "/"
	}
	return to
}

// ServeHTTP answers a request on a port subdomain (the caller has matched the Host with Port).
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	port, ok := p.Port(r.Host)
	if !ok {
		http.Error(w, i18n.S("这个端口不代理", "This port isn't proxied"), http.StatusForbidden)
		return
	}
	w.Header().Set("Referrer-Policy", "no-referrer") // the ticket is in the URL: keep it out of Referer to other sites

	if r.URL.Path == AuthPath {
		s, ok := p.redeem(r.URL.Query().Get("t"), port)
		if !ok {
			// Expired / used: start over (with the main site's cookie still there, nobody notices)
			http.Redirect(w, r, p.entry(port, SafePath(r.URL.Query().Get("to"))), http.StatusFound)
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name: CookieName, Value: s, Path: "/",
			// Lax, not Strict: a link opened from another app (a chat, the system browser's history) must carry it
			// too, or every visit goes round the main site. Guarding writes is the proxied server's own business
			Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode,
			MaxAge: int(sessionTTL / time.Second),
		})
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, SafePath(r.URL.Query().Get("to")), http.StatusFound)
		return
	}

	// Cross-origin requests from other sites are refused (an Origin that isn't one of our port subdomains).
	//
	// Why: dev servers often answer any Origin with `Access-Control-Allow-Origin: <it>` + `Allow-Credentials: true`,
	// and the subdomain cookie is Lax, so a same-site page (another site under the same registrable domain) sends
	// it. Together: any page there could read your local dev server. Ports calling each other (a front end on 5173
	// calling its API on 8080) still work. Requests without an Origin (the address bar, plain GET subresources)
	// aren't affected.
	if o := r.Header.Get("Origin"); o != "" && !p.ownOrigin(o) {
		http.Error(w, i18n.S("跨站请求被拒", "Cross-site request refused"), http.StatusForbidden)
		return
	}

	if !p.authed(r, port) {
		w.Header().Set("Cache-Control", "no-store")
		if r.Method == http.MethodGet && r.Header.Get("Upgrade") == "" {
			http.Redirect(w, r, p.entry(port, r.URL.RequestURI()), http.StatusFound)
			return
		}
		// A subresource / WebSocket can't follow a redirect (nobody would see it): refuse, the page itself takes
		// the person to sign in
		http.Error(w, i18n.S("先在 Gerdr 上登录", "Sign in to Gerdr first"), http.StatusUnauthorized)
		return
	}
	p.proxy(port).ServeHTTP(w, r)
}

// ownOrigin: the Origin is one of our port subdomains (https://<port>.<Domain>).
func (p *Proxy) ownOrigin(o string) bool {
	u, err := url.Parse(o)
	if err != nil || u.Scheme != "https" {
		return false
	}
	_, ok := p.Port(u.Host)
	return ok
}

// entry is the main site's door that mints a ticket for this port.
func (p *Proxy) entry(port int, to string) string {
	return fmt.Sprintf("https://%s%s%d?to=%s", p.Domain(), EntryPath, port, url.QueryEscape(to))
}

// Back is where the main site sends a device with its ticket.
func (p *Proxy) Back(port int, ticket, to string) string {
	return p.URL(port, AuthPath+"?t="+url.QueryEscape(ticket)+"&to="+url.QueryEscape(SafePath(to)))
}

func (p *Proxy) proxy(port int) http.Handler {
	target := &url.URL{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", port)}
	local := fmt.Sprintf("localhost:%d", port)
	public := strings.TrimSuffix(p.URL(port, ""), "/")
	return &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.SetXForwarded()
			// Host as localhost: dev servers like Vite / Next refuse Hosts they don't know (Vite 5 on: "Blocked
			// request. This host is not allowed") and know localhost
			pr.Out.Host = local
			// Origin likewise (Vite's HMR WebSocket checks it). Only our own subdomain's; others pass unchanged
			if pr.Out.Header.Get("Origin") == public {
				pr.Out.Header.Set("Origin", "http://"+local)
			}
			stripCookie(pr.Out, CookieName)
		},
		ModifyResponse: func(resp *http.Response) error {
			// A dev server's redirect names the address it thinks it has (http://localhost:8765/x): back to the subdomain
			if loc := resp.Header.Get("Location"); loc != "" {
				for _, pre := range []string{"http://" + local, fmt.Sprintf("http://127.0.0.1:%d", port)} {
					if rest, ok := strings.CutPrefix(loc, pre); ok {
						resp.Header.Set("Location", public+rest)
					}
				}
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusBadGateway)
			fmt.Fprintf(w, `<!doctype html><meta charset=utf-8><meta name=viewport content="width=device-width">`+
				`<body style="font:15px system-ui;padding:24px;background:#111;color:#ddd">`+
				`<p>%s</p><p style="color:#888">%s</p>`,
				html.EscapeString(fmt.Sprintf(i18n.S("这台电脑的 127.0.0.1:%d 上没有服务在听。", "Nothing is listening on 127.0.0.1:%d on this computer."), port)),
				html.EscapeString(err.Error()))
		},
	}
}

// stripCookie takes our own cookie out of the Cookie header sent to the proxied server.
func stripCookie(r *http.Request, name string) {
	cs := r.Cookies()
	r.Header.Del("Cookie")
	for _, c := range cs {
		if c.Name != name {
			r.AddCookie(c)
		}
	}
}
