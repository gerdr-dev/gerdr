package llm

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A 200 stream that sends a little and then nothing (a proxy holding a connection whose network is gone) must end
// as a dropped stream, not block the turn forever; one that keeps sending must not be cut however long it runs.
func TestStreamIdleTimeout(t *testing.T) {
	old := streamIdle
	streamIdle = 150 * time.Millisecond
	defer func() { streamIdle = old }()

	release := make(chan struct{})
	defer close(release)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		if r.URL.Path == "/alive" {
			for i := 0; i < 6; i++ { // 6 × 60ms: longer than streamIdle overall, never silent that long
				io.WriteString(w, "data: {}\n\n")
				f.Flush()
				time.Sleep(60 * time.Millisecond)
			}
			io.WriteString(w, "data: [DONE]\n\n")
			return
		}
		io.WriteString(w, "data: {}\n\n")
		f.Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()

	read := func(path string) (int, error) {
		rc, err := postURL(context.Background(), srv.URL+path, nil, nil, map[string]any{})
		if err != nil {
			t.Fatal(err)
		}
		defer rc.Close()
		n := 0
		err = streamErr(context.Background(), readSSE(rc, func([]byte) error { n++; return nil }))
		return n, err
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		n, err := read("/stall")
		if n != 1 || !errors.Is(err, errStreamIdle) || !strings.Contains(err.Error(), "stream disconnected") {
			t.Errorf("stalled stream: %d events, err %v", n, err)
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a silent stream was never given up on")
	}

	if n, err := read("/alive"); n != 6 || err != nil {
		t.Fatalf("live stream: %d events, err %v", n, err)
	}
}
