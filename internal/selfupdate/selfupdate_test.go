package selfupdate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/andybalholm/brotli"
)

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"0.2.21", "0.2.20", true},
		{"0.2.20", "0.2.20", false},
		{"0.2.20-3-gabc1234", "0.2.20", true},        // a local build is ahead of the release it was built on
		{"0.2.21", "0.2.20-70-gabc1234-dirty", true}, // and behind the next one
		{"v0.2.20-3-gabc1234-dirty", "0.2.20", true}, // git describe's v is accepted
		{"0.1.0-dev", "0.0.1", false},                // not a release or a describe: never
		{"0.2.20", "0.2.20-3-gabc1234", false},
	} {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// fakeGerdr: a script that answers `gerdr version` like the real one.
func fakeGerdr(t *testing.T, path, version string) []byte {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake backend is a shell script")
	}
	b := []byte("#!/bin/sh\necho " + version + "\n")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o755); err != nil {
		t.Fatal(err)
	}
	return b
}

// feedServer serves update.json and the .br next to it, the way a release does.
func feedServer(t *testing.T, version string, payload []byte, corrupt bool) *httptest.Server {
	t.Helper()
	var packed bytes.Buffer
	w := brotli.NewWriter(&packed)
	_, _ = w.Write(payload)
	_ = w.Close()
	sum := sha256.Sum256(packed.Bytes())
	file := "gerdr-" + version + "-" + PlatKey() + ".br"
	body := packed.Bytes()
	if corrupt {
		body = append([]byte{}, body...)
		body[len(body)/2] ^= 0xff
	}
	feed, _ := json.Marshal(map[string]any{
		"version": version,
		"backend": map[string]any{PlatKey(): map[string]any{"file": file, "size": len(body), "sha256": hex.EncodeToString(sum[:])}},
	})
	mux := http.NewServeMux()
	mux.HandleFunc("/update.json", func(rw http.ResponseWriter, r *http.Request) { _, _ = rw.Write(feed) })
	mux.HandleFunc("/"+file, func(rw http.ResponseWriter, r *http.Request) { _, _ = rw.Write(body) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestCheckDownloadsAndPicksTheBackend(t *testing.T) {
	dir := t.TempDir()
	bundled := filepath.Join(dir, "bundle", "gerdr")
	fakeGerdr(t, bundled, "v0.2.20")
	newBin := []byte("#!/bin/sh\necho 0.2.21\n")
	srv := feedServer(t, "0.2.21", newBin, false)
	t.Setenv("GERDR_UPDATE_FEED", srv.URL+"/update.json")

	u := New(filepath.Join(dir, "data"), bundled, "0.2.20", "", true)
	if !u.enabled {
		t.Fatal("updater should be on for a packaged release")
	}
	if b := u.Backend(); !b.Bundled || b.Version != "0.2.20" {
		t.Fatalf("before: %+v, want the bundle's 0.2.20", b)
	}
	msg := u.Check()
	if !strings.Contains(msg, "0.2.21") {
		t.Fatalf("check: %q", msg)
	}
	b := u.Backend()
	if b.Bundled || b.Version != "0.2.21" {
		t.Fatalf("after: %+v, want the downloaded 0.2.21", b)
	}
	if got, _ := os.ReadFile(b.Path); !bytes.Equal(got, newBin) {
		t.Fatalf("unpacked backend differs: %q", got)
	}
	if st, _ := os.Stat(b.Path); st.Mode()&0o111 == 0 {
		t.Fatal("downloaded backend isn't executable")
	}
	if p := u.Pending(); p == nil || p.Version != "0.2.21" {
		t.Fatalf("pending: %+v", p)
	}
	// Checking again before it's applied: it says it waits (not "up to date"), and downloads nothing again.
	if msg := u.Check(); !strings.Contains(msg, "0.2.21") || !(strings.Contains(msg, "waiting") || strings.Contains(msg, "等待")) {
		t.Fatalf("second check: %q", msg)
	}
	// The running gerdr catches up: nothing pending any more.
	u.NoteRunning("0.2.21")
	if p := u.Pending(); p != nil {
		t.Fatalf("pending after the service runs it: %+v", p)
	}
}

func TestCorruptDownloadIsNotUsed(t *testing.T) {
	dir := t.TempDir()
	bundled := filepath.Join(dir, "bundle", "gerdr")
	fakeGerdr(t, bundled, "0.2.20")
	srv := feedServer(t, "0.2.21", []byte("#!/bin/sh\necho 0.2.21\n"), true)
	t.Setenv("GERDR_UPDATE_FEED", srv.URL+"/update.json")

	u := New(filepath.Join(dir, "data"), bundled, "0.2.20", "", true)
	if msg := u.Check(); !strings.Contains(msg, "corrupted") {
		t.Fatalf("check: %q, want a corrupted download", msg)
	}
	if b := u.Backend(); !b.Bundled {
		t.Fatalf("a corrupted download got picked: %+v", b)
	}
	if p := u.Pending(); p != nil {
		t.Fatalf("pending after a failed download: %+v", p)
	}
}

func TestLocalBuildDoesntUpdate(t *testing.T) {
	dir := t.TempDir()
	bundled := filepath.Join(dir, "gerdr")
	fakeGerdr(t, bundled, "0.1.0-dev")
	if u := New(dir, bundled, "0.1.0-dev", "", true); u.enabled {
		t.Fatal("a 0.1.0-dev build should not update")
	}
	if u := New(dir, bundled, "0.2.20", "", false); u.enabled {
		t.Fatal("not packaged: should not update")
	}
}

// A release whose shell id isn't ours: the whole package is downloaded (not the backend), and waits for a click.
func TestOtherShellDownloadsTheApp(t *testing.T) {
	dir := t.TempDir()
	bundled := filepath.Join(dir, "bundle", "gerdr")
	fakeGerdr(t, bundled, "0.2.20")
	pkg := []byte("a whole new App")
	sum := sha256.Sum256(pkg)
	file := "Gerdr-0.2.21-mac-arm64.dmg"
	feed, _ := json.Marshal(map[string]any{
		"version": "0.2.21", "shell": "new-shell",
		"backend": map[string]any{PlatKey(): map[string]any{"file": "never.br", "size": 1, "sha256": "x"}},
		"app":     map[string]any{PlatKey(): map[string]any{"file": file, "size": len(pkg), "sha256": hex.EncodeToString(sum[:])}},
	})
	hits := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/update.json", func(rw http.ResponseWriter, r *http.Request) { _, _ = rw.Write(feed) })
	mux.HandleFunc("/"+file, func(rw http.ResponseWriter, r *http.Request) { hits++; _, _ = rw.Write(pkg) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	t.Setenv("GERDR_UPDATE_FEED", srv.URL+"/update.json")

	u := New(filepath.Join(dir, "data"), bundled, "0.2.20", "old-shell", true)
	if msg := u.Check(); !strings.Contains(msg, "0.2.21") {
		t.Fatalf("check: %q", msg)
	}
	p := u.Pending()
	if p == nil || p.Kind != "app" || p.Version != "0.2.21" {
		t.Fatalf("pending: %+v, want the 0.2.21 app", p)
	}
	if got, _ := os.ReadFile(p.File); !bytes.Equal(got, pkg) {
		t.Fatalf("downloaded package: %q", got)
	}
	if b := u.Backend(); !b.Bundled {
		t.Fatalf("the backend changed with an app update pending: %+v", b)
	}
	// The running gerdr reporting its version doesn't settle a whole-App update.
	u.NoteRunning("0.2.20")
	if p := u.Pending(); p == nil || p.Kind != "app" {
		t.Fatalf("pending after NoteRunning: %+v", p)
	}
	// Checking again doesn't download it again.
	u.mu.Lock()
	u.pending = nil
	u.mu.Unlock()
	u.Check()
	if hits != 1 {
		t.Fatalf("package downloaded %d times", hits)
	}
}
