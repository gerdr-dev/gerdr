package screen

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"image"
	"image/jpeg"
)

// The stream sends only what changed since the frame this viewer last got, like a video's delta frames but with
// nothing a browser can't draw: the picture is cut into tiles, each run of changed tiles in a tile row goes out as
// one small JPEG, and the page pastes them onto a canvas. Typing a letter or opening a menu changes a handful of
// tiles (a few KB) where the whole screen is 100–400 KB.

const tile = 64 // px of the shrunk picture; a multiple of JPEG's 16 px blocks, so the edges line up

// Rect is one patch: where it goes on the picture and how many bytes of the payload are its JPEG.
type Rect [5]int // x, y, w, h, len

// Header leads every frame message. A frame with no rects still goes out when it acknowledges an input or the
// pointer moved, so the page can time the round trip.
type Header struct {
	Seq   int64       `json:"seq"`
	W     int         `json:"w"`
	H     int         `json:"h"`
	Full  bool        `json:"full,omitempty"` // a whole picture: (re)size the canvas
	Ack   int64       `json:"ack"`            // the last input done before this capture
	Cur   *[2]float64 `json:"cur,omitempty"`  // the pointer, as fractions of the display (screenshots don't show it)
	Rects []Rect      `json:"rects"`
	Err   string      `json:"err,omitempty"`      // capturing failed (no grant, switched off)
	InErr string      `json:"inputErr,omitempty"` // an input failed
}

// Differ remembers the last picture sent to one viewer.
type Differ struct {
	prev *image.RGBA
}

// Reset makes the next frame a full one (new viewer settings, a refresh asked for).
func (d *Differ) Reset() { d.prev = nil }

// Diff encodes what changed between the last picture and cur at JPEG quality q: the rects and their bytes,
// concatenated. full is set when it is the whole picture.
func (d *Differ) Diff(cur *image.RGBA, q int) (rects []Rect, payload []byte, full bool, err error) {
	b := cur.Bounds()
	prev := d.prev
	d.prev = cur
	if prev == nil || prev.Bounds() != b {
		return encodeRects(cur, []image.Rectangle{b}, q, true)
	}
	var changed []image.Rectangle
	area := 0
	for ty := 0; ty < b.Dy(); ty += tile {
		th := min(tile, b.Dy()-ty)
		run := -1 // start x of the current run of changed tiles
		// One step past the last tile closes a run that reaches the right edge (the width needn't be a multiple)
		for tx := 0; ; tx += tile {
			end := tx >= b.Dx()
			dirty := !end && tileChanged(prev, cur, tx, ty, min(tile, b.Dx()-tx), th)
			if dirty && run < 0 {
				run = tx
			}
			if !dirty && run >= 0 {
				r := image.Rect(run, ty, min(tx, b.Dx()), ty+th)
				changed = append(changed, r)
				area += r.Dx() * r.Dy()
				run = -1
			}
			if end {
				break
			}
		}
	}
	// Mostly new (a window switched, a page scrolled): one picture is smaller than many patches
	if area*10 > b.Dx()*b.Dy()*6 {
		return encodeRects(cur, []image.Rectangle{b}, q, true)
	}
	return encodeRects(cur, changed, q, false)
}

func tileChanged(a, b *image.RGBA, x, y, w, h int) bool {
	for yy := y; yy < y+h; yy++ {
		o := yy*a.Stride + x*4
		if !bytes.Equal(a.Pix[o:o+w*4], b.Pix[o:o+w*4]) {
			return true
		}
	}
	return false
}

func encodeRects(m *image.RGBA, rs []image.Rectangle, q int, full bool) ([]Rect, []byte, bool, error) {
	var buf bytes.Buffer
	out := make([]Rect, 0, len(rs))
	for _, r := range rs {
		n := buf.Len()
		if err := jpeg.Encode(&buf, m.SubImage(r), &jpeg.Options{Quality: q}); err != nil {
			return nil, nil, false, err
		}
		out = append(out, Rect{r.Min.X, r.Min.Y, r.Dx(), r.Dy(), buf.Len() - n})
	}
	return out, buf.Bytes(), full, nil
}

// Message is one binary WebSocket message: a 4-byte big-endian header length, the header JSON, then the payload.
func Message(h Header, payload []byte) []byte {
	if h.Rects == nil {
		h.Rects = []Rect{}
	}
	hb, _ := json.Marshal(h)
	out := make([]byte, 4, 4+len(hb)+len(payload))
	binary.BigEndian.PutUint32(out, uint32(len(hb)))
	out = append(out, hb...)
	return append(out, payload...)
}
