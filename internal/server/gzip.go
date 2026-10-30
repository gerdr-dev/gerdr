package server

import (
	"bufio"
	"compress/gzip"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// gzipped compresses text responses (the UI's modules and vendor bundle, the page, API JSON) for clients that
// aren't on this machine. The UI is several MB of JavaScript (React's development build among it), which over the
// relay or a phone's mobile data is most of the first load; JavaScript and JSON shrink to roughly a quarter.
//
// Left alone:
//   - loopback requests (the App, a local browser): bytes cost nothing there and compressing costs CPU;
//   - WebSocket upgrades and SSE (text/event-stream isn't in the list): long-lived streams, and Hijack must still work;
//   - Range requests: byte offsets would refer to the compressed stream;
//   - anything already encoded, or of a type that doesn't compress (images, video, PDFs).
//
// The relay forwards TLS untouched (docs/dev/REMOTE.md), so what leaves here is what the phone receives.
func gzipped(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") || r.Header.Get("Range") != "" ||
			r.Header.Get("Upgrade") != "" || loopbackHost(r.Host) {
			next.ServeHTTP(w, r)
			return
		}
		g := &gzipWriter{ResponseWriter: w}
		defer g.close()
		next.ServeHTTP(g, r)
	})
}

func loopbackHost(hostport string) bool {
	h := hostport
	if host, _, err := net.SplitHostPort(hostport); err == nil {
		h = host
	}
	h = strings.Trim(h, "[]")
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

var compressibleTypes = []string{"javascript", "json", "text/html", "text/css", "text/plain", "text/markdown", "svg", "xml", "typescript"}

func compressible(ct string) bool {
	ct = strings.ToLower(ct)
	for _, t := range compressibleTypes {
		if strings.Contains(ct, t) {
			return true
		}
	}
	return false
}

var gzipPool = sync.Pool{New: func() any { w, _ := gzip.NewWriterLevel(nil, gzip.DefaultCompression); return w }}

type gzipWriter struct {
	http.ResponseWriter
	gz      *gzip.Writer
	decided bool
}

// decide once, at the status line, from the headers the handler has set by then.
func (g *gzipWriter) decide(code int) {
	g.decided = true
	h := g.Header()
	if code < 200 || code == http.StatusNoContent || code == http.StatusNotModified || h.Get("Content-Encoding") != "" || !compressible(h.Get("Content-Type")) {
		return
	}
	if n, err := strconv.Atoi(h.Get("Content-Length")); err == nil && n < 1024 {
		return
	}
	h.Del("Content-Length")
	h.Set("Content-Encoding", "gzip")
	h.Add("Vary", "Accept-Encoding")
	g.gz = gzipPool.Get().(*gzip.Writer)
	g.gz.Reset(g.ResponseWriter)
}

func (g *gzipWriter) WriteHeader(code int) {
	if !g.decided {
		g.decide(code)
	}
	g.ResponseWriter.WriteHeader(code)
}

func (g *gzipWriter) Write(b []byte) (int, error) {
	if !g.decided {
		if g.Header().Get("Content-Type") == "" {
			g.Header().Set("Content-Type", http.DetectContentType(b)) // what net/http would do, but it must be known now
		}
		g.WriteHeader(http.StatusOK)
	}
	if g.gz != nil {
		return g.gz.Write(b)
	}
	return g.ResponseWriter.Write(b)
}

func (g *gzipWriter) Flush() {
	if g.gz != nil {
		_ = g.gz.Flush()
	}
	if f, ok := g.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (g *gzipWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := g.ResponseWriter.(http.Hijacker); ok && !g.decided {
		return hj.Hijack()
	}
	return nil, nil, errors.New("gzip: can't hijack")
}

func (g *gzipWriter) Unwrap() http.ResponseWriter { return g.ResponseWriter }

func (g *gzipWriter) close() {
	if g.gz != nil {
		_ = g.gz.Close()
		g.gz.Reset(nil)
		gzipPool.Put(g.gz)
		g.gz = nil
	}
}
