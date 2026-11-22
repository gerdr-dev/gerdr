package server

import (
	"encoding/binary"
	"encoding/json"
	"image"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/gerdr-dev/gerdr/internal/screen"
)

// busySource: a page that never stops moving (a WebGL demo): a new picture, entirely different, on every Changed.
type busySource struct {
	ch chan struct{}
	n  int
}

func (b *busySource) Grab(int) (*image.RGBA, error) {
	b.n++
	m := image.NewRGBA(image.Rect(0, 0, 128, 128))
	for i := range m.Pix {
		m.Pix[i] = byte(b.n * 37)
	}
	return m, nil
}
func (b *busySource) Cursor() (float64, float64, bool) { return 0, 0, false }
func (b *busySource) Do(screen.Input) error            { return nil }
func (b *busySource) Off() string                      { return "" }
func (b *busySource) Changed() <-chan struct{}         { return b.ch }
func (b *busySource) Page() *screen.Page               { return nil }
func (b *busySource) Size(int, int)                    {}
func (b *busySource) Selection() string                { return "" }

// Over the relay a busy page gets one picture at a time and a pause after each, so the tunnel isn't kept full and
// the rest of the app's traffic gets through (screenstream.go, inFlight / notBefore).
func TestStreamPacesARemoteViewer(t *testing.T) {
	src := &busySource{ch: make(chan struct{})}
	stop := make(chan struct{})
	defer close(stop)
	go func() { // changes as fast as it can be told
		for {
			select {
			case src.ch <- struct{}{}:
			case <-stop:
				return
			}
		}
	}()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := screenUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		streamPictures(conn, r, src, false)
	}))
	defer srv.Close()
	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/?w=128", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()

	// One reader: a gorilla connection whose read timed out is broken for good, so waits are selects
	msgs := make(chan screen.Header, 64)
	go func() {
		for {
			_, b, err := ws.ReadMessage()
			if err != nil {
				close(msgs)
				return
			}
			var h screen.Header
			n := binary.BigEndian.Uint32(b)
			if json.Unmarshal(b[4:4+n], &h) == nil {
				msgs <- h
			}
		}
	}()
	read := func(wait time.Duration) (screen.Header, bool) {
		select {
		case h, ok := <-msgs:
			return h, ok
		case <-time.After(wait):
			return screen.Header{}, false
		}
	}
	got := func(seq int64) {
		m, _ := json.Marshal(map[string]any{"t": "got", "seq": seq})
		if err := ws.WriteMessage(websocket.TextMessage, m); err != nil {
			t.Fatal(err)
		}
	}
	// Warm-up: the stream knows the viewer acknowledges only after its first got; until then it sends freely. Ack
	// everything until it goes quiet, as the viewer would.
	for end := time.Now().Add(300 * time.Millisecond); time.Now().Before(end); {
		if h, ok := read(50 * time.Millisecond); ok && len(h.Rects) > 0 {
			got(h.Seq)
		}
	}

	const draw = 60 * time.Millisecond // the picture's trip + drawing, as the viewer sees it
	var gotAt time.Time
	pictures := 0
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline) && pictures < 8; {
		h, ok := read(time.Second)
		if !ok {
			t.Fatal("no picture for a second: the stream stalled")
		}
		if len(h.Rects) == 0 {
			continue
		}
		pictures++
		// no picture until half the last one's trip has passed since it was drawn
		if pictures > 1 {
			if gap := time.Since(gotAt); gap < draw/2-10*time.Millisecond {
				t.Fatalf("picture %d came %v after the last was drawn, want ≥ %v", pictures, gap, draw/2)
			}
		}
		// while this one is "on its way" nothing else may arrive: one at a time
		if h2, ok := read(draw); ok && len(h2.Rects) > 0 {
			t.Fatalf("a second picture (seq %d) while seq %d wasn't drawn yet", h2.Seq, h.Seq)
		}
		gotAt = time.Now()
		got(h.Seq)
	}
	if pictures < 4 {
		t.Fatalf("only %d pictures in 2 s: the stream stalled", pictures)
	}
}
