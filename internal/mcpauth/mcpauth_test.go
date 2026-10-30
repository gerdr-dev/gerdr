package mcpauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeServer is an MCP endpoint at /mcp guarded by OAuth, which is also its own authorization server.
type fakeServer struct {
	*httptest.Server
	valid      atomic.Value // string: the access token /mcp accepts
	registered atomic.Int32
	refreshed  atomic.Int32
}

func newFake(t *testing.T) *fakeServer {
	f := &fakeServer{}
	f.valid.Store("")
	mux := http.NewServeMux()
	f.Server = httptest.NewServer(mux)
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		if tok := f.valid.Load().(string); tok == "" || r.Header.Get("Authorization") != "Bearer "+tok {
			w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+f.URL+`/.well-known/oauth-protected-resource/mcp"`)
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	})
	mux.HandleFunc("/.well-known/oauth-protected-resource/mcp", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"resource": f.URL + "/mcp", "authorization_servers": []string{f.URL + "/as"}})
	})
	mux.HandleFunc("/.well-known/oauth-authorization-server/as", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"authorization_endpoint": f.URL + "/as/authorize", "token_endpoint": f.URL + "/as/token", "registration_endpoint": f.URL + "/as/register"})
	})
	mux.HandleFunc("/as/register", func(w http.ResponseWriter, r *http.Request) {
		f.registered.Add(1)
		w.WriteHeader(201)
		w.Write([]byte(`{"client_id":"cid"}`))
	})
	mux.HandleFunc("/as/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		switch r.Form.Get("grant_type") {
		case "authorization_code":
			if r.Form.Get("code") != "good" || r.Form.Get("code_verifier") == "" || r.Form.Get("resource") != f.URL+"/mcp" {
				w.WriteHeader(400)
				w.Write([]byte(`{"error":"invalid_grant"}`))
				return
			}
			f.valid.Store("tok1")
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"access_token":"tok1","refresh_token":"r1","token_type":"Bearer","expires_in":1}`))
		case "refresh_token":
			f.refreshed.Add(1)
			f.valid.Store("tok2")
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"access_token":"tok2","refresh_token":"r2","token_type":"Bearer","expires_in":3600}`))
		}
	})
	t.Cleanup(f.Close)
	return f
}

func TestOAuthFlow(t *testing.T) {
	f := newFake(t)
	s := New(t.TempDir())
	s.readClaude = func() []claudeEntry { return nil }
	ctx := context.Background()
	u := f.URL + "/mcp"

	if st := s.Check(ctx, "x", u, nil, 0); st.State != StateNeedsAuth {
		t.Fatalf("before login: %+v", st)
	}
	authURL, err := s.Begin(ctx, "x", u, "http://127.0.0.1:1/mcp-oauth/callback")
	if err != nil {
		t.Fatal(err)
	}
	q, _ := url.Parse(authURL)
	if !strings.HasPrefix(authURL, f.URL+"/as/authorize") || q.Query().Get("code_challenge_method") != "S256" || q.Query().Get("resource") != u {
		t.Fatalf("auth url %s", authURL)
	}
	if st := s.Check(ctx, "x", u, nil, time.Minute); !st.Pending {
		t.Fatalf("should be pending: %+v", st)
	}
	if _, err := s.Finish(ctx, "wrong-state", "good"); err == nil {
		t.Fatal("unknown state must fail")
	}
	name, err := s.Finish(ctx, q.Query().Get("state"), "good")
	if err != nil || name != "x" {
		t.Fatal(name, err)
	}
	if _, err := s.Finish(ctx, q.Query().Get("state"), "good"); err == nil {
		t.Fatal("state must be single-use")
	}
	// expires_in was 1s: the next token is a refresh, and the refreshed one is what /mcp now accepts.
	time.Sleep(1100 * time.Millisecond)
	st := s.Check(ctx, "x", u, nil, 0)
	if st.State != StateOK || st.Auth != AuthGerdr || !st.HasOwn {
		t.Fatalf("after login: %+v", st)
	}
	if f.refreshed.Load() != 1 || s.Token(ctx, "x", u, nil) != "tok2" {
		t.Fatalf("refresh: %d", f.refreshed.Load())
	}
	// A second login reuses the registered client.
	if _, err := s.Begin(ctx, "x", u, "http://127.0.0.1:1/mcp-oauth/callback"); err != nil || f.registered.Load() != 1 {
		t.Fatalf("re-register: %v %d", err, f.registered.Load())
	}
	if err := s.Logout("x"); err != nil {
		t.Fatal(err)
	}
	f.valid.Store("someone-else")
	if st := s.Check(ctx, "x", u, nil, 0); st.State != StateNeedsAuth || st.HasOwn {
		t.Fatalf("after logout: %+v", st)
	}
}

func TestClaudeTokenFirst(t *testing.T) {
	f := newFake(t)
	f.valid.Store("claude-tok")
	s := New(t.TempDir())
	u := f.URL + "/mcp"
	exp := time.Now().Add(time.Hour).UnixMilli()
	s.readClaude = func() []claudeEntry {
		return []claudeEntry{{ServerName: "x", ServerURL: u + "/", AccessToken: "claude-tok", ExpiresAt: exp}}
	}
	ctx := context.Background()
	if st := s.Check(ctx, "x", u, nil, 0); st.State != StateOK || st.Auth != AuthClaude {
		t.Fatalf("%+v", st)
	}
	if s.Token(ctx, "x", u, nil) != "claude-tok" {
		t.Fatal("should hand out Claude Code's token")
	}
	// Claude Code's login expired (server rejects it now): back to needs_auth, so gerdr offers its own.
	f.valid.Store("new")
	if st := s.Check(ctx, "x", u, nil, 0); st.State != StateNeedsAuth {
		t.Fatalf("%+v", st)
	}
	// A token Claude Code itself marks expired isn't even tried.
	exp = time.Now().Add(-time.Hour).UnixMilli()
	s.claude.at = time.Time{}
	if s.claudeToken("x", u) != "" {
		t.Fatal("expired Claude token used")
	}
}

func TestStaticHeader(t *testing.T) {
	f := newFake(t)
	f.valid.Store("static")
	s := New(t.TempDir())
	s.readClaude = func() []claudeEntry { return nil }
	st := s.Check(context.Background(), "x", f.URL+"/mcp", map[string]string{"authorization": "Bearer static"}, 0)
	if st.State != StateOK || st.Auth != AuthHeaders {
		t.Fatalf("%+v", st)
	}
	st = s.Check(context.Background(), "x", f.URL+"/mcp", map[string]string{"Authorization": "Bearer nope"}, 0)
	if st.State != StateError {
		t.Fatalf("%+v", st)
	}
}
