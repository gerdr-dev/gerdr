package native

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"testing"
)

// withOrientation puts an EXIF APP1 with Orientation o right after a JPEG's SOI.
func withOrientation(t *testing.T, jpg []byte, o int, bo binary.ByteOrder) []byte {
	t.Helper()
	var tiff bytes.Buffer
	if bo == binary.LittleEndian {
		tiff.WriteString("II")
	} else {
		tiff.WriteString("MM")
	}
	_ = binary.Write(&tiff, bo, uint16(42))
	_ = binary.Write(&tiff, bo, uint32(8)) // IFD0 right after the header
	_ = binary.Write(&tiff, bo, uint16(1)) // one entry
	_ = binary.Write(&tiff, bo, uint16(0x112))
	_ = binary.Write(&tiff, bo, uint16(3)) // SHORT
	_ = binary.Write(&tiff, bo, uint32(1))
	_ = binary.Write(&tiff, bo, uint16(o))
	_ = binary.Write(&tiff, bo, uint16(0))
	_ = binary.Write(&tiff, bo, uint32(0)) // no next IFD
	seg := append([]byte("Exif\x00\x00"), tiff.Bytes()...)
	app1 := []byte{0xFF, 0xE1, 0, 0}
	binary.BigEndian.PutUint16(app1[2:], uint16(len(seg)+2))
	out := append([]byte{}, jpg[:2]...)
	out = append(out, app1...)
	out = append(out, seg...)
	return append(out, jpg[2:]...)
}

func TestJPEGOrientation(t *testing.T) {
	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 4, 2)), nil)
	plain := buf.Bytes()
	if o := jpegOrientation(plain); o != 1 {
		t.Fatalf("no EXIF: %d", o)
	}
	for _, bo := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
		for o := 1; o <= 8; o++ {
			if got := jpegOrientation(withOrientation(t, plain, o, bo)); got != o {
				t.Fatalf("%v orientation %d read as %d", bo, o, got)
			}
		}
	}
	if o := jpegOrientation([]byte("not a jpeg")); o != 1 {
		t.Fatalf("garbage: %d", o)
	}
}

// Each orientation puts the stored top-left pixel where the viewer sees it.
func TestOrient(t *testing.T) {
	// 3×2, the top-left pixel red, the rest black
	src := image.NewNRGBA(image.Rect(0, 0, 3, 2))
	src.SetNRGBA(0, 0, color.NRGBA{255, 0, 0, 255})
	type pt struct{ x, y, w, h int }
	want := map[int]pt{ // where red ends up, and the size
		1: {0, 0, 3, 2}, 2: {2, 0, 3, 2}, 3: {2, 1, 3, 2}, 4: {0, 1, 3, 2},
		5: {0, 0, 2, 3}, 6: {1, 0, 2, 3}, 7: {1, 2, 2, 3}, 8: {0, 2, 2, 3},
	}
	for o, w := range want {
		img := orient(src, o)
		b := img.Bounds()
		if b.Dx() != w.w || b.Dy() != w.h {
			t.Fatalf("orientation %d: size %dx%d, want %dx%d", o, b.Dx(), b.Dy(), w.w, w.h)
		}
		if r, _, _, _ := img.At(w.x, w.y).RGBA(); r>>8 != 255 {
			t.Fatalf("orientation %d: red not at (%d,%d)", o, w.x, w.y)
		}
	}
}

// A portrait photo stored landscape (Orientation 6) comes out of FitImage portrait.
func TestFitImageKeepsOrientation(t *testing.T) {
	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 400, 200)), nil)
	in := withOrientation(t, buf.Bytes(), 6, binary.BigEndian)
	_, out := FitImage("image/jpeg", in, 100, 0)
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Width != 50 || cfg.Height != 100 {
		t.Fatalf("got %dx%d, want 50x100 (upright)", cfg.Width, cfg.Height)
	}

	if p := os.Getenv("GERDR_FIT_PHOTO"); p != "" {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		_, out := FitImage("image/jpeg", b, 2560, 6<<20)
		cfg, _ := jpeg.DecodeConfig(bytes.NewReader(out))
		t.Logf("photo (orientation %d): %d bytes, %dx%d", jpegOrientation(b), len(out), cfg.Width, cfg.Height)
	}
}
