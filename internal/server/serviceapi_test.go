package server

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Settings → "Restart Gerdr": refused where nothing would start the process again (a terminal's gerdr), the hook
// runs after the answer went out, and the phone can't reach it at all.
func TestRestartAPI(t *testing.T) {
	s, _, _ := remoteServer(t)
	s.Hub = reportHub(t)
	lh := s.Handler()
	rh := RemoteHandler(lh)
	local := func(method string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://127.0.0.1:7870/api/restart", strings.NewReader("{}"))
		r.Header.Set("Authorization", "Bearer "+s.Token)
		r.Header.Set("X-Gerdr", "1")
		w := httptest.NewRecorder()
		lh.ServeHTTP(w, r)
		return w
	}
	if w := local("GET"); w.Code != 200 || !strings.Contains(w.Body.String(), `"can":false`) || !strings.Contains(w.Body.String(), `"busy"`) {
		t.Fatalf("terminal gerdr: %d %s", w.Code, w.Body)
	}
	if w := local("POST"); w.Code != 409 {
		t.Fatalf("a terminal's gerdr can't be restarted from the UI, got %d", w.Code)
	}

	called := make(chan struct{}, 1)
	s.Restart = func() { called <- struct{}{} }
	if w := local("GET"); !strings.Contains(w.Body.String(), `"can":true`) {
		t.Fatalf("with a hook: %s", w.Body)
	}
	if w := local("POST"); w.Code != 200 {
		t.Fatalf("restart: %d %s", w.Code, w.Body)
	}
	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatal("restart hook never ran")
	}

	for _, m := range []string{"GET", "POST"} {
		if w := do(rh, m, "/api/restart", strings.NewReader("{}"), csrf, hdr("Origin", "https://"+testHost)); w.Code != 403 {
			t.Fatalf("remote %s /api/restart must be refused, got %d", m, w.Code)
		}
	}
}
