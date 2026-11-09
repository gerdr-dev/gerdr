package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The App's window link: a code from POST /api/auth/code logs in once; spent, it still goes on for a window that has
// the cookie (later windows open at the same link), and gets nobody else in. Remote: no codes at all.
func TestLoginCode(t *testing.T) {
	s, h, rh := remoteServer(t)
	local := func(method, path string, opts ...reqOpt) *httptest.ResponseRecorder {
		return do(h, method, path, nil, append([]reqOpt{func(r *http.Request) { r.RemoteAddr = "127.0.0.1:5000"; r.Host = "127.0.0.1:7870" }}, opts...)...)
	}
	bearer := hdr("Authorization", "Bearer "+s.Token)
	if w := local("POST", "/api/auth/code", bearer); w.Code != http.StatusForbidden {
		t.Fatalf("without X-Gerdr: %d", w.Code)
	}
	if w := local("POST", "/api/auth/code", hdr("X-Gerdr", "1")); w.Code != http.StatusUnauthorized {
		t.Fatalf("without the token: %d", w.Code)
	}
	w := local("POST", "/api/auth/code", bearer, hdr("X-Gerdr", "1"))
	var out struct{ Code string }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || len(out.Code) != 32 {
		t.Fatalf("code: %d %s", w.Code, w.Body)
	}

	w = local("GET", "/auth?code="+out.Code+"&next=/x")
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/x" || len(w.Result().Cookies()) != 1 {
		t.Fatalf("first use: %d %v", w.Code, w.Header())
	}
	c := w.Result().Cookies()[0]
	if w := local("GET", "/auth?code="+out.Code); w.Code != http.StatusUnauthorized {
		t.Fatalf("spent, no cookie: %d", w.Code)
	}
	if w := local("GET", "/auth?code="+out.Code+"&next=/y", cookie(c)); w.Code != http.StatusFound || w.Header().Get("Location") != "/y" {
		t.Fatalf("spent, with the cookie: %d", w.Code)
	}
	if w := local("GET", "/auth?code=nope"); w.Code != http.StatusUnauthorized {
		t.Fatalf("made up: %d", w.Code)
	}
	if w := local("GET", "/auth?token="+s.Token); w.Code != http.StatusFound {
		t.Fatalf("the token still works: %d", w.Code)
	}

	fresh := s.codes.issue()
	if w := do(rh, "GET", "/auth?code="+fresh, nil); w.Code == http.StatusFound {
		t.Fatalf("remote /auth with a code: %d", w.Code)
	}
	if w := do(rh, "POST", "/api/auth/code", nil, bearer, hdr("X-Gerdr", "1")); w.Code == 200 {
		t.Fatalf("remote code: %d", w.Code)
	}
}

func TestLoginCodeExpires(t *testing.T) {
	var c loginCodes
	code := c.issue()
	c.mu.Lock()
	for k := range c.m {
		c.m[k] = c.m[k].Add(-2 * loginCodeTTL)
	}
	c.mu.Unlock()
	if c.spend(code) {
		t.Fatal("an expired code was taken")
	}
}
