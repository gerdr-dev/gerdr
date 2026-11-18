package chromeext

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// A 2x capture of a 100x50 viewport comes back at 100x50, and a crop keeps the right part of the picture.
func TestToCSSPixels(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 200, 100))
	for y := 0; y < 100; y++ {
		for x := 0; x < 200; x++ {
			c := color.RGBA{255, 0, 0, 255}
			if x >= 100 {
				c = color.RGBA{0, 0, 255, 255}
			}
			src.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, src)
	b64 := base64.StdEncoding.EncodeToString(buf.Bytes())

	decode := func(s string) image.Image {
		raw, _ := base64.StdEncoding.DecodeString(s)
		img, err := png.Decode(bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		return img
	}

	out, err := toCSSPixels(b64, image.Rect(0, 0, 100, 50), 2, "png")
	if err != nil {
		t.Fatal(err)
	}
	if b := decode(out).Bounds(); b.Dx() != 100 || b.Dy() != 50 {
		t.Fatalf("viewport: got %v, want 100x50", b)
	}

	out, err = toCSSPixels(b64, image.Rect(60, 10, 90, 30), 2, "png")
	if err != nil {
		t.Fatal(err)
	}
	img := decode(out)
	if b := img.Bounds(); b.Dx() != 30 || b.Dy() != 20 {
		t.Fatalf("crop: got %v, want 30x20", b)
	}
	if r, _, bl, _ := img.At(15, 10).RGBA(); bl < r {
		t.Fatalf("crop of the right half should be blue")
	}

	// at 1x with no crop the capture is passed through untouched
	if out, _ := toCSSPixels(b64, image.Rect(0, 0, 200, 100), 1, "png"); out != b64 {
		t.Fatalf("1x viewport should not be re-encoded")
	}
}
