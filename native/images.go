package native

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	_ "image/gif" // decoders for image.Decode
	"image/jpeg"
	"image/png"

	"github.com/gerdr-dev/gerdr/native/llm"
)

// imagePart turns image bytes into a model message part, applying Options.Images.
func (a *Agent) imagePart(mime string, data []byte) llm.Part {
	mime, data = limitImage(mime, data, a.opt.Images)
	return llm.Part{Type: "image", MIME: mime, Data: base64.StdEncoding.EncodeToString(data)}
}

// imagePartB64 is imagePart for base64 input (ACP image blocks). Without limits the data passes through untouched.
func (a *Agent) imagePartB64(mime, data string) llm.Part {
	if a.opt.Images == (ImageLimits{}) {
		return llm.Part{Type: "image", MIME: mime, Data: data}
	}
	b, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return llm.Part{Type: "image", MIME: mime, Data: data}
	}
	return a.imagePart(mime, b)
}

// limitImage scales an image whose long edge is over MaxEdge down to it, then re-encodes it as JPEG if it is
// still over MaxBytes. Formats the standard library can't decode (webp) pass through as they are.
func limitImage(mime string, data []byte, l ImageLimits) (string, []byte) {
	if l == (ImageLimits{}) {
		return mime, data
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return mime, data
	}
	long := max(cfg.Width, cfg.Height)
	tooWide := l.MaxEdge > 0 && long > l.MaxEdge
	tooBig := l.MaxBytes > 0 && len(data) > l.MaxBytes
	if !tooWide && !tooBig {
		return mime, data
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return mime, data
	}
	if tooWide {
		w, h := cfg.Width*l.MaxEdge/long, cfg.Height*l.MaxEdge/long
		img = downscale(img, max(w, 1), max(h, 1))
		var buf bytes.Buffer
		if mime == "image/png" && png.Encode(&buf, img) == nil {
			mime, data = "image/png", buf.Bytes()
		} else if jpeg.Encode(&buf, flatten(img), &jpeg.Options{Quality: 90}) == nil {
			mime, data = "image/jpeg", buf.Bytes()
		}
	}
	if l.MaxBytes > 0 && len(data) > l.MaxBytes {
		flat := flatten(img)
		for _, q := range []int{85, 75, 60} {
			var buf bytes.Buffer
			if jpeg.Encode(&buf, flat, &jpeg.Options{Quality: q}) != nil {
				break
			}
			mime, data = "image/jpeg", buf.Bytes()
			if len(data) <= l.MaxBytes {
				break
			}
		}
	}
	return mime, data
}

// downscale shrinks src to w×h by averaging each destination pixel's source area (box filter): sharp enough for
// screenshots, no extra dependency.
func downscale(src image.Image, w, h int) image.Image {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		y0, y1 := y*sh/h, max((y+1)*sh/h, y*sh/h+1)
		for x := 0; x < w; x++ {
			x0, x1 := x*sw/w, max((x+1)*sw/w, x*sw/w+1)
			var r, g, bl, a, n uint64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					c := color.NRGBA64Model.Convert(src.At(b.Min.X+sx, b.Min.Y+sy)).(color.NRGBA64)
					r, g, bl, a = r+uint64(c.R), g+uint64(c.G), bl+uint64(c.B), a+uint64(c.A)
					n++
				}
			}
			dst.SetNRGBA(x, y, color.NRGBA{uint8(r / n >> 8), uint8(g / n >> 8), uint8(bl / n >> 8), uint8(a / n >> 8)})
		}
	}
	return dst
}

// flatten puts transparent pixels on white: JPEG has no alpha, and transparent black would turn black.
func flatten(src image.Image) image.Image {
	b := src.Bounds()
	dst := image.NewRGBA(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := color.NRGBA64Model.Convert(src.At(x, y)).(color.NRGBA64)
			a := uint32(c.A)
			blend := func(v uint16) uint8 { return uint8((uint32(v)*a + 0xffff*(0xffff-a)) / 0xffff >> 8) }
			dst.SetRGBA(x, y, color.RGBA{blend(c.R), blend(c.G), blend(c.B), 255})
		}
	}
	return dst
}
