package server

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/gerdr-dev/gerdr/internal/config"
	"github.com/gerdr-dev/gerdr/internal/peer"
	"github.com/gerdr-dev/gerdr/internal/remote"
)

// A console and a host, both real servers: the host only on its remote port (TLS, device cookies), the console
// reaching it the way it would through the relay (docs/dev/MULTIHOST.md).
func hostPair(t *testing.T) (console *Server, ch http.Handler, hostSrv *Server) {
	t.Helper()
	rm, err := remote.Open(t.TempDir(), config.Remote{RelayURL: "https://relay.example.com", Host: testHost, DeviceTTL: 24 * time.Hour, ReauthAfter: 24 * time.Hour},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	hostSrv = &Server{Dir: t.TempDir(), Token: "host-token-host-token-host-token-xx", Remote: rm, Hub: reportHub(t), Layout: config.NewStore(filepath.Join(t.TempDir(), "layout.json"), `null`)}
	hh := hostSrv.Handler()
	ts := httptest.NewTLSServer(RemoteHandler(hh))
	t.Cleanup(ts.Close)

	// The peer code is minted on the host's local port, by a person (token)
	w := do(hh, "POST", "/api/remote/pair-code", strings.NewReader(`{"peer":true}`), hdr("Authorization", "Bearer "+hostSrv.Token), csrf)
	var code struct{ URL string }
	if json.Unmarshal(w.Body.Bytes(), &code); w.Code != 200 || !strings.Contains(code.URL, "?peer=") {
		t.Fatalf("peer code: %d %s", w.Code, w.Body)
	}

	// The console has sessions of its own too (same ids as the host's: they must not get mixed up)
	ch2 := reportHub(t)
	pm, err := peer.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	addr := ts.Listener.Addr().String()
	pm.Route(func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}, &tls.Config{InsecureSkipVerify: true})
	console = &Server{Token: "console-token-console-token-console", Hub: ch2, Peers: pm, Layout: config.NewStore(filepath.Join(t.TempDir(), "layout.json"), `null`)}
	ch = console.Handler()
	w = do(ch, "POST", "/api/hosts", strings.NewReader(`{"invite":`+mustJSON(code.URL)+`,"name":"mini"}`), hdr("Authorization", "Bearer "+console.Token), csrf)
	if w.Code != 200 {
		t.Fatalf("add host: %d %s", w.Code, w.Body)
	}
	return console, ch, hostSrv
}

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestHostSessionsAndOps(t *testing.T) {
	console, ch, hostSrv := hostPair(t)
	auth := hdr("Authorization", "Bearer "+console.Token)

	// The host sees the console as a peer device that is never asked for a passkey
	devs := hostSrv.Remote.Auth.Devices()
	if len(devs) != 1 || !devs[0].Peer || devs[0].Temp || !strings.Contains(devs[0].Label, "Gerdr") {
		t.Fatalf("host's device list: %+v", devs)
	}

	w := do(ch, "GET", "/api/sessions", nil, auth)
	var list []struct {
		ID       string
		ReportTo string
		Host     *peer.HostRef
	}
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	byID := map[string]int{}
	for i, s := range list {
		byID[s.ID] = i
	}
	i, ok := byID["mini~w1"]
	if !ok {
		t.Fatalf("host sessions not merged: %s", w.Body)
	}
	if s := list[i]; s.Host == nil || s.Host.ID != "mini" || s.ReportTo != "mini~boss" {
		t.Fatalf("summary not rewritten: %+v", s)
	}

	w = do(ch, "GET", "/api/sessions/mini~w1/events", nil, auth)
	var evs []map[string]any
	if json.Unmarshal(w.Body.Bytes(), &evs); w.Code != 200 || len(evs) != 3 {
		t.Fatalf("events through the console: %d %s", w.Code, w.Body)
	}

	w = do(ch, "POST", "/api/sessions/mini~w2/rename", strings.NewReader(`{"Title":"from the console"}`), auth, csrf)
	if w.Code != 200 {
		t.Fatalf("rename: %d %s", w.Code, w.Body)
	}
	if s, _ := hostSrv.Hub.Session("w2"); s.Summary().Title != "from the console" {
		t.Fatalf("rename didn't reach the host: %q", s.Summary().Title)
	}

	w = do(ch, "GET", "/api/sessions/mini~w1", nil, auth)
	if !strings.Contains(w.Body.String(), `"id":"mini~w1"`) {
		t.Fatalf("one summary not rewritten: %s", w.Body)
	}
	// The proxy paths need this computer's credential like /api/ does
	for _, p := range []string{"/h/mini/api/sessions", "/api/sessions/mini~w1", "/api/fs/dirs?host=mini"} {
		if w := do(ch, "GET", p, nil); w.Code != 401 {
			t.Fatalf("%s without a token: %d", p, w.Code)
		}
	}
	if w := do(ch, "POST", "/h/mini/api/sessions/w1/cancel", nil, auth); w.Code != 403 {
		t.Fatalf("a write through /h/ without the CSRF header: %d", w.Code)
	}
	if w := do(ch, "GET", "/h/mini/api/sessions?local=1", nil, auth); !strings.Contains(w.Body.String(), `"mini~w1"`) {
		t.Fatalf("/h/ proxy: %s", w.Body)
	}
	if w := do(ch, "GET", "/api/sessions/nope~w1", nil, auth); w.Code != 404 {
		t.Fatalf("unknown host: %d", w.Code)
	}
	// Removing the host here forgets it
	if w := do(ch, "DELETE", "/api/hosts/mini", nil, auth, csrf); w.Code != 200 {
		t.Fatalf("remove: %d", w.Code)
	}
	if w := do(ch, "GET", "/api/sessions", nil, auth); strings.Contains(w.Body.String(), "mini~") {
		t.Fatalf("removed host still listed: %s", w.Body)
	}
}

