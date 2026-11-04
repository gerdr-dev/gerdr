package server

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/gerdr-dev/gerdr/internal/config"
	"github.com/gerdr-dev/gerdr/internal/hub"
	"github.com/gerdr-dev/gerdr/internal/peer"
	"github.com/gerdr-dev/gerdr/internal/remote"
	"github.com/gerdr-dev/gerdr/internal/term"
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
	hostSrv = &Server{Dir: t.TempDir(), Token: "host-token-host-token-host-token-xx", Remote: rm, Hub: reportHub(t), Layout: config.NewStore(filepath.Join(t.TempDir(), "layout.json"), `null`),
		Terms: term.New(os.Environ, slog.New(slog.NewTextHandler(io.Discard, nil)))}
	t.Cleanup(hostSrv.Terms.Close)
	hh := hostSrv.Handler()
	// Offers h2 like a real host (relay / Caddy) does: the console's upstream /ws must still come out as HTTP/1.1
	ts := httptest.NewUnstartedServer(RemoteHandler(hh))
	ts.EnableHTTP2 = true
	ts.StartTLS()
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
	i, ok := byID["mini:w1"]
	if !ok {
		t.Fatalf("host sessions not merged: %s", w.Body)
	}
	if s := list[i]; s.Host == nil || s.Host.ID != "mini" || s.ReportTo != "mini:boss" {
		t.Fatalf("summary not rewritten: %+v", s)
	}

	w = do(ch, "GET", "/api/sessions/mini:w1/events", nil, auth)
	var evs []map[string]any
	if json.Unmarshal(w.Body.Bytes(), &evs); w.Code != 200 || len(evs) != 3 {
		t.Fatalf("events through the console: %d %s", w.Code, w.Body)
	}

	w = do(ch, "POST", "/api/sessions/mini:w2/rename", strings.NewReader(`{"Title":"from the console"}`), auth, csrf)
	if w.Code != 200 {
		t.Fatalf("rename: %d %s", w.Code, w.Body)
	}
	if s, _ := hostSrv.Hub.Session("w2"); s.Summary().Title != "from the console" {
		t.Fatalf("rename didn't reach the host: %q", s.Summary().Title)
	}

	w = do(ch, "GET", "/api/sessions/mini:w1", nil, auth)
	if !strings.Contains(w.Body.String(), `"id":"mini:w1"`) {
		t.Fatalf("one summary not rewritten: %s", w.Body)
	}
	// The proxy paths need this computer's credential like /api/ does
	for _, p := range []string{"/h/mini/api/sessions", "/api/sessions/mini:w1", "/api/fs/dirs?host=mini"} {
		if w := do(ch, "GET", p, nil); w.Code != 401 {
			t.Fatalf("%s without a token: %d", p, w.Code)
		}
	}
	if w := do(ch, "POST", "/h/mini/api/sessions/w1/cancel", nil, auth); w.Code != 403 {
		t.Fatalf("a write through /h/ without the CSRF header: %d", w.Code)
	}
	if w := do(ch, "GET", "/h/mini/api/sessions?local=1", nil, auth); !strings.Contains(w.Body.String(), `"mini:w1"`) {
		t.Fatalf("/h/ proxy: %s", w.Body)
	}
	if w := do(ch, "GET", "/api/sessions/nope:w1", nil, auth); w.Code != 404 {
		t.Fatalf("unknown host: %d", w.Code)
	}
	// Removing the host here forgets it
	if w := do(ch, "DELETE", "/api/hosts/mini", nil, auth, csrf); w.Code != 200 {
		t.Fatalf("remove: %d", w.Code)
	}
	if w := do(ch, "GET", "/api/sessions", nil, auth); strings.Contains(w.Body.String(), "mini:") {
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
	_ = conn.WriteJSON(map[string]any{"t": "watch", "ids": []string{"mini:w1"}})
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
			if m.T == "events" && m.ID == "mini:w1" && len(m.Events) == 3 {
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
	w := do(ch, "POST", "/api/sessions/mini:w1/meta", strings.NewReader(`{"reportTo":"boss"}`), auth, csrf)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"reportTo":"boss"`) {
		t.Fatalf("meta: %d %s", w.Code, w.Body)
	}
	if got := console.Peers.ReportTo("mini:w1"); got != "boss" {
		t.Fatalf("console-side target: %q", got)
	}
	if s, _ := hostSrv.Hub.Session("w1"); s.Summary().ReportTo != "" {
		t.Fatalf("the host's own target should give way, got %q", s.Summary().ReportTo)
	}
	rp := newReporter(console)
	sum, evs, err := rp.source("mini:w1")
	if err != nil || sum.ID != "mini:w1" || len(evs) != 3 || lastReply(evs) == "" {
		t.Fatalf("source: %+v %d %v", sum.ID, len(evs), err)
	}
	if st, ok := rp.state("mini:w2"); !ok || st == "" {
		t.Fatalf("state of a host session: %q %v", st, ok)
	}
	// A console session may report to a host session too (Hub.Elsewhere)
	if w := do(ch, "POST", "/api/sessions/w2/meta", strings.NewReader(`{"reportTo":"mini:boss"}`), auth, csrf); w.Code != 200 {
		t.Fatalf("console → host target: %d %s", w.Code, w.Body)
	}
	// A target on the same host goes back to the host
	w = do(ch, "POST", "/api/sessions/mini:w1/meta", strings.NewReader(`{"reportTo":"mini:boss"}`), auth, csrf)
	if w.Code != 200 || console.Peers.ReportTo("mini:w1") != "" {
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
	if w := do(ch, "POST", "/api/sessions/mini:w2/open", strings.NewReader(`{"near":"x"}`), hdr("Authorization", "Bearer "+console.Token), csrf); w.Code != 200 {
		t.Fatalf("open: %d %s", w.Code, w.Body)
	}
	for {
		select {
		case m := <-sub.C():
			if b, _ := json.Marshal(m); m.T == "open" && strings.Contains(string(b), "mini:w2") {
				return
			}
		case <-time.After(3 * time.Second):
			t.Fatal("no open broadcast here")
		}
	}
}

// A WebSocket to another computer goes through as is (the remote screen's stream does this; tested with the host's
// /ws, since the screen would capture this machine's real display).
func TestHostWSProxy(t *testing.T) {
	console, ch, _ := hostPair(t)
	cs := httptest.NewServer(ch)
	defer cs.Close()
	base := "ws" + strings.TrimPrefix(cs.URL, "http")
	if _, resp, err := websocket.DefaultDialer.Dial(base+"/h/mini/ws", nil); err == nil || resp == nil || resp.StatusCode != 401 {
		t.Fatalf("no token should be a 401, got %v %v", resp, err)
	}
	conn, _, err := websocket.DefaultDialer.Dial(base+"/h/mini/ws", http.Header{"Authorization": {"Bearer " + console.Token}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// Straight through: the host's own ids, not rewritten (this is the host's /ws, not the console's)
	_ = conn.WriteJSON(map[string]any{"t": "watch", "ids": []string{"w1"}})
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	for {
		_, b, err := conn.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), `"t":"events"`) && strings.Contains(string(b), `"id":"w1"`) {
			return
		}
	}
}

// Carrying a conversation on on another computer: the log comes along (plus the move notice), both ways, and the
// images it shows are copied to the computer it moves to.
func TestHostMoveConversation(t *testing.T) {
	console, ch, hostSrv := hostPair(t)
	auth := hdr("Authorization", "Bearer "+console.Token)
	here := t.TempDir()
	w := do(ch, "POST", "/api/sessions", strings.NewReader(`{"continueFrom":"mini:w1","cwd":`+mustJSON(here)+`}`), auth, csrf)
	if w.Code != 200 {
		t.Fatalf("host → here: %d %s", w.Code, w.Body)
	}
	var sum struct{ ID, Agent, Title string }
	_ = json.Unmarshal(w.Body.Bytes(), &sum)
	moved, err := console.Hub.Session(sum.ID)
	if err != nil || sum.Agent != "claude" {
		t.Fatalf("new session here: %+v %v", sum, err)
	}
	if evs := moved.Events(); len(evs) != 4 || evs[3].Kind != hub.EvNotice || !strings.Contains(evs[3].Text, "mini") {
		t.Fatalf("moved log: %d events", len(evs))
	}
	there := t.TempDir()
	w = do(ch, "POST", "/api/sessions", strings.NewReader(`{"continueFrom":"w1","host":"mini","cwd":`+mustJSON(there)+`}`), auth, csrf)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"id":"mini:`) {
		t.Fatalf("here → host: %d %s", w.Code, w.Body)
	}
	_ = json.Unmarshal(w.Body.Bytes(), &sum)
	_, rest := peer.Split(sum.ID)
	if s, err := hostSrv.Hub.Session(rest); err != nil || len(s.Events()) != 4 {
		t.Fatalf("moved there: %v", err)
	}

	img := base64.StdEncoding.EncodeToString([]byte("not really a png"))
	u := hostSrv.Hub.SaveBlob(img)
	hash := strings.TrimPrefix(u, hub.BlobURL)
	evs := []*hub.Event{{Kind: hub.EvUser, Content: []json.RawMessage{json.RawMessage(`{"type":"image","blob":"` + u + `"}`)}}}
	if err := console.copyBlobs(context.Background(), "mini:w1", "", evs); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(console.Hub.BlobPath(hash)); err != nil || string(b) != "not really a png" {
		t.Fatalf("image not copied: %v", err)
	}
}

