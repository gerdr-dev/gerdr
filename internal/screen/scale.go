package screen

import (
	"image"
	"runtime"
	"sync"
)

// raw is a 32-bit-per-pixel screen buffer as CoreGraphics hands it over: rows of stride bytes, the colour channels
// at byte offsets r, g, b within each pixel (BGRA on Apple silicon and Intel alike, but read from the bitmap info).
type raw struct {
	pix          []byte
	w, h, stride int
	r, g, b      int
}

// shrink box-filters src down to width dw (keeping the aspect ratio), or copies it when it is already that narrow.
// Rows are split across the CPUs: a 3840×2160 screen to 1600 px wide takes a few ms this way.
func shrink(src raw, dw int) *image.RGBA {
	if dw <= 0 || dw > src.w {
		dw = src.w
	}
	dh := max(1, src.h*dw/src.w)
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	// Each destination column / row covers [x0[i], x0[i+1]) of the source
	xs := make([]int, dw+1)
	for i := range xs {
		xs[i] = i * src.w / dw
	}
	ys := make([]int, dh+1)
	for i := range ys {
		ys[i] = i * src.h / dh
	}
	workers := max(1, min(runtime.NumCPU(), dh/32))
	var wg sync.WaitGroup
	for k := 0; k < workers; k++ {
		wg.Add(1)
		go func(from, to int) {
			defer wg.Done()
			for y := from; y < to; y++ {
				sy0, sy1 := ys[y], max(ys[y+1], ys[y]+1)
				row := dst.Pix[y*dst.Stride:]
				for x := 0; x < dw; x++ {
					sx0, sx1 := xs[x], max(xs[x+1], xs[x]+1)
					var r, g, b, n int
					for sy := sy0; sy < sy1; sy++ {
						p := src.pix[sy*src.stride+sx0*4:]
						for i := 0; i < (sx1-sx0)*4; i += 4 {
							r += int(p[i+src.r])
							g += int(p[i+src.g])
							b += int(p[i+src.b])
							n++
						}
					}
					o := x * 4
					row[o] = uint8(r / n)
					row[o+1] = uint8(g / n)
					row[o+2] = uint8(b / n)
					row[o+3] = 255
				}
			}
		}(dh*k/workers, dh*(k+1)/workers)
	}
	wg.Wait()
	return dst
}

// fromImage turns any decoded image (the screencapture fallback) into a raw buffer for shrink.
func fromImage(m image.Image) raw {
	b := m.Bounds()
	if rgba, ok := m.(*image.RGBA); ok && b.Min == (image.Point{}) {
		return raw{pix: rgba.Pix, w: b.Dx(), h: b.Dy(), stride: rgba.Stride, r: 0, g: 1, b: 2}
	}
	out := raw{pix: make([]byte, b.Dx()*b.Dy()*4), w: b.Dx(), h: b.Dy(), stride: b.Dx() * 4, r: 0, g: 1, b: 2}
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			r, g, bl, _ := m.At(b.Min.X+x, b.Min.Y+y).RGBA()
			o := y*out.stride + x*4
			out.pix[o], out.pix[o+1], out.pix[o+2] = uint8(r>>8), uint8(g>>8), uint8(bl>>8)
		}
	}
	return out
}
