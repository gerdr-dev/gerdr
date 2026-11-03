package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A file link opens without any cookie, on the remote door too, and only for its own file.
func TestFileLink(t *testing.T) {
	s, h, rh := remoteServer(t)
	p := filepath.Join(t.TempDir(), "报告.pdf")
	if err := os.WriteFile(p, []byte("%PDF-1.4 hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	if w := do(rh, "POST", "/api/fs/link", body(`{"path":"`+p+`"}`)(), csrf, hdr("Origin", "https://"+testHost)); w.Code != 401 {
		t.Fatalf("issuing a link must need a signed-in device, got %d", w.Code)
	}
	w := do(h, "POST", "/api/fs/link", body(`{"path":"`+p+`"}`)(), csrf, hdr("Authorization", "Bearer "+s.Token))
	if w.Code != 200 {
		t.Fatalf("issue: %d %s", w.Code, w.Body)
	}
	var out struct{ URL string }
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if !strings.HasPrefix(out.URL, "/f/") || !strings.HasSuffix(out.URL, "/%E6%8A%A5%E5%91%8A.pdf") {
		t.Fatalf("unexpected url %q", out.URL)
	}

	w = do(rh, "GET", out.URL, nil)
	if w.Code != 200 || w.Body.String() != "%PDF-1.4 hello" || w.Header().Get("Content-Type") != "application/pdf" {
		t.Fatalf("remote open without cookie: %d %q %q", w.Code, w.Header().Get("Content-Type"), w.Body)
	}
	if w := do(h, "GET", out.URL+"?download=1", nil); w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("local download: %d %q", w.Code, w.Header().Get("Content-Disposition"))
	}
	if w := do(rh, "GET", "/f/nope/x.pdf", nil); w.Code != 404 {
		t.Fatalf("unknown token should be 404, got %d", w.Code)
	}
}

// A link works for linkTTL from issue, used or not.
func TestFileLinkExpiry(t *testing.T) {
	var l fileLinks
	t0 := time.Now()
	a, _ := l.issue("/a", t0)
	if p, ok := l.lookup(a, t0.Add(linkTTL-time.Second)); !ok || p != "/a" {
		t.Fatal("link refused before linkTTL")
	}
	if _, ok := l.lookup(a, t0.Add(linkTTL+time.Second)); ok {
		t.Fatal("link outlived linkTTL")
	}
}

// An .html link renders as a page, but sandboxed without allow-same-origin so it can't act as us.
func TestFileLinkHTMLPage(t *testing.T) {
	s, h, rh := remoteServer(t)
	p := filepath.Join(t.TempDir(), "index.html")
	if err := os.WriteFile(p, []byte("<p>hi</p>"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := do(h, "POST", "/api/fs/link", body(`{"path":"`+p+`"}`)(), csrf, hdr("Authorization", "Bearer "+s.Token))
	var out struct{ URL string }
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	w = do(rh, "GET", out.URL, nil)
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("Content-Type = %q, want text/html", ct)
	}
	csp := w.Header().Get("Content-Security-Policy")
	if !strings.HasPrefix(csp, "sandbox ") || strings.Contains(csp, "allow-same-origin") {
		t.Fatalf("CSP = %q, want a sandbox without allow-same-origin", csp)
	}
	if w := do(h, "GET", out.URL+"?download=1", nil); strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
		t.Fatal("download must not be served as a page")
	}
}