// Artifact folders through the console: list a folder on the host, write a file only if it's still the version we
// had (412 with the current one when it isn't), and a new file only if it isn't there yet.
func TestHostFolderAPI(t *testing.T) {
	console, ch, _ := hostPair(t)
	auth := hdr("Authorization", "Bearer "+console.Token)
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "out"), 0o755)
	os.WriteFile(filepath.Join(dir, "out", "a.txt"), []byte("one"), 0o644)
	os.WriteFile(filepath.Join(dir, ".gerdrignore"), []byte("*.tmp\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "x.tmp"), []byte("skip"), 0o644)
	w := do(ch, "GET", "/api/fs/tree?host=mini&path="+url.QueryEscape(dir), nil, auth)
	var tree struct {
		Files []struct{ Path, Sha256 string }
	}
	if json.Unmarshal(w.Body.Bytes(), &tree); w.Code != 200 || len(tree.Files) != 2 || tree.Files[1].Path != "out/a.txt" {
		t.Fatalf("tree: %d %s", w.Code, w.Body)
	}
	sha := tree.Files[1].Sha256
	put := func(name, match, body string) *httptest.ResponseRecorder {
		q := url.Values{"host": {"mini"}, "path": {filepath.Join(dir, name)}, "ifMatch": {match}}
		return do(ch, "PUT", "/api/fs/file?"+q.Encode(), strings.NewReader(body), auth, csrf, hdr("Content-Type", "application/json"))
	}
	if w := put("out/a.txt", sha, "two"); w.Code != 200 {
		t.Fatalf("put on the right base: %d %s", w.Code, w.Body)
	}
	if w := put("out/a.txt", sha, "three"); w.Code != http.StatusPreconditionFailed || !strings.Contains(w.Body.String(), "current") {
		t.Fatalf("put on a stale base: %d %s", w.Code, w.Body)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "out", "a.txt")); string(b) != "two" {
		t.Fatalf("content: %q", b)
	}
	if w := put("new/b.json", "none", `{"from":"x"}`); w.Code != 200 {
		t.Fatalf("new file: %d %s", w.Code, w.Body)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "new", "b.json")); string(b) != `{"from":"x"}` {
		t.Fatalf("a json file must pass as is: %q", b)
	}
	if w := put("new/b.json", "none", "again"); w.Code != http.StatusPreconditionFailed {
		t.Fatalf("none on an existing file: %d", w.Code)
	}
}

