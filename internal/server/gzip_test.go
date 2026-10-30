package server

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGzipped(t *testing.T) {
	big := strings.Repeat("export const x = 1;\n", 500)
	h := gzipped(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/js":
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			w.Write([]byte(big))
		case "/small":
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Length", "2")
			w.Write([]byte("{}"))
		case "/png":
			w.Header().Set("Content-Type", "image/png")
			w.Write([]byte(big))
		case "/sse":
			w.Header().Set("Content-Type", "text/event-stream")
			w.(http.Flusher).Flush()
			w.Write([]byte("data: {}\n\n"))
		case "/sniff":
			w.Write([]byte("<!DOCTYPE html><html>" + big))
		}
	}))
	get := func(host, path string, hdr map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", path, nil)
		req.Host = host
		req.Header.Set("Accept-Encoding", "gzip, deflate, br")
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	remote := "gerdr.example.com"

	w := get(remote, "/js", nil)
	if w.Header().Get("Content-Encoding") != "gzip" || w.Header().Get("Content-Length") != "" {
		t.Fatalf("js not gzipped: %v", w.Header())
	}
	zr, err := gzip.NewReader(w.Body)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := io.ReadAll(zr); string(b) != big {
		t.Fatal("round trip mismatch")
	}
	if w.Body.Len() >= len(big)/4 {
		t.Fatalf("barely compressed: %d of %d", w.Body.Len(), len(big))
	}
	if w := get(remote, "/sniff", nil); w.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("sniffed html not gzipped: %v", w.Header())
	}
	for _, c := range []struct {
		host, path string
		hdr        map[string]string
	}{
		{"127.0.0.1:7870", "/js", nil},
		{"localhost:7870", "/js", nil},
		{"[::1]:7870", "/js", nil},
		{remote, "/small", nil},
		{remote, "/png", nil},
		{remote, "/sse", nil},
		{remote, "/js", map[string]string{"Range": "bytes=0-10"}},
		{remote, "/js", map[string]string{"Upgrade": "websocket"}},
	} {
		if w := get(c.host, c.path, c.hdr); w.Header().Get("Content-Encoding") != "" {
			t.Errorf("%s %s %v: should not be gzipped", c.host, c.path, c.hdr)
		}
	}
}
