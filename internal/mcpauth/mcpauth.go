// Package mcpauth signs gerdr in to remote MCP servers that use OAuth (Notion, whale-mcp …).
//
// Two places a token can come from, tried in this order:
//
//  1. Claude Code's own login (macOS keychain "Claude Code-credentials" → mcpOAuth, elsewhere
//     ~/.claude/.credentials.json). If the user already authorized the server in Claude Code and
//     that token still works, gerdr counts as signed in too — no second login. We only read it:
//     refreshing it ourselves could rotate the refresh token out from under Claude Code.
//  2. gerdr's own login, stored in <GERDR_DIR>/mcp-auth/<name>.json (0600) and refreshed here.
//     Used once Claude Code's copy is missing or has expired.
//
// Which one works is decided by probing: an `initialize` POST with the token. 401 on both means
// "needs auth" and Settings → MCP shows an Authorize button that runs the flow in Begin / Finish
// (MCP authorization spec: protected-resource metadata → authorization-server metadata →
// dynamic client registration → authorization code + PKCE, with the RFC 8707 resource parameter).
package mcpauth

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
)

// Default is the store the whole process shares (the native agent's MCP client and the work
// functions read tokens from it). Set by main; nil means OAuth is off.
var Default *Store

const (
	AuthNone    = "none"    // server answered without any credentials
	AuthHeaders = "headers" // a static Authorization header in the server's config did it
	AuthClaude  = "claude"  // Claude Code's token
	AuthGerdr   = "gerdr"   // gerdr's own token

	StateOK        = "ok"
	StateNeedsAuth = "needs_auth"
	StateError     = "error"
)

type Status struct {
	Name    string `json:"name"`
	URL     string `json:"url"`
	State   string `json:"state"`
	Auth    string `json:"auth,omitempty"`
	Error   string `json:"error,omitempty"`
	Pending bool   `json:"pending,omitempty"` // an authorization was started and not finished yet
	// HasOwn: gerdr has its own saved login for this server (Logout can remove it).
	HasOwn    bool  `json:"hasOwn,omitempty"`
	CheckedAt int64 `json:"checkedAt"`
}

type Store struct {
	dir  string
	http *http.Client
	// readClaude reads Claude Code's MCP logins (swapped in tests).
	readClaude func() []claudeEntry

	mu      sync.Mutex
	status  map[string]Status // key(name, url)
	flows   map[string]*flow  // state → flow
	sources map[string]*savingSource
	claude  struct {
		at   time.Time
		list []claudeEntry
	}
}

func New(dir string) *Store {
	return &Store{
		dir:        dir,
		http:       &http.Client{Timeout: 20 * time.Second},
		readClaude: readClaude,
		status:     map[string]Status{},
		flows:      map[string]*flow{},
		sources:    map[string]*savingSource{},
	}
}

func key(name, u string) string { return name + "\x00" + normURL(u) }

func normURL(u string) string { return strings.TrimRight(strings.TrimSpace(u), "/") }

// HasAuthHeader: the server's config already carries credentials, OAuth stays out of the way.
func HasAuthHeader(h map[string]string) bool {
	for k := range h {
		if strings.EqualFold(k, "Authorization") {
			return true
		}
	}
	return false
}

// ---- probing ----

// Check probes the server and caches the result. maxAge > 0 reuses a cached result that young.
func (s *Store) Check(ctx context.Context, name, u string, headers map[string]string, maxAge time.Duration) Status {
	k := key(name, u)
	s.mu.Lock()
	st, ok := s.status[k]
	s.mu.Unlock()
	if ok && st.State != StateOK {
		// A failed check goes stale fast: the user may have just signed in from Claude Code.
		maxAge = min(maxAge, 20*time.Second)
	}
	if ok && maxAge > 0 && time.Since(time.UnixMilli(st.CheckedAt)) < maxAge {
		return s.decorate(st)
	}
	st = s.probeAll(ctx, name, u, headers)
	s.mu.Lock()
	s.status[k] = st
	s.mu.Unlock()
	return s.decorate(st)
}

