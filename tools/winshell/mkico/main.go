// mkico writes a Windows .ico from PNGs: the icon of the shortcuts the installer makes, and of the installer itself
// (package.sh). The window's own icon comes from the PNG in the exe (main.go).
//
//	go run ./mkico out.ico icon.png 16 32 48 256         # PNG-compressed entries (Windows Vista and later)
//	go run ./mkico -bmp out.ico icon.png 16 32 48        # classic 32-bit bitmap entries: NSIS's installer icon
//	                                                      # needs these (it crashed on PNG ones: bad_alloc)
//
// One source image, scaled here (area average): the release builds the Windows package on Linux, without macOS's sips.
package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"log"
	"os"
	"strconv"
)

func main() {
	args := os.Args[1:]
	bmp := len(args) > 0 && args[0] == "-bmp"
	if bmp {
		args = args[1:]
	}
	if len(args) < 3 {
		log.Fatal("usage: mkico [-bmp] out.ico source.png size…")
	}
	raw, err := os.ReadFile(args[1])
	if err != nil {
		log.Fatal(err)
	}
	src, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		log.Fatal(err)
	}
	var imgs [][]byte
	var sizes [][2]int
	for _, a := range args[2:] {
		n, err := strconv.Atoi(a)
		if err != nil || n < 1 || n > 256 {
			log.Fatalf("size %q: 1..256", a)
		}
		img := scale(src, n)
		var b []byte
		if bmp {
			b = dib(img)
		} else {
			var buf bytes.Buffer
			if err := png.Encode(&buf, img); err != nil {
				log.Fatal(err)
			}
			b = buf.Bytes()
		}
		imgs = append(imgs, b)
		sizes = append(sizes, [2]int{n, n})
	}
	var out bytes.Buffer
	w := func(v any) { _ = binary.Write(&out, binary.LittleEndian, v) }
	w(uint16(0))         // reserved
	w(uint16(1))         // type: icon
	w(uint16(len(imgs))) // count
	offset := 6 + 16*len(imgs)
	for i, b := range imgs {
		cfg := struct{ Width, Height int }{sizes[i][0], sizes[i][1]}
		dim := func(n int) uint8 { // 256 is written as 0
			if n >= 256 {
				return 0
			}
			return uint8(n)
		}
		w(dim(cfg.Width))
		w(dim(cfg.Height))
		w(uint8(0))  // palette
		w(uint8(0))  // reserved
		w(uint16(1)) // planes
		w(uint16(32))
		w(uint32(len(b)))
		w(uint32(offset))
		offset += len(b)
	}
	for _, b := range imgs {
		out.Write(b)
	}
	if err := os.WriteFile(args[0], out.Bytes(), 0o644); err != nil {
		log.Fatal(err)
	}
}

// dib: an icon entry as a classic bitmap: BITMAPINFOHEADER (height doubled: colour then mask), 32-bit BGRA rows
// bottom-up, then the 1-bit AND mask (all zero: the alpha channel decides).
func dib(img image.Image) []byte {
	r := img.Bounds()
	w, h := r.Dx(), r.Dy()
	var b bytes.Buffer
	put := func(v any) { _ = binary.Write(&b, binary.LittleEndian, v) }
	put(uint32(40))
	put(int32(w))
	put(int32(h * 2))
	put(uint16(1))
	put(uint16(32))
	put(uint32(0)) // BI_RGB
	put(uint32(w * h * 4))
	put(int32(0))
	put(int32(0))
	put(uint32(0))
	put(uint32(0))
	for y := h - 1; y >= 0; y-- {
		for x := 0; x < w; x++ {
			c := color.NRGBAModel.Convert(img.At(r.Min.X+x, r.Min.Y+y)).(color.NRGBA)
			b.Write([]byte{c.B, c.G, c.R, c.A})
		}
	}
	stride := ((w + 31) / 32) * 4
	b.Write(make([]byte, stride*h))
	return b.Bytes()
}

// scale: src down to n×n, each target pixel the average of the source pixels it covers (alpha-weighted colour, so the
// transparent corners don't darken the edges).
func scale(src image.Image, n int) *image.NRGBA {
	r := src.Bounds()
	out := image.NewNRGBA(image.Rect(0, 0, n, n))
	for y := 0; y < n; y++ {
		y0, y1 := r.Min.Y+y*r.Dy()/n, r.Min.Y+(y+1)*r.Dy()/n
		for x := 0; x < n; x++ {
			x0, x1 := r.Min.X+x*r.Dx()/n, r.Min.X+(x+1)*r.Dx()/n
			var rs, gs, bs, as, cnt float64
			for sy := y0; sy < max(y1, y0+1); sy++ {
				for sx := x0; sx < max(x1, x0+1); sx++ {
					c := color.NRGBAModel.Convert(src.At(sx, sy)).(color.NRGBA)
					a := float64(c.A)
					rs += float64(c.R) * a
					gs += float64(c.G) * a
					bs += float64(c.B) * a
					as += a
					cnt++
				}
			}
			if as > 0 {
				out.SetNRGBA(x, y, color.NRGBA{uint8(rs / as), uint8(gs / as), uint8(bs / as), uint8(as / cnt)})
			}
		}
	}
	return out
}
