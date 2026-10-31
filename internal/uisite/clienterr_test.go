package uisite

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// Errors the page reports land in the log once each, also from a stale build, so they can still be read after the
// rescue panel cleared them or rolled the UI back.
func TestClientErrorsAreLogged(t *testing.T) {
	var buf bytes.Buffer
	r := &Renderer{dir: "/x", log: slog.New(slog.NewTextHandler(&buf, nil))}
	boom := []errorItem{{Source: "react", Message: "boom", Detail: "at A\nat B"}}
	r.ReportClientErrors("old", boom)
	r.ReportClientErrors("old", boom)
	r.ReportClientErrors("old", []errorItem{{Source: "react", Message: "other"}})
	out := buf.String()
	if n := strings.Count(out, "msg=boom") + strings.Count(out, "message=boom"); n != 1 {
		t.Fatalf("boom logged %d times, want 1:\n%s", n, out)
	}
	if !strings.Contains(out, "message=other") {
		t.Fatalf("second error missing:\n%s", out)
	}
	if len(r.clientErr) != 0 {
		t.Fatalf("stale-build errors kept for the panel: %v", r.clientErr)
	}
}
