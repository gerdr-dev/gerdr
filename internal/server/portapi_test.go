package server

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/gerdr-dev/gerdr/internal/auth"
	"github.com/gerdr-dev/gerdr/internal/portproxy"
)

// Local ports through the relay (portapi.go): a port subdomain goes to the proxy, not to Gerdr; the main site's
// /_port/<port> mints the ticket for a signed-in device and shows the pairing page to anyone else.
func TestPortProxyRoutes(t *testing.T) {
	s, lh, rh := remoteServer(t)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "dev server "+r.URL.Path) }))
	defer up.Close()
	_, ps, _ := net.SplitHostPort(up.Listener.Addr().String())
	port, _ := strconv.Atoi(ps)
	sub := ps + "." + testHost

	// No certificate covering the subdomains: the proxy is off, the name is just a wrong Host
	if w := do(rh, "GET", "/", nil, host(sub)); w.Code != http.StatusMisdirectedRequest {
		t.Fatalf("proxy off: a port subdomain should be refused like any other Host, got %d", w.Code)
	}
	s.ports.Domain = func() string { return testHost }

	w := do(rh, "GET", "/x?y=1", nil, host(sub))
	if w.Code != 302 || w.Header().Get("Location") != "https://"+testHost+"/_port/"+ps+"?to="+url.QueryEscape("/x?y=1") {
		t.Fatalf("not signed in on the subdomain should go to the main site: %d %q", w.Code, w.Header().Get("Location"))
	}
	if w.Header().Get("Content-Security-Policy") != "" {
		t.Error("the main site's CSP must not land on the proxied page")
	}
	if w := do(rh, "GET", "/_port/"+ps+"?to=/x", nil); w.Code != 200 || !strings.Contains(w.Body.String(), "配对码") {
		t.Fatalf("not paired: /_port/ should show the pairing page, got %d", w.Code)
	}

	_, tok := s.Remote.Auth.NewDevice("test", "203.0.113.9", "")
	dev := &http.Cookie{Name: auth.CookieName, Value: tok}
	w = do(rh, "GET", "/_port/"+ps+"?to=/x", nil, cookie(dev))
	loc, _ := url.Parse(w.Header().Get("Location"))
	if w.Code != 302 || loc == nil || loc.Host != sub || loc.Path != portproxy.AuthPath {
		t.Fatalf("signed in: should go on to the subdomain with a ticket, got %d %q", w.Code, w.Header().Get("Location"))
	}
	w = do(rh, "GET", loc.RequestURI(), nil, host(sub))
	var pc *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == portproxy.CookieName {
			pc = c
		}
	}
	if w.Code != 302 || pc == nil || w.Header().Get("Location") != "/x" {
		t.Fatalf("ticket exchange: %d %q", w.Code, w.Header().Get("Location"))
	}
	if w := do(rh, "GET", "/x", nil, host(sub), cookie(pc)); w.Code != 200 || w.Body.String() != "dev server /x" {
		t.Fatalf("proxied: %d %q", w.Code, w.Body.String())
	}
	// The device cookie alone (it's host-only and never sent here anyway) doesn't open a subdomain
	if w := do(rh, "GET", "/x", nil, host(sub), cookie(dev)); w.Code != 302 {
		t.Fatalf("only the subdomain's own cookie counts, got %d", w.Code)
	}

	// The page learns where ports show up; only through the relay
	if w := do(rh, "GET", "/api/hello?only=portDomain", nil, cookie(dev)); !strings.Contains(w.Body.String(), `"portDomain":"`+testHost+`"`) {
		t.Fatalf("hello through the relay should carry portDomain: %s", w.Body)
	}
	r := httptest.NewRequest("GET", "http://127.0.0.1:7870/api/hello?only=portDomain", nil)
	r.Header.Set("Authorization", "Bearer "+s.Token)
	lw := httptest.NewRecorder()
	lh.ServeHTTP(lw, r)
	if strings.Contains(lw.Body.String(), "portDomain") {
		t.Fatalf("on this computer localhost is right: no portDomain, got %s", lw.Body)
	}

	// Gerdr's own port is never proxied
	s.Port = port
	if w := do(rh, "GET", "/x", nil, host(sub), cookie(pc)); w.Code != http.StatusMisdirectedRequest {
		t.Fatalf("Gerdr's own port must not be proxied, got %d", w.Code)
	}
}

// The Apps' way in (system browser, no device cookie): a ticketed subdomain address minted with the page's cookie.
func TestPortLinkForApps(t *testing.T) {
	s, _, rh := remoteServer(t)
	s.ports.Domain = func() string { return testHost }
	_, tok := s.Remote.Auth.NewDevice("test", "203.0.113.9", "")
	dev := &http.Cookie{Name: auth.CookieName, Value: tok}
	if w := do(rh, "POST", "/api/port-link", body(`{"port":8765,"to":"/x"}`)(), cookie(dev)); w.Code != 403 {
		t.Fatalf("a write without X-Gerdr must be refused (CSRF), got %d", w.Code)
	}
	if w := do(rh, "POST", "/api/port-link", body(`{"port":8765,"to":"/x"}`)(), csrf); w.Code != 401 {
		t.Fatalf("not paired: no ticket, got %d", w.Code)
	}
	w := do(rh, "POST", "/api/port-link", body(`{"port":8765,"to":"//evil.com/x"}`)(), cookie(dev), csrf)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"url":"https://8765.`+testHost+portproxy.AuthPath+"?t=") || !strings.Contains(w.Body.String(), "to=%2F\"") {
		t.Fatalf("port link: %d %s", w.Code, w.Body)
	}
}
