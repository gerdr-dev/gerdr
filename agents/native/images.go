package native

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	_ "image/gif" // decoders for image.Decode
	"image/jpeg"
	"image/png"
	"math"

	"github.com/gerdr-dev/gerdr/agents/native/llm"
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
	if err != nil || len(b) == 0 {
		return llm.Part{Type: "image", MIME: mime, Data: data}
	}
	m, out := limitImage(mime, b, a.opt.Images)
	if len(out) == len(b) && &out[0] == &b[0] { // untouched: skip re-encoding
		return llm.Part{Type: "image", MIME: mime, Data: data}
	}
	return llm.Part{Type: "image", MIME: m, Data: base64.StdEncoding.EncodeToString(out)}
}

// limitHistory applies Options.Images to the images in a loaded transcript. Sessions from before the limits (or
// with images that came in unlimited) would otherwise send the same oversized image on every turn and fail every
// time. Only the in-memory copy changes; assistant messages are left alone (Anthropic wants those verbatim), and
// they hold no images anyway.
func (a *Agent) limitHistory(es []entry) {
	if a.opt.Images == (ImageLimits{}) {
		return
	}
	for _, e := range es {
		if e.Msg == nil || e.Msg.Role == "assistant" {
			continue
		}
		for i, p := range e.Msg.Content {
			if p.Type == "image" {
				e.Msg.Content[i] = a.imagePartB64(p.MIME, p.Data)
			}
		}
	}
}

// limitImage scales an image down to fitImage's size, then re-encodes it as JPEG if it is still over MaxBytes. Formats the standard library can't decode (webp) pass through as they are.
func limitImage(mime string, data []byte, l ImageLimits) (string, []byte) {
	if l == (ImageLimits{}) {
		return mime, data
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return mime, data
	}
	w, h := fitImage(cfg.Width, cfg.Height, l)
	resize := w != cfg.Width || h != cfg.Height
	tooBig := l.MaxBytes > 0 && len(data) > l.MaxBytes
	if !resize && !tooBig {
		return mime, data
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return mime, data
	}
	if resize {
		img = downscale(img, w, h)
		var buf bytes.Buffer
		if mime == "image/png" && png.Encode(&buf, img) == nil {
			mime, data = "image/png", buf.Bytes()
		} else if jpeg.Encode(&buf, flatten(img), &jpeg.Options{Quality: 90}) == nil {
			mime, data = "image/jpeg", buf.Bytes()
		}
	}
	if l.MaxBytes > 0 && len(data) > l.MaxBytes {
		flat := flatten(img)
		for _, q := range []int{80, 60, 40, 20} { // Claude Code's steps
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

// fitImage is the size an image is scaled down to, the way Claude Code does it: each edge within MaxEdge (keeping
// the aspect ratio), then the largest size whose tile count stays within MaxTokens.
func fitImage(w, h int, l ImageLimits) (int, int) {
	scale := func(v, num, den int) int { return max(int(math.Round(float64(v)*float64(num)/float64(den))), 1) }
	if l.MaxEdge > 0 && w > l.MaxEdge {
		h, w = scale(h, l.MaxEdge, w), l.MaxEdge
	}
	if l.MaxEdge > 0 && h > l.MaxEdge {
		w, h = scale(w, l.MaxEdge, h), l.MaxEdge
	}
	if l.MaxTokens <= 0 || imageTokens(w, h) <= l.MaxTokens {
		return w, h
	}
	if h > w {
		nh, nw := fitImage(h, w, ImageLimits{MaxTokens: l.MaxTokens})
		return nw, nh
	}
	ratio := float64(w) / float64(h)
	lo, hi := 1, w // binary search the widest width that fits
	for lo+1 < hi {
		mid := (lo + hi) / 2
		if imageTokens(mid, max(int(math.Round(float64(mid)/ratio)), 1)) <= l.MaxTokens {
			lo = mid
		} else {
			hi = mid
		}
	}
	return lo, max(int(math.Round(float64(lo)/ratio)), 1)
}

// imageTokens: Anthropic bills an image one token per 28×28 tile.
func imageTokens(w, h int) int { return ((w + 27) / 28) * ((h + 27) / 28) }

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
