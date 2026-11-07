package work

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func writeStream(t *testing.T, dir, id, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, StreamsDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, StreamsDir, id+".json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestParseStreams(t *testing.T) {
	for _, c := range []struct{ body, err string }{
		{`{"connect":"t.stream","on":"t.tick"}`, ""},
		{`{"connect":"stream","on":"t.tick"}`, "文件.函数"},
		{`{"connect":"t.stream"}`, "文件.函数"},
		{`{"connect":`, "格式不对"},
	} {
		dir := t.TempDir()
		writeStream(t, dir, "a", c.body)
		_, err := ParseStreams(dir)
		if c.err == "" && err != nil || c.err != "" && (err == nil || !strings.Contains(err.Error(), c.err)) {
			t.Errorf("%s: err %v, want %q", c.body, err, c.err)
		}
	}
}

// A panel's stream: connect tells where (with its header), every message runs on, a burst collapses into fewer runs,
// a dropped connection is dialled again, and "not now" (null) keeps it idle.
func TestStreamConnectsRunsAndRedials(t *testing.T) {
	var dials atomic.Int32
	conns := make(chan *websocket.Conn, 4)
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "no", 401)
			return
		}
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		dials.Add(1)
		conns <- c
	}))
	defer srv.Close()
	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	r, dir := newProject(t, map[string]string{
		"s.ts": `export function stream(input: any, ctx: any) {
  if (!ctx.fs.stat("data/on")) return null;
  return { url: ` + "`" + url + "`" + `, headers: { Authorization: "Bearer tok" } };
}
export async function tick(input: any, ctx: any) {
  const n = ctx.fs.stat("data/n") ? Number(ctx.fs.read("data/n")) : 0;
  ctx.fs.write("data/n", String(n + 1));
  ctx.fs.write("data/last.json", JSON.stringify(input));
  await new Promise((f) => setTimeout(f, 200));
}`,
	})
	writeStream(t, dir, "team", `{"connect":"s.stream","on":"s.tick"}`)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Not signed in: connect says null, nothing is dialled
	r.syncStreams(ctx)
	waitFor(t, func() bool { l, _ := r.Streams(""); return len(l) == 1 && l[0].State == "idle" })
	if dials.Load() != 0 {
		t.Fatal("dialled while connect said not now")
	}
	r.strm.mu.Lock()
	for _, c := range r.strm.conns {
		c.cancel()
	}
	r.strm.conns = nil
	r.strm.mu.Unlock()

	os.MkdirAll(filepath.Join(dir, "data"), 0o755)
	os.WriteFile(filepath.Join(dir, "data", "on"), []byte("1"), 0o644)
	r.syncStreams(ctx)
	c := <-conns
	// A burst: the first runs, the rest collapse into one more run with the latest
	for i := 1; i <= 5; i++ {
		c.WriteMessage(websocket.TextMessage, []byte(`{"type":"rev","rev":`+string(rune('0'+i))+`}`))
	}
	waitFor(t, func() bool {
		b, _ := os.ReadFile(filepath.Join(dir, "data", "last.json"))
		return strings.Contains(string(b), `"rev":5`)
	})
	time.Sleep(400 * time.Millisecond)
	n, _ := os.ReadFile(filepath.Join(dir, "data", "n"))
	if s := string(n); s != "1" && s != "2" {
		t.Errorf("5 messages in a burst ran on %s times, want 1 or 2", s)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "data", "last.json")); !strings.Contains(string(b), `"stream":"team"`) {
		t.Errorf("on's input: %s", b)
	}
	l, _ := r.Streams("")
	if l[0].State != "connected" || l[0].Messages != 5 {
		t.Errorf("info: %+v", l[0])
	}

	// The server drops it: dialled again
	c.Close()
	select {
	case c = <-conns:
	case <-time.After(10 * time.Second):
		t.Fatal("not redialled")
	}
	c.WriteMessage(websocket.TextMessage, []byte(`plain text`))
	waitFor(t, func() bool {
		b, _ := os.ReadFile(filepath.Join(dir, "data", "last.json"))
		return strings.Contains(string(b), `"message":"plain text"`)
	})

	// The file goes: the connection is closed
	os.Remove(filepath.Join(dir, StreamsDir, "team.json"))
	r.syncStreams(ctx)
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, _, err := c.ReadMessage(); err == nil {
		t.Error("connection still open after its file went")
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	for i := 0; i < 500; i++ {
		if ok() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out")
}