func (s *Store) decorate(st Status) Status {
	s.mu.Lock()
	for _, f := range s.flows {
		if f.name == st.Name && normURL(f.url) == normURL(st.URL) && time.Since(f.at) < flowTTL {
			st.Pending = true
		}
	}
	s.mu.Unlock()
	if a := s.load(st.Name); a != nil && normURL(a.URL) == normURL(st.URL) {
		st.HasOwn = true
	}
	return st
}

func (s *Store) probeAll(ctx context.Context, name, u string, headers map[string]string) Status {
	st := Status{Name: name, URL: u, CheckedAt: time.Now().UnixMilli()}
	if HasAuthHeader(headers) {
		code, err := s.probe(ctx, u, headers, "")
		switch {
		case err != nil:
			st.State, st.Error = StateError, err.Error()
		case code == 401 || code == 403:
			st.State, st.Error = StateError, fmt.Sprintf("the Authorization header in this server's config was rejected (HTTP %d)", code)
		default:
			st.State, st.Auth = StateOK, AuthHeaders
		}
		return st
	}
	var lastErr error
	try := func(src, tok string) bool {
		if tok == "" {
			return false
		}
		code, err := s.probe(ctx, u, headers, tok)
		if err != nil {
			lastErr = err
			return false
		}
		if code == 401 || code == 403 {
			return false
		}
		st.State, st.Auth = StateOK, src
		return true
	}
	if try(AuthClaude, s.claudeToken(name, u)) {
		return st
	}
	if tok, err := s.ownToken(ctx, name, u); err == nil && try(AuthGerdr, tok) {
		return st
	}
	code, err := s.probe(ctx, u, headers, "")
	switch {
	case err != nil:
		st.State, st.Error = StateError, err.Error()
	case code == 401 || code == 403:
		st.State = StateNeedsAuth
	default:
		st.State, st.Auth = StateOK, AuthNone
		if len(headers) > 0 {
			st.Auth = AuthHeaders // a custom header (X-Api-Key …) is the credential
		}
	}
	if st.State == StateError && lastErr != nil && st.Error == "" {
		st.Error = lastErr.Error()
	}
	return st
}

