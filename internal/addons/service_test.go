//go:build !windows

package addons

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// echoService: says it is up, then sends every stdin line back as a message's data. Exits when stdin closes and
// leaves a marker, so the test can tell it was asked to stop rather than killed.
const echoService = `#!/bin/sh
echo '{"type":"status","text":"starting","links":{"panel":"http://127.0.0.1:7858","bad":"javascript:alert(1)"}}'
echo '{"type":"status","text":"up"}'
echo 'not json, goes to the log'
echo '{"type":"event","event":{"kind":"ping","key":"k"}}'
while IFS= read -r line; do
  printf '{"type":"message","data":%s}\n' "$line"
  case "$line" in *crash*) exit 3;; esac
done
touch exited # cwd: the add-on folder
`

type recorder struct {
	mu       sync.Mutex
	msgs     []string
	statuses []ServiceStatus
}

func (r *recorder) waitMsg(t *testing.T, sub string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		for _, m := range r.msgs {
			if strings.Contains(m, sub) {
				r.mu.Unlock()
				return
			}
		}
		r.mu.Unlock()
		time.Sleep(20 * time.Millisecond)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	t.Fatalf("no message containing %q; got %q", sub, r.msgs)
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestService(t *testing.T) {
	dir := t.TempDir()
	pdir := filepath.Join(dir, "addons", "band")
	_ = os.MkdirAll(pdir, 0o755)
	if err := os.WriteFile(filepath.Join(pdir, "svc.sh"), []byte(echoService), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pdir, "addon.json"), []byte(`{"service":{"command":"./svc.sh","events":["ci/*"]},"settings":[{"key":"token","type":"secret","default":"d0"},{"key":"open","type":"link","url":"https://x"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	m := New(dir, slog.Default())
	// A service-only add-on is listed, with nothing to build and no error
	list := m.List()
	if len(list) != 1 || list[0].ID != "band" || list[0].Error != "" || list[0].Code != "" {
		t.Fatalf("list = %+v", list)
	}

	rec := &recorder{}
	s := NewServices(m, slog.Default())
	s.Greeting = func(id string) [][]byte { return [][]byte{[]byte(`{"type":"hello","addon":"` + id + `"}`)} }
	s.OnMessage = func(id string, data json.RawMessage) {
		rec.mu.Lock()
		rec.msgs = append(rec.msgs, id+":"+string(data))
		rec.mu.Unlock()
	}
	s.OnStatus = func(id string, st ServiceStatus) {
		rec.mu.Lock()
		rec.statuses = append(rec.statuses, st)
		rec.mu.Unlock()
	}
	var evs []string
	s.OnEvent = func(id string, e json.RawMessage) {
		rec.mu.Lock()
		evs = append(evs, id+":"+string(e))
		rec.mu.Unlock()
	}
	s.Sync()
	defer s.Close()

	rec.waitMsg(t, `band:{"type":"hello","addon":"band"}`) // greeting reached stdin
	// then its settings: defaults, no link
	rec.waitMsg(t, `band:{"settings":{"token":"d0"},"type":"settings"}`)
	waitFor(t, "the service's event", func() bool {
		rec.mu.Lock()
		defer rec.mu.Unlock()
		return len(evs) == 1 && evs[0] == `band:{"kind":"ping","key":"k"}`
	})
	// A changed setting reaches the running service on the next Sync (the state file is watched)
	if err := m.SetSettings("band", map[string]any{"token": "t1"}); err != nil {
		t.Fatal(err)
	}
	s.Sync()
	rec.waitMsg(t, `"token":"t1"`)
	// Events go only to services whose patterns pick them
	s.SendWhere(func(p []string) bool { return len(p) == 1 && p[0] == "ci/*" }, []byte(`{"type":"event","n":"yes"}`))
	rec.waitMsg(t, `"n":"yes"`)
	waitFor(t, "status text", func() bool {
		st := s.Status("band")
		// a status line without "links" keeps the last ones; a non-http address is dropped
		return st != nil && st.Text == "up" && st.State == "running" &&
			len(st.Links) == 1 && st.Links["panel"] == "http://127.0.0.1:7858"
	})
	if err := s.Send("band", []byte(`{"type":"message","data":{"n":1}}`)); err != nil {
		t.Fatal(err)
	}
	rec.waitMsg(t, `"n":1`)
	s.Broadcast([]byte(`{"type":"sessions","sessions":[]}`))
	rec.waitMsg(t, `"sessions":[]`)

	// Dies → restarted after a second, the reason kept
	_ = s.Send("band", []byte(`{"type":"crash"}`))
	waitFor(t, "restart", func() bool { st := s.Status("band"); return st != nil && st.Restarts == 1 && st.State == "running" })
	if err := s.Send("band", []byte(`{"type":"message","data":"again"}`)); err != nil {
		t.Fatal(err)
	}
	rec.waitMsg(t, `"again"`)

	// Switched off: stopped politely — stdin closed, the script exits on its own
	if err := m.SetEnabled("band", false); err != nil {
		t.Fatal(err)
	}
	s.Sync()
	if s.Status("band") != nil {
		t.Fatal("still has a service")
	}
	if _, err := os.Stat(filepath.Join(pdir, "exited")); err != nil {
		t.Fatal("service was not given the chance to exit on its own")
	}
	if err := s.Send("band", []byte(`{}`)); err == nil {
		t.Fatal("send to a removed service succeeded")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "logs", "addons", "band.log")); !strings.Contains(string(b), "start") {
		t.Fatalf("log = %q", b)
	}
}

func TestServiceBadManifest(t *testing.T) {
	dir := t.TempDir()
	pdir := filepath.Join(dir, "addons", "x")
	_ = os.MkdirAll(pdir, 0o755)
	_ = os.WriteFile(filepath.Join(pdir, "addon.json"), []byte(`{"service":{}}`), 0o644)
	m := New(dir, slog.Default())
	if l := m.List(); len(l) != 1 || !strings.Contains(l[0].Error, "command") {
		t.Fatalf("list = %+v", l)
	}
	if len(m.services()) != 0 {
		t.Fatal("broken addon.json started a service")
	}
}

func TestServiceCommandNotFound(t *testing.T) {
	dir := t.TempDir()
	pdir := filepath.Join(dir, "addons", "x")
	_ = os.MkdirAll(pdir, 0o755)
	_ = os.WriteFile(filepath.Join(pdir, "addon.json"), []byte(`{"service":{"command":"no-such-cmd-gerdr"}}`), 0o644)
	s := NewServices(New(dir, slog.Default()), slog.Default())
	s.Sync()
	defer s.Close()
	waitFor(t, "error", func() bool {
		st := s.Status("x")
		return st != nil && st.State == "exited" && strings.Contains(st.Error, "not found")
	})
}
