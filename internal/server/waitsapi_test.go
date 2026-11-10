//go:build !windows

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gerdr-dev/gerdr/internal/addons"
	"github.com/gerdr-dev/gerdr/internal/waits"
	"github.com/gerdr-dev/gerdr/internal/work"
)

// A source of gitlab states: records each get, answers h/a!1 (opened, 2 reviews) and h/t!9, refuses h/b!2, and
// once reports a state of a source it doesn't declare, unasked.
const stateSvc = `#!/bin/sh
while IFS= read -r line; do
  id=$(printf '%s' "$line" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
  case "$line" in *'"type":"get"'*) echo "$line" >> got;; esac
  case "$line" in
    *'"type":"get"'*'"key":"h/a!1"'* | *'"key":"h/a!1"'*'"type":"get"'*)
      echo '{"type":"state","id":"'"$id"'","state":{"state":"opened","reviews":2,"pipeline":"running"},"detail":"alice: rename this","url":"https://h/a/1"}'
      echo '{"type":"state","source":"jira","key":"X-1","state":{"state":"open"}}';;
    *'"key":"h/t!9"'*) echo '{"type":"state","id":"'"$id"'","state":{"state":"opened","reviews":0,"pipeline":"running"}}';;
    *'"type":"get"'*) echo '{"type":"error","id":"'"$id"'","reason":"glab not logged in to h","retry":false}';;
  esac
done
`