// The console's /ws carries a host session's log: watched through the upstream, ids prefixed.
func TestHostWS(t *testing.T) {
	console, ch, _ := hostPair(t)
	cs := httptest.NewServer(ch)
	defer cs.Close()
	hdrs := http.Header{"Authorization": {"Bearer " + console.Token}}
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(cs.URL, "http")+"/ws", hdrs)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.WriteJSON(map[string]any{"t": "watch", "ids": []string{"mini~w1"}})
	deadline := time.Now().Add(10 * time.Second)
	_ = conn.SetReadDeadline(deadline)
	for time.Now().Before(deadline) {
		_, b, err := conn.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		var batch []struct {
			T      string
			ID     string
			Events []map[string]any
		}
		_ = json.Unmarshal(b, &batch)
		for _, m := range batch {
			if m.T == "events" && m.ID == "mini~w1" && len(m.Events) == 3 {
				return
			}
		}
	}
	t.Fatal("no events for the host session")
}

// A host session reporting to a console session: kept on the console (the host can't reach it), shown on the
// summary here, the host's own same-host target cleared; the reporter can read the host session for the report.
func TestHostReportTo(t *testing.T) {
	console, ch, hostSrv := hostPair(t)
	auth := hdr("Authorization", "Bearer "+console.Token)
	w := do(ch, "POST", "/api/sessions/mini~w1/meta", strings.NewReader(`{"reportTo":"boss"}`), auth, csrf)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"reportTo":"boss"`) {
		t.Fatalf("meta: %d %s", w.Code, w.Body)
	}
	if got := console.Peers.ReportTo("mini~w1"); got != "boss" {
		t.Fatalf("console-side target: %q", got)
	}
	if s, _ := hostSrv.Hub.Session("w1"); s.Summary().ReportTo != "" {
		t.Fatalf("the host's own target should give way, got %q", s.Summary().ReportTo)
	}
	rp := newReporter(console)
	sum, evs, err := rp.source("mini~w1")
	if err != nil || sum.ID != "mini~w1" || len(evs) != 3 || lastReply(evs) == "" {
		t.Fatalf("source: %+v %d %v", sum.ID, len(evs), err)
	}
	if st, ok := rp.state("mini~w2"); !ok || st == "" {
		t.Fatalf("state of a host session: %q %v", st, ok)
	}
	// A console session may report to a host session too (Hub.Elsewhere)
	if w := do(ch, "POST", "/api/sessions/w2/meta", strings.NewReader(`{"reportTo":"mini~boss"}`), auth, csrf); w.Code != 200 {
		t.Fatalf("console → host target: %d %s", w.Code, w.Body)
	}
	// A target on the same host goes back to the host
	w = do(ch, "POST", "/api/sessions/mini~w1/meta", strings.NewReader(`{"reportTo":"mini~boss"}`), auth, csrf)
	if w.Code != 200 || console.Peers.ReportTo("mini~w1") != "" {
		t.Fatalf("same-host target: %d %s", w.Code, w.Body)
	}
	if s, _ := hostSrv.Hub.Session("w1"); s.Summary().ReportTo != "boss" {
		t.Fatalf("host target: %q", s.Summary().ReportTo)
	}
}

// Files cross as they are: an upload lands on the host (even one that looks like JSON) and reads back through /h/.
func TestHostFiles(t *testing.T) {
	console, ch, _ := hostPair(t)
	auth := hdr("Authorization", "Bearer "+console.Token)
	content := `{"from":"x","reportTo":"y"}`
	w := do(ch, "POST", "/api/fs/upload?host=mini&name=a.json", strings.NewReader(content), auth, csrf, hdr("Content-Type", "application/json"))
	var up struct{ Path string }
	if json.Unmarshal(w.Body.Bytes(), &up); w.Code != 200 || up.Path == "" {
		t.Fatalf("upload: %d %s", w.Code, w.Body)
	}
	w = do(ch, "GET", "/h/mini/api/fs/raw?path="+url.QueryEscape(up.Path), nil, auth)
	if w.Code != 200 || w.Body.String() != content {
		t.Fatalf("raw: %d %q", w.Code, w.Body.String())
	}
	w = do(ch, "GET", "/api/fs/stat?host=mini&path="+url.QueryEscape(up.Path), nil, auth)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "a.json") {
		t.Fatalf("stat: %d %s", w.Code, w.Body)
	}
}

// `gerdr session open` on a host session opens it on this computer's screen
func TestHostOpenHere(t *testing.T) {
	console, ch, _ := hostPair(t)
	sub := console.Hub.Subscribe()
	defer console.Hub.Unsubscribe(sub)
	if w := do(ch, "POST", "/api/sessions/mini~w2/open", strings.NewReader(`{"near":"x"}`), hdr("Authorization", "Bearer "+console.Token), csrf); w.Code != 200 {
		t.Fatalf("open: %d %s", w.Code, w.Body)
	}
	for {
		select {
		case m := <-sub.C():
			if b, _ := json.Marshal(m); m.T == "open" && strings.Contains(string(b), "mini~w2") {
				return
			}
		case <-time.After(3 * time.Second):
			t.Fatal("no open broadcast here")
		}
	}
}
