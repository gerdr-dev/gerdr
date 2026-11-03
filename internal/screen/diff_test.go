package screen

import (
	"encoding/binary"
	"encoding/json"
	"image"
	"testing"
)

func pic(w, h int) *image.RGBA { return image.NewRGBA(image.Rect(0, 0, w, h)) }

func TestDiff(t *testing.T) {
	var d Differ
	a := pic(300, 200)
	if rects, _, full, _ := d.Diff(a, 60); !full || len(rects) != 1 || rects[0][2] != 300 {
		t.Fatalf("first frame should be full: %v %v", full, rects)
	}
	b := pic(300, 200)
	if rects, payload, full, _ := d.Diff(b, 60); full || len(rects) != 0 || len(payload) != 0 {
		t.Fatalf("unchanged frame should send nothing: %v %v", full, rects)
	}
	// One pixel in the second tile row, third tile column: one 64×64 patch at (128, 64)
	c := pic(300, 200)
	c.Pix[(70*c.Stride)+130*4] = 255
	rects, payload, full, _ := d.Diff(c, 60)
	if full || len(rects) != 1 || rects[0] != (Rect{128, 64, 64, 64, len(payload)}) {
		t.Fatalf("one changed pixel: %v %v", full, rects)
	}
	// Two neighbouring tiles in a row merge into one patch; the last column is narrower (300 = 4×64 + 44)
	e := pic(300, 200)
	copy(e.Pix, c.Pix)
	e.Pix[(10*e.Stride)+200*4] = 1
	e.Pix[(10*e.Stride)+299*4] = 1
	if rects, _, _, _ := d.Diff(e, 60); len(rects) != 1 || rects[0][0] != 192 || rects[0][2] != 108 {
		t.Fatalf("adjacent tiles should merge: %v", rects)
	}
	// Mostly new: the whole picture
	f := pic(300, 200)
	for i := range f.Pix {
		f.Pix[i] = 9
	}
	if _, _, full, _ := d.Diff(f, 60); !full {
		t.Fatal("a mostly new frame should go out whole")
	}
}

func TestMessage(t *testing.T) {
	b := Message(Header{Seq: 3, W: 10, H: 5, Rects: []Rect{{0, 0, 10, 5, 2}}}, []byte{1, 2})
	n := binary.BigEndian.Uint32(b)
	var h Header
	if err := json.Unmarshal(b[4:4+n], &h); err != nil || h.Seq != 3 || len(h.Rects) != 1 {
		t.Fatalf("header: %v %+v", err, h)
	}
	if string(b[4+n:]) != "\x01\x02" {
		t.Fatal("payload")
	}
}