// Report loops of any length are refused, wherever each step is kept (here, on the host, or here for a host
// session). Both hubs start with w1 → boss and w2 → boss (reportHub).
func TestHostReportLoops(t *testing.T) {
	console, ch, _ := hostPair(t)
	auth := hdr("Authorization", "Bearer "+console.Token)
	meta := func(id, to string) *httptest.ResponseRecorder {
		return do(ch, "POST", "/api/sessions/"+id+"/meta", strings.NewReader(`{"reportTo":`+mustJSON(to)+`}`), auth, csrf)
	}
	// b ↔ c across two computers: c → b is fine, then b → c closes the loop (either side may be asked last)
	if w := meta("w2", "mini:w2"); w.Code != 200 {
		t.Fatalf("here w2 → mini:w2: %d %s", w.Code, w.Body)
	}
	if w := meta("mini:w2", "w2"); w.Code != 400 || !strings.Contains(w.Body.String(), "绕回") {
		t.Fatalf("mini:w2 → w2 should be a loop: %d %s", w.Code, w.Body)
	}
	if w := meta("mini:w2", "boss"); w.Code != 200 {
		t.Fatalf("mini:w2 → boss (no loop): %d %s", w.Code, w.Body)
	}
	// a → b → c → a over three places: mini:w1 → boss (kept here), boss → mini:boss (here), mini:boss → mini:w1
	// (on the host: the host alone sees no loop, mini:w1's target is kept here)
	if w := meta("mini:w1", "boss"); w.Code != 200 {
		t.Fatalf("mini:w1 → boss: %d %s", w.Code, w.Body)
	}
	if w := meta("boss", "mini:boss"); w.Code != 200 {
		t.Fatalf("boss → mini:boss: %d %s", w.Code, w.Body)
	}
	if w := meta("mini:boss", "mini:w1"); w.Code != 400 || !strings.Contains(w.Body.String(), "绕回") {
		t.Fatalf("mini:boss → mini:w1 closes a three-step loop: %d %s", w.Code, w.Body)
	}
	// And from the other end: a session here asked to report into a chain that comes back to it
	if w := meta("w1", "mini:w2"); w.Code != 200 {
		t.Fatalf("w1 → mini:w2 → boss → mini:boss (no loop): %d %s", w.Code, w.Body)
	}
	if w := meta("boss", "w1"); w.Code != 400 || !strings.Contains(w.Body.String(), "绕回") {
		t.Fatalf("boss → w1 → mini:w2 → boss is a loop: %d %s", w.Code, w.Body)
	}
}

