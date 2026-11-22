package portproxy

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
)

type rig struct {
	p       *Proxy
	store   *auth.Store
	port    int
	host    string
	gotHost string
	gotCk   string
}

func newRig(t *testing.T) *rig {
	g := &rig{}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.gotHost, g.gotCk = r.Host, r.Header.Get("Cookie")
		if r.URL.Path == "/go" {
			http.Redirect(w, r, "http://localhost:"+strconv.Itoa(g.port)+"/there", http.StatusFound)
			return
		}
		io.WriteString(w, "hello from "+r.URL.Path)
	}))
	t.Cleanup(up.Close)
	_, ps, _ := net.SplitHostPort(up.Listener.Addr().String())
	g.port, _ = strconv.Atoi(ps)
	st, err := auth.New(auth.Config{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	g.store = st
	g.p = &Proxy{Domain: func() string { return "h.test" }, Auth: st, Deny: func(p int) bool { return p == 7788 }}
	g.host = strconv.Itoa(g.port) + ".h.test"
	return g
}

func (g *rig) do(path, cookie string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, "https://"+g.host+path, nil)
	if cookie != "" {
		r.AddCookie(&http.Cookie{Name: CookieName, Value: cookie})
		r.AddCookie(&http.Cookie{Name: "app", Value: "keep"}) // the proxied server's own cookie
	}
	w := httptest.NewRecorder()
	g.p.ServeHTTP(w, r)
	return w
}

func (g *rig) login(t *testing.T, device string) string {
	tk := g.p.Mint(device, g.port)
	w := g.do(AuthPath+"?t="+tk+"&to="+url.QueryEscape("/x?y=1"), "")
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/x?y=1" {
		t.Fatalf("ticket exchange: %d %s", w.Code, w.Header().Get("Location"))
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == CookieName {
			if !c.Secure || !c.HttpOnly || c.Path != "/" || c.Domain != "" {
				t.Fatalf("the subdomain cookie must be Secure + HttpOnly + Path=/ + no Domain: %+v", c)
			}
			return c.Value
		}
	}
	t.Fatal("no cookie")
	return ""
}

// The whole way: not signed in → the main site mints a ticket → ticket for a cookie → proxied to 127.0.0.1:<port>.
func TestFlow(t *testing.T) {
	g := newRig(t)
	d, _ := g.store.NewDevice("test", "1.2.3.4", "")

	w := g.do("/a/b?c=1", "")
	if w.Code != http.StatusFound || !strings.HasPrefix(w.Header().Get("Location"), "https://h.test/_port/"+strconv.Itoa(g.port)+"?to=") {
		t.Fatalf("not signed in should go to the main site for a ticket: %d %s", w.Code, w.Header().Get("Location"))
	}

	ck := g.login(t, d.ID)
	w = g.do("/page", ck)
	if w.Code != 200 || w.Body.String() != "hello from /page" {
		t.Fatalf("proxy: %d %q", w.Code, w.Body.String())
	}
	if g.gotHost != "localhost:"+strconv.Itoa(g.port) {
		t.Errorf("Host should be localhost (what dev servers accept): %q", g.gotHost)
	}
	if strings.Contains(g.gotCk, CookieName) || !strings.Contains(g.gotCk, "app=keep") {
		t.Errorf("our cookie must never reach the proxied server, its own must: %q", g.gotCk)
	}

	// The dev server redirects to the address it thinks it has → back to the subdomain
	w = g.do("/go", ck)
	if got := w.Header().Get("Location"); got != "https://"+g.host+"/there" {
		t.Errorf("Location not mapped back: %q", got)
	}
}

// Tickets are single use and bound to a port; a revoked device loses its subdomains at once.
func TestTicketAndRevoke(t *testing.T) {
	g := newRig(t)
	d, _ := g.store.NewDevice("test", "1.2.3.4", "")

	other := g.p.Mint(d.ID, g.port+1)
	if w := g.do(AuthPath+"?t="+other, ""); len(w.Result().Cookies()) != 0 {
		t.Fatal("another port's ticket must not work here")
	}
	tk := g.p.Mint(d.ID, g.port)
	g.do(AuthPath+"?t="+tk, "")
	if w := g.do(AuthPath+"?t="+tk, ""); len(w.Result().Cookies()) != 0 {
		t.Fatal("a ticket works once")
	}

	ck := g.login(t, d.ID)
	g.store.Revoke(d.ID)
	if w := g.do("/page", ck); w.Code != http.StatusFound {
		t.Fatalf("a revoked device's subdomain session must end at once: %d", w.Code)
	}

	// Due to verify again: likewise
	d2, _ := g.store.NewDevice("test", "1.2.3.4", "")
	ck = g.login(t, d2.ID)
	g.p.Reauth = func(*auth.Ident) bool { return true }
	if w := g.do("/page", ck); w.Code != http.StatusFound {
		t.Fatalf("due to verify again, the subdomain must stop too: %d", w.Code)
	}
}

func TestPortAndSafePath(t *testing.T) {
	p := &Proxy{Domain: func() string { return "h.test" }, Deny: func(p int) bool { return p == 7788 }}
	for host, want := range map[string]bool{
		"7830.h.test": true, "7830.h.test:443": true, "7830.H.test": true, "7788.h.test": false, "80.h.test": false,
		"07830.h.test": false, "a.7830.h.test": false, "h.test": false, "7830.evil.test": false, "7830.h.test:7878": false,
	} {
		if _, ok := p.Port(host); ok != want {
			t.Errorf("Port(%q) = %v, want %v", host, ok, want)
		}
	}
	// Direct access with a port in the address: the subdomains carry the same port
	d := &Proxy{Domain: func() string { return "h.test:7878" }}
	for host, want := range map[string]bool{"7830.h.test:7878": true, "7830.h.test": false, "7830.h.test:443": false} {
		if _, ok := d.Port(host); ok != want {
			t.Errorf("direct Port(%q) = %v, want %v", host, ok, want)
		}
	}
	if (&Proxy{Domain: func() string { return "" }}).URL(8000, "/") != "" {
		t.Error("off: no URL")
	}
	for in, want := range map[string]string{
		"/x?y=1": "/x?y=1", "//evil.com/x": "/", "https://evil.com": "/", "/\\evil.com": "/", "": "/",
	} {
		if got := SafePath(in); got != want {
			t.Errorf("SafePath(%q) = %q, want %q", in, got, want)
		}
	}
}

// Cross-origin requests from other sites (even same-site ones, another subdomain of the same domain) are refused:
// dev servers often answer any Origin with credentialed CORS, and the Lax cookie rides same-site requests.
func TestForeignOriginRejected(t *testing.T) {
	g := newRig(t)
	d, _ := g.store.NewDevice("test", "1.2.3.4", "")
	ck := g.login(t, d.ID)
	try := func(origin string) int {
		r := httptest.NewRequest(http.MethodGet, "https://"+g.host+"/page", nil)
		r.AddCookie(&http.Cookie{Name: CookieName, Value: ck})
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		g.p.ServeHTTP(w, r)
		return w.Code
	}
	for origin, want := range map[string]int{
		"":                     200, // the address bar
		"https://" + g.host:    200, // itself
		"https://7831.h.test":  200, // another port (a front end calling its API)
		"https://blog.h.test":  403, // another site on the same domain
		"https://h.test":       403, // Gerdr itself has no business here
		"https://evil.example": 403,
		"http://" + g.host:     403, // not https
	} {
		if got := try(origin); got != want {
			t.Errorf("Origin %q: %d, want %d", origin, got, want)
		}
	}
}