func TestWaits(t *testing.T) {
	dir := t.TempDir()
	pdir := filepath.Join(dir, "addons", "gitlab")
	_ = os.MkdirAll(pdir, 0o755)
	_ = os.WriteFile(filepath.Join(pdir, "svc.sh"), []byte(stateSvc), 0o755)
	_ = os.WriteFile(filepath.Join(pdir, "addon.json"), []byte(`{"service":{"command":"./svc.sh"},"states":[{"source":"gitlab","fields":{"state":"","reviews":"","pipeline":""}}]}`), 0o644)
	wt, err := waits.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	am := addons.New(dir, slog.Default())
	wr := work.New(dir, nil)
	wdir, err := wr.Ensure("")
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(wdir, "local", "w.ts"), []byte(`export async function on(input: any, ctx: any) { ctx.fs.write("data/got.json", JSON.stringify(input)); return 1 }`), 0o644)
	s := &Server{Hub: reportHub(t), Waits: wt, Log: slog.Default(), Addons: am, AddonSvc: addons.NewServices(am, slog.Default()), Work: wr}
	var mu sync.Mutex
	var woken []string
	s.wakeFn = func(w waits.Wait, st waits.State) {
		mu.Lock()
		woken = append(woken, w.Key+" "+wakeText(w, st))
		mu.Unlock()
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.wireWaits(ctx)
	s.AddonSvc.Sync()
	defer s.AddonSvc.Close()
	mux := http.NewServeMux()
	s.waitRoutes(mux)
	type resp struct {
		Wait   waits.Wait `json:"wait"`
		Woken  bool       `json:"woken"`
		Status waitStatus `json:"status"`
	}
	post := func(body string) (int, resp, string) {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/waits", bytes.NewBufferString(body)))
		var v resp
		_ = json.Unmarshal(rec.Body.Bytes(), &v)
		return rec.Code, v, rec.Body.String()
	}

	// Nobody reports jira; a field gitlab doesn't have; a broken condition
	for _, bad := range []string{
		`{"source":"jira","key":"X-1","until":"state != open","session":"w1"}`,
		`{"source":"gitlab","key":"h/a!1","until":"reveiws > 0","session":"w1"}`,
		`{"source":"gitlab","key":"h/a!1","until":"reviews >","session":"w1"}`,
		`{"source":"gitlab","key":"h/t!9","until":"reveiws > 0","session":"w1"}`,
	} {
		if code, _, body := post(bad); code != 400 {
			t.Errorf("%s: %d %s", bad, code, body)
		}
	}
	// Already true when waited on (late): woken at once, gone
	code, v, body := post(`{"source":"gitlab","key":"h/a!1","until":"reviews > 0","session":"w1","note":"fix them"}`)
	if code != 200 || !v.Woken {
		t.Fatalf("late wait: %d %s", code, body)
	}
	mu.Lock()
	if len(woken) != 1 || !strings.Contains(woken[0], "reviews=2") || !strings.Contains(woken[0], "alice: rename this") || !strings.Contains(woken[0], "fix them") {
		t.Errorf("woken %q", woken)
	}
	mu.Unlock()
	// Not true yet: waits, with the state shown; then holds when the state changes
	code, v, body = post(`{"source":"gitlab","key":"h/a!1","until":"reviews > 2 || state != 'opened'","session":"w1"}`)
	if code != 200 || v.Woken || v.Status.Holds == nil || *v.Status.Holds || v.Status.Now["reviews"] != 2.0 || v.Status.Reporter.State != "serving" {
		t.Fatalf("pending wait: %d %s", code, body)
	}
	_ = wt.SetState(waits.State{Source: "gitlab", Key: "h/a!1", State: map[string]any{"state": "merged", "reviews": 2.0, "pipeline": "success"}})
	s.stateChanged("gitlab", "h/a!1")
	mu.Lock()
	if len(woken) != 2 || !strings.Contains(woken[1], "state=merged") {
		t.Errorf("woken %q", woken)
	}
	mu.Unlock()
	// A key the source refuses: said so, with why
	if _, v, body := post(`{"source":"gitlab","key":"h/b!2","until":"reviews > 0","session":"w1"}`); v.Woken || v.Status.Warning == "" || v.Status.Reporter.State != "refused" || v.Status.Reporter.Reason == "" {
		t.Fatalf("refused: %s", body)
	}
	// A panel's trigger waiting for a state is asked about too
	_ = os.MkdirAll(filepath.Join(wdir, "triggers"), 0o755)
	_ = os.WriteFile(filepath.Join(wdir, "triggers", "mr.json"), []byte(`{"source":"gitlab","key":"h/t!9","until":"pipeline != 'running'","fn":"w.on"}`), 0o644)
	s.waitsChanged()
	waitUntil(t, "the panel's key asked about", func() bool {
		b, _ := os.ReadFile(filepath.Join(pdir, "got"))
		return strings.Contains(string(b), `"key":"h/t!9"`)
	})
	// The panel's wait runs when its state holds
	_ = wt.SetState(waits.State{Source: "gitlab", Key: "h/t!9", State: map[string]any{"state": "opened", "reviews": 0.0, "pipeline": "failed"}, Detail: "job test failed"})
	s.stateChanged("gitlab", "h/t!9")
	waitUntil(t, "the panel's trigger to run", func() bool {
		b, _ := os.ReadFile(filepath.Join(wdir, "data", "got.json"))
		return strings.Contains(string(b), `"pipeline":"failed"`) && strings.Contains(string(b), "job test failed")
	})
	tl, _ := wr.Triggers("")
	if tv := s.viewTriggers(tl); len(tv) != 1 || tv[0].waitStatus == nil || tv[0].waitStatus.Reporter.State != "serving" {
		t.Fatalf("trigger view %+v", tv)
	}
	// A wait whose condition can't be evaluated on the state (made before the state was known) is dropped, and its
	// session told why
	bw, _ := wt.Add(waits.Wait{Source: "gitlab", Key: "h/a!1", Until: "reveiws > 0", Session: "w1"})
	s.stateChanged("gitlab", "h/a!1")
	waitUntil(t, "the broken wait's session told", func() bool {
		mu.Lock()
		defer mu.Unlock()
		last := woken[len(woken)-1]
		return strings.Contains(last, "broken:") && strings.Contains(last, "reveiws")
	})
	for _, x := range wt.List("") {
		if x.ID == bw.ID {
			t.Error("a broken wait was kept")
		}
	}
	// A session closed (no pane shows it any more) takes its waits with it
	_, _ = wt.Add(waits.Wait{Source: "gitlab", Key: "h/a!1", Until: "reviews > 100", Session: "w2"})
	if err := s.Hub.Shutdown(context.Background(), "w2"); err != nil {
		t.Fatal(err)
	}
	if l := wt.List("w2"); len(l) != 0 {
		t.Errorf("a closed session's waits kept: %+v", l)
	}
	// A list in the state shows as its length in the wake message, not dumped
	if l := fieldsLine(map[string]any{"notes": []any{map[string]any{"id": 1.0}, map[string]any{"id": 2.0}}, "reviews": 2.0}); l != "notes=[2 项] reviews=2" {
		t.Errorf("fieldsLine %q", l)
	}
	// A state of a source this add-on doesn't declare is dropped
	if _, ok := wt.StateOf("jira", "X-1"); ok {
		t.Error("an undeclared source's state was kept")
	}
}

func waitUntil(t *testing.T, what string, ok func() bool) {
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