// probe POSTs an MCP initialize and returns the HTTP status. Anything below 400 counts as "let in"
// (servers differ in what they answer to a bare initialize; we only care about 401).
func (s *Store) probe(ctx context.Context, u string, headers map[string]string, tok string) (int, error) {
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"gerdr","version":"1"}}}`
	req, err := http.NewRequestWithContext(ctx, "POST", u, strings.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range headers {
		req.Header.Set(k, os.ExpandEnv(v))
	}
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	res, err := s.http.Do(req)
	if err != nil {
		return 0, err
	}
	io.Copy(io.Discard, io.LimitReader(res.Body, 64<<10))
	res.Body.Close()
	return res.StatusCode, nil
}

// Token is what an MCP client should send as `Authorization: Bearer …`, "" when the server needs
// none (or nothing works yet — the request then gets its 401 and the caller says "authorize").
func (s *Store) Token(ctx context.Context, name, u string, headers map[string]string) string {
	st := s.Check(ctx, name, u, headers, 5*time.Minute)
	switch st.Auth {
	case AuthClaude:
		return s.claudeToken(name, u)
	case AuthGerdr:
		tok, _ := s.ownToken(ctx, name, u)
		return tok
	}
	return ""
}

// Invalidate drops the cached status (a client just got a 401), so the next Token re-probes.
func (s *Store) Invalidate(name, u string) {
	s.mu.Lock()
	delete(s.status, key(name, u))
	s.mu.Unlock()
}

// Cached returns the last probe result without touching the network.
func (s *Store) Cached(name, u string) (Status, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.status[key(name, u)]
	return st, ok
}

// ---- Claude Code's tokens ----

type claudeEntry struct {
	ServerName  string `json:"serverName"`
	ServerURL   string `json:"serverUrl"`
	AccessToken string `json:"accessToken"`
	ExpiresAt   int64  `json:"expiresAt"` // ms; 0 = unknown
}

func (s *Store) claudeToken(name, u string) string {
	s.mu.Lock()
	if time.Since(s.claude.at) > 15*time.Second {
		s.claude.list, s.claude.at = s.readClaude(), time.Now()
	}
	list := s.claude.list
	s.mu.Unlock()
	var best string
	for _, e := range list {
		if normURL(e.ServerURL) != normURL(u) || e.AccessToken == "" {
			continue
		}
		if e.ExpiresAt > 0 && time.Now().UnixMilli() > e.ExpiresAt-30_000 {
			continue
		}
		if e.ServerName == name {
			return e.AccessToken
		}
		best = e.AccessToken
	}
	return best
}

func readClaude() []claudeEntry {
	var raw []byte
	if runtime.GOOS == "darwin" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		raw, _ = exec.CommandContext(ctx, "security", "find-generic-password", "-s", "Claude Code-credentials", "-w").Output()
		cancel()
	}
	if len(raw) == 0 {
		home, _ := os.UserHomeDir()
		dir := os.Getenv("CLAUDE_CONFIG_DIR")
		if dir == "" {
			dir = filepath.Join(home, ".claude")
		}
		raw, _ = os.ReadFile(filepath.Join(dir, ".credentials.json"))
	}
	var d struct {
		MCPOAuth map[string]claudeEntry `json:"mcpOAuth"`
	}
	if json.Unmarshal(bytes.TrimSpace(raw), &d) != nil {
		return nil
	}
	out := make([]claudeEntry, 0, len(d.MCPOAuth))
	for _, e := range d.MCPOAuth {
		out = append(out, e)
	}
	return out
}

// ---- gerdr's own tokens ----

type saved struct {
	URL          string        `json:"url"`
	ClientID     string        `json:"client_id"`
	ClientSecret string        `json:"client_secret,omitempty"`
	AuthURL      string        `json:"auth_url"`
	TokenURL     string        `json:"token_url"`
	Resource     string        `json:"resource,omitempty"`
	Redirect     string        `json:"redirect"`
	Token        *oauth2.Token `json:"token,omitempty"`
}

func (s *Store) file(name string) string { return filepath.Join(s.dir, "mcp-auth", name+".json") }

func (s *Store) load(name string) *saved {
	b, err := os.ReadFile(s.file(name))
	if err != nil {
		return nil
	}
	var a saved
	if json.Unmarshal(b, &a) != nil {
		return nil
	}
	return &a
}

func (s *Store) save(name string, a *saved) error {
	if err := os.MkdirAll(filepath.Dir(s.file(name)), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(a, "", "  ")
	return os.WriteFile(s.file(name), b, 0o600)
}

func (a *saved) config() *oauth2.Config {
	return &oauth2.Config{ClientID: a.ClientID, ClientSecret: a.ClientSecret, RedirectURL: a.Redirect,
		Endpoint: oauth2.Endpoint{AuthURL: a.AuthURL, TokenURL: a.TokenURL}}
}

// savingSource refreshes through oauth2 and writes every new token back to disk.
type savingSource struct {
	mu   sync.Mutex
	src  oauth2.TokenSource
	last string
	save func(*oauth2.Token)
}

func (t *savingSource) Token() (*oauth2.Token, error) {
	tok, err := t.src.Token()
	if err != nil {
		return nil, err
	}
	t.mu.Lock()
	if tok.AccessToken != t.last {
		t.last = tok.AccessToken
		t.save(tok)
	}
	t.mu.Unlock()
	return tok, nil
}

func (s *Store) ownToken(ctx context.Context, name, u string) (string, error) {
	s.mu.Lock()
	src := s.sources[key(name, u)]
	s.mu.Unlock()
	if src == nil {
		a := s.load(name)
		if a == nil || a.Token == nil || normURL(a.URL) != normURL(u) {
			return "", errors.New("no saved login")
		}
		src = &savingSource{last: a.Token.AccessToken, src: a.config().TokenSource(context.WithoutCancel(ctx), a.Token), save: func(t *oauth2.Token) {
			if cur := s.load(name); cur != nil {
				cur.Token = t
				_ = s.save(name, cur)
			}
		}}
		s.mu.Lock()
		s.sources[key(name, u)] = src
		s.mu.Unlock()
	}
	tok, err := src.Token()
	if err != nil {
		// refresh token revoked / expired: forget it so the next probe says "needs auth"
		s.mu.Lock()
		delete(s.sources, key(name, u))
		s.mu.Unlock()
		return "", err
	}
	return tok.AccessToken, nil
}

// Logout forgets gerdr's own login (Claude Code's is left alone — that's Claude Code's to manage).
func (s *Store) Logout(name string) error {
	s.mu.Lock()
	for k := range s.sources {
		if strings.HasPrefix(k, name+"\x00") {
			delete(s.sources, k)
		}
	}
	for k := range s.status {
		if strings.HasPrefix(k, name+"\x00") {
			delete(s.status, k)
		}
	}
	s.mu.Unlock()
	if err := os.Remove(s.file(name)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// ---- the authorization flow ----

const flowTTL = 15 * time.Minute

type flow struct {
	name, url string
	verifier  string
	a         *saved
	at        time.Time
}

// Begin discovers the server's authorization server, registers gerdr as a client if needed and
// returns the URL to send the user to. redirect is gerdr's /mcp-oauth/callback as the browser sees it.
func (s *Store) Begin(ctx context.Context, name, u, redirect string) (string, error) {
	resource, asURL, scopes, err := s.discoverResource(ctx, u)
	if err != nil {
		return "", err
	}
	meta, err := s.authServerMeta(ctx, asURL)
	if err != nil {
		return "", err
	}
	if meta.AuthorizationEndpoint == "" || meta.TokenEndpoint == "" {
		return "", fmt.Errorf("authorization server %s doesn't list its endpoints", asURL)
	}
	if len(scopes) == 0 {
		scopes = meta.ScopesSupported
	}
	a := &saved{URL: u, AuthURL: meta.AuthorizationEndpoint, TokenURL: meta.TokenEndpoint, Resource: resource, Redirect: redirect}
	// Reuse the client we registered last time if it was for the same server and callback.
	if old := s.load(name); old != nil && old.ClientID != "" && normURL(old.URL) == normURL(u) && old.Redirect == redirect && old.TokenURL == meta.TokenEndpoint {
		a.ClientID, a.ClientSecret = old.ClientID, old.ClientSecret
	} else {
		if meta.RegistrationEndpoint == "" {
			return "", errors.New("this server doesn't support dynamic client registration, so Gerdr can't sign in to it by itself; put a token in the server's headers instead")
		}
		if a.ClientID, a.ClientSecret, err = s.register(ctx, meta.RegistrationEndpoint, redirect); err != nil {
			return "", err
		}
	}
	state, verifier := random(), oauth2.GenerateVerifier()
	opts := []oauth2.AuthCodeOption{oauth2.S256ChallengeOption(verifier)}
	if resource != "" {
		opts = append(opts, oauth2.SetAuthURLParam("resource", resource))
	}
	cfg := a.config()
	cfg.Scopes = scopes
	authURL := cfg.AuthCodeURL(state, opts...)
	s.mu.Lock()
	for k, f := range s.flows {
		if time.Since(f.at) > flowTTL || f.name == name {
			delete(s.flows, k)
		}
	}
	s.flows[state] = &flow{name: name, url: u, verifier: verifier, a: a, at: time.Now()}
	s.mu.Unlock()
	return authURL, nil
}

// Finish exchanges the code the authorization server sent back. Returns the server's name.
func (s *Store) Finish(ctx context.Context, state, code string) (string, error) {
	s.mu.Lock()
	f := s.flows[state]
	delete(s.flows, state)
	s.mu.Unlock()
	if f == nil || time.Since(f.at) > flowTTL {
		return "", errors.New("this authorization link was already used or has expired; start again from Settings → MCP")
	}
	opts := []oauth2.AuthCodeOption{oauth2.VerifierOption(f.verifier)}
	if f.a.Resource != "" {
		opts = append(opts, oauth2.SetAuthURLParam("resource", f.a.Resource))
	}
	tok, err := f.a.config().Exchange(ctx, code, opts...)
	if err != nil {
		return f.name, fmt.Errorf("exchanging the code failed: %w", err)
	}
	f.a.Token = tok
	if err := s.save(f.name, f.a); err != nil {
		return f.name, err
	}
	s.mu.Lock()
	delete(s.sources, key(f.name, f.url))
	delete(s.status, key(f.name, f.url))
	s.mu.Unlock()
	return f.name, nil
}

var resourceMetaRE = regexp.MustCompile(`resource_metadata="([^"]+)"`)
var scopeRE = regexp.MustCompile(`scope="([^"]+)"`)

