package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gerdr-dev/gerdr/internal/version"
)

// The App's page reports locally; a phone reads it and presses buttons; a remote device can't fake the report.
func TestAppUpdateRelay(t *testing.T) {
	s, lh, rh := remoteServer(t)
	code, _ := s.Remote.Auth.MintCode()
	_, tok, _ := s.Remote.Auth.Redeem(code, "Android · App", "")
	phone := cookie(&http.Cookie{Name: "gerdr_dev", Value: tok})
	origin := hdr("Origin", "https://"+testHost)
	local := hdr("Authorization", "Bearer "+s.Token)

	if w := do(rh, "POST", "/api/app-update/op", body(`{"op":"check"}`)(), phone, csrf, origin); w.Code != 409 {
		t.Fatalf("no App window reported: want 409, got %d", w.Code)
	}
	if w := do(rh, "PUT", "/api/app-update", body(`{"client":"x","status":{"enabled":true}}`)(), phone, csrf, origin); w.Code != 403 {
		t.Fatalf("a remote device must not report the App status, got %d", w.Code)
	}
	if w := do(lh, "PUT", "/api/app-update", body(`{"client":"app1","status":{"enabled":true}}`)(), local, csrf); w.Code != 200 {
		t.Fatalf("report: %d %s", w.Code, w.Body)
	}
	if w := do(rh, "GET", "/api/app-update", nil, phone); w.Code != 200 || !strings.Contains(w.Body.String(), `"client":"app1"`) || !strings.Contains(w.Body.String(), `"enabled":true`) {
		t.Fatalf("phone read: %d %s", w.Code, w.Body)
	}
	if w := do(rh, "POST", "/api/app-update/op", body(`{"op":"check"}`)(), phone, csrf, origin); w.Code != 200 {
		t.Fatalf("op: %d %s", w.Code, w.Body)
	}
	if w := do(rh, "POST", "/api/app-update/op", body(`{"op":"rm"}`)(), phone, csrf, origin); w.Code != 400 {
		t.Fatalf("unknown op: want 400, got %d", w.Code)
	}

	// A window that stopped reporting is gone
	s.appUpdate.at = time.Now().Add(-appUpdateFresh - time.Second)
	if w := do(rh, "GET", "/api/app-update", nil, phone); !strings.Contains(w.Body.String(), `"status":null`) {
		t.Fatalf("stale report should read as none: %s", w.Body)
	}
}

// Several App pages report: the phone sees one, the truest, not whichever wrote last.
func TestAppUpdatePicksTruest(t *testing.T) {
	var a appUpdate
	now := time.Now()
	a.put("stale", json.RawMessage(`{"running":"0.0.1-old","checkedAt":200}`), now)
	a.put("live", json.RawMessage(`{"running":"`+version.Version+`","checkedAt":100}`), now)
	a.put("old", json.RawMessage(`{"running":"`+version.Version+`","checkedAt":50}`), now)
	for i := 0; i < 3; i++ {
		a.put("stale", json.RawMessage(`{"running":"0.0.1-old","checkedAt":200}`), now)
		if _, c := a.get(now); c != "live" {
			t.Fatalf("want the report running this backend with the latest check, got %q", c)
		}
	}
	a.reports["live"] = appUpdateReport{at: now.Add(-appUpdateFresh - time.Second)}
	if _, c := a.get(now); c != "old" {
		t.Fatalf("a gone page drops out: got %q", c)
	}
}

// A relaunched App: its old process's last report is still fresh, but buttons must go to the new one.
func TestAppUpdatePicksNewestApp(t *testing.T) {
	var a appUpdate
	now := time.Now()
	v := version.Version
	a.put("gone", json.RawMessage(`{"running":"`+v+`","shellStartedAt":1000,"checkedAt":500}`), now)
	a.put("new", json.RawMessage(`{"running":"`+v+`","shellStartedAt":2000,"checkedAt":0}`), now)
	if _, c := a.get(now); c != "new" {
		t.Fatalf("want the newest App process, got %q", c)
	}
}
