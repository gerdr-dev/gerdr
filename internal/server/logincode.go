package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"
)

// One-time login codes: /auth?code=… instead of /auth?token=…, for links that end up where other programs can read
// them. The macOS App opens its first window with `--app=<link>`, a command line any process on the Mac can read
// (`ps`); with the token in it, that is the backend's long-lived key. A code works once, within a minute: by the time
// anyone could read it, the window has spent it. POST /api/auth/code (authorized, so only the token's holder) makes
// one; both it and /auth are local-only (remoteapi.go).

const loginCodeTTL = time.Minute

type loginCodes struct {
	mu sync.Mutex
	m  map[string]time.Time // code → expiry
}

func (c *loginCodes) issue() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	code := hex.EncodeToString(b)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]time.Time{}
	}
	now := time.Now()
	for k, exp := range c.m {
		if now.After(exp) {
			delete(c.m, k)
		}
	}
	c.m[code] = now.Add(loginCodeTTL)
	return code
}

// spend: whether code is a live one, which it no longer is after this.
func (c *loginCodes) spend(code string) bool {
	if code == "" {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, exp := range c.m {
		if subtle.ConstantTimeCompare([]byte(k), []byte(code)) == 1 {
			delete(c.m, k)
			return time.Now().Before(exp)
		}
	}
	return false
}

func (s *Server) authCode(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]string{"code": s.codes.issue()})
}

// auth：/auth?token=…&next=/ or /auth?code=…&next=/ —— 换一个 HttpOnly cookie，然后跳走（令牌不留在地址栏里）。
// A spent code with the cookie already set just goes on: the App opens later windows at the same link.
func (s *Server) auth(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	tok := q.Get("token")
	ok := tok != "" && subtle.ConstantTimeCompare([]byte(tok), []byte(s.Token)) == 1
	if !ok && q.Has("code") {
		ok = s.codes.spend(q.Get("code")) || s.authed(r)
	}
	if !ok {
		time.Sleep(500 * time.Millisecond)
		httpError(w, http.StatusUnauthorized, "bad token")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: s.cookieName(), Value: s.Token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 400 * 24 * 3600})
	next := q.Get("next")
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		next = "/"
	}
	http.Redirect(w, r, next, http.StatusFound)
}