// discoverResource: protected-resource metadata (RFC 9728) → which authorization server, which
// resource id, which scopes. Servers without it are assumed to be their own authorization server.
func (s *Store) discoverResource(ctx context.Context, u string) (resource, as string, scopes []string, err error) {
	pu, err := url.Parse(u)
	if err != nil {
		return "", "", nil, err
	}
	origin := pu.Scheme + "://" + pu.Host
	var candidates []string
	req, _ := http.NewRequestWithContext(ctx, "POST", u, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"gerdr","version":"1"}}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if res, e := s.http.Do(req); e == nil {
		res.Body.Close()
		h := res.Header.Get("WWW-Authenticate")
		if m := resourceMetaRE.FindStringSubmatch(h); m != nil {
			candidates = append(candidates, m[1])
		}
		if m := scopeRE.FindStringSubmatch(h); m != nil {
			scopes = strings.Fields(m[1])
		}
	}
	if p := strings.TrimRight(pu.Path, "/"); p != "" {
		candidates = append(candidates, origin+"/.well-known/oauth-protected-resource"+p)
	}
	candidates = append(candidates, origin+"/.well-known/oauth-protected-resource")
	for _, c := range candidates {
		var m struct {
			Resource             string   `json:"resource"`
			AuthorizationServers []string `json:"authorization_servers"`
			ScopesSupported      []string `json:"scopes_supported"`
		}
		if s.getJSON(ctx, c, &m) == nil && len(m.AuthorizationServers) > 0 {
			if len(scopes) == 0 {
				scopes = m.ScopesSupported
			}
			res := m.Resource
			if res == "" {
				res = u
			}
			return res, m.AuthorizationServers[0], scopes, nil
		}
	}
	return u, origin, scopes, nil
}