// A terminal on another computer: made through this one, its id comes back as "<host>:<id>", and its WebSocket
// (input and output) goes through the proxy.
func TestHostTerminal(t *testing.T) {
	console, ch, hostSrv := hostPair(t)
	auth := hdr("Authorization", "Bearer "+console.Token)
	dir := t.TempDir()
	w := do(ch, "POST", "/api/terms", strings.NewReader(`{"host":"mini","cwd":`+mustJSON(dir)+`}`), auth, csrf)
	var info struct{ ID, Cwd string }
	if json.Unmarshal(w.Body.Bytes(), &info); w.Code != 200 || !strings.HasPrefix(info.ID, "mini:") {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	_, rest := peer.Split(info.ID)
	if hostSrv.Terms.Get(rest) == nil {
		t.Fatal("the shell isn't on the host")
	}
	if w := do(ch, "GET", "/api/terms/"+info.ID, nil, auth); w.Code != 200 || !strings.Contains(w.Body.String(), `"id":"`+info.ID+`"`) {
		t.Fatalf("info: %d %s", w.Code, w.Body)
	}
	cs := httptest.NewServer(ch)
	defer cs.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(cs.URL, "http")+"/api/terms/"+info.ID+"/ws", http.Header{"Authorization": {"Bearer " + console.Token}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.WriteMessage(websocket.BinaryMessage, []byte("echo hello-from-$((40+2))\r"))
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	var got strings.Builder
	for !strings.Contains(got.String(), "hello-from-42") {
		typ, b, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("no output from the host's shell: %v (%q)", err, got.String())
		}
		if typ == websocket.BinaryMessage {
			got.Write(b)
		}
	}
	if w := do(ch, "DELETE", "/api/terms/"+info.ID, nil, auth, csrf); w.Code != 200 {
		t.Fatalf("kill: %d %s", w.Code, w.Body)
	}
}

// A session started by a panel's conversation (a worker's `gerdr session new`) belongs to that panel too, here or on
// a host: it carries the panel's tag and isn't placed in Code.
func TestPanelAdoptsSessionsItsConversationsStart(t *testing.T) {
	console, ch, hostSrv := hostPair(t)
	auth := hdr("Authorization", "Bearer "+console.Token)
	if w := do(ch, "POST", "/api/sessions/w2/meta", strings.NewReader(`{"tags":{"panel":"default/board"}}`), auth, csrf); w.Code != 200 {
		t.Fatalf("tag: %d %s", w.Code, w.Body)
	}
	here := t.TempDir()
	w := do(ch, "POST", "/api/sessions", strings.NewReader(`{"cwd":`+mustJSON(here)+`,"open":{"near":"w2"}}`), auth, csrf)
	var sum struct {
		ID   string            `json:"id"`
		Tags map[string]string `json:"tags"`
	}
	if json.Unmarshal(w.Body.Bytes(), &sum); w.Code != 200 || sum.Tags["panel"] != "default/board" {
		t.Fatalf("local: %d %s", w.Code, w.Body)
	}
	w = do(ch, "POST", "/api/sessions", strings.NewReader(`{"host":"mini","cwd":`+mustJSON(here)+`,"open":{"near":"w2"}}`), auth, csrf)
	if json.Unmarshal(w.Body.Bytes(), &sum); w.Code != 200 || !strings.HasPrefix(sum.ID, "mini:") {
		t.Fatalf("host: %d %s", w.Code, w.Body)
	}
	if s, err := hostSrv.Hub.Session(strings.TrimPrefix(sum.ID, "mini:")); err != nil || s.Summary().Tags["panel"] != "default/board" {
		t.Fatalf("host session not the panel's: %v %v", err, s)
	}
	// Not a panel's caller, or a workspace asked for: as before, no tag
	w = do(ch, "POST", "/api/sessions", strings.NewReader(`{"cwd":`+mustJSON(here)+`,"open":{"near":"w1"}}`), auth, csrf)
	sum.Tags = nil
	if json.Unmarshal(w.Body.Bytes(), &sum); w.Code != 200 || sum.Tags["panel"] != "" {
		t.Fatalf("plain caller: %d %s", w.Code, w.Body)
	}
}
