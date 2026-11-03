package screen

import (
	"image/jpeg"
	"os"
	"testing"
	"time"
)

// GERDR_SCREEN_LIVE=1 go test ./internal/screen -run Live -v: times a real capture (needs Screen Recording for
// the process that runs it) and writes the picture to $TMPDIR/gerdr-live.jpg to check the colours.
func TestLiveGrab(t *testing.T) {
	if os.Getenv("GERDR_SCREEN_LIVE") == "" {
		t.Skip("set GERDR_SCREEN_LIVE=1")
	}
	var d Differ
	for i := 0; i < 4; i++ {
		t0 := time.Now()
		m, err := Grab(1600)
		if err != nil {
			t.Fatal(err)
		}
		t1 := time.Now()
		rects, payload, full, err := d.Diff(m, 60)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("grab %v diff+encode %v size %v full %v rects %d bytes %d", t1.Sub(t0), time.Since(t1), m.Bounds().Size(), full, len(rects), len(payload))
		if i == 0 {
			f, _ := os.Create(os.TempDir() + "/gerdr-live.jpg")
			_ = jpeg.Encode(f, m, &jpeg.Options{Quality: 80})
			f.Close()
		}
		time.Sleep(300 * time.Millisecond)
	}
}