type asMeta struct {
	AuthorizationEndpoint string   `json:"authorization_endpoint"`
	TokenEndpoint         string   `json:"token_endpoint"`
	RegistrationEndpoint  string   `json:"registration_endpoint"`
	ScopesSupported       []string `json:"scopes_supported"`
}

// authServerMeta: RFC 8414 / OIDC discovery, with the path-insertion variants the MCP spec lists.
func (s *Store) authServerMeta(ctx context.Context, issuer string) (*asMeta, error) {
	pu, err := url.Parse(issuer)
	if err != nil {
		return nil, err
	}
	origin := pu.Scheme + "://" + pu.Host
	p := strings.TrimRight(pu.Path, "/")
	var urls []string
	if p != "" {
		urls = []string{origin + "/.well-known/oauth-authorization-server" + p, origin + "/.well-known/openid-configuration" + p, origin + p + "/.well-known/openid-configuration"}
	} else {
		urls = []string{origin + "/.well-known/oauth-authorization-server", origin + "/.well-known/openid-configuration"}
	}
	for _, x := range urls {
		var m asMeta
		if s.getJSON(ctx, x, &m) == nil && m.TokenEndpoint != "" {
			return &m, nil
		}
	}
	// Old servers without metadata: the spec's default endpoints.
	return &asMeta{AuthorizationEndpoint: origin + "/authorize", TokenEndpoint: origin + "/token", RegistrationEndpoint: origin + "/register"}, nil
}

func (s *Store) register(ctx context.Context, endpoint, redirect string) (id, secret string, err error) {
	body, _ := json.Marshal(map[string]any{
		"client_name":                "gerdr",
		"redirect_uris":              []string{redirect},
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	})
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	res, err := s.http.Do(req)
	if err != nil {
		return "", "", err
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	if res.StatusCode >= 300 {
		return "", "", fmt.Errorf("client registration failed: HTTP %d %s", res.StatusCode, strings.TrimSpace(string(b)))
	}
	var r struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	if err := json.Unmarshal(b, &r); err != nil || r.ClientID == "" {
		return "", "", fmt.Errorf("client registration returned no client_id")
	}
	return r.ClientID, r.ClientSecret, nil
}

func (s *Store) getJSON(ctx context.Context, u string, out any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	res, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", res.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(out)
}

func random() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
