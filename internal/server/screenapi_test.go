package server

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gerdr-dev/gerdr/internal/config"
)

// The switch gates the screenshot and the input; nothing is captured or posted while it is off.
func TestScreenOff(t *testing.T) {
	s := &Server{Config: config.NewStore(filepath.Join(t.TempDir(), "config.json"), `{}`)}
	mux := http.NewServeMux()
	s.screenRoutes(mux)
	for _, c := range []struct{ method, path, body string }{
		{"GET", "/api/screen/shot", ""},
		{"POST", "/api/screen/input", `{"kind":"text","text":"x"}`},
		{"GET", "/api/screen/stream", ""},
	} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(c.method, c.path, strings.NewReader(c.body)))
		if w.Code != http.StatusForbidden {
			t.Fatalf("%s %s with the switch off: want 403, got %d", c.method, c.path, w.Code)
		}
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/screen", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"enabled":false`) {
		t.Fatalf("status: %d %s", w.Code, w.Body)
	}
}

// Asking macOS for the grants pops its prompts on this computer's screen: never from a remote device.
func TestScreenRequestAccessLocalOnly(t *testing.T) {
	_, _, rh := remoteServer(t)
	if w := do(rh, "POST", "/api/screen/request-access", body("{}")(), csrf, hdr("Origin", "https://"+testHost)); w.Code != http.StatusForbidden {
		t.Fatalf("remote request-access: want 403, got %d", w.Code)
	}
}
