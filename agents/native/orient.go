package native

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/draw"
)

// Camera photos keep their pixels as the sensor saw them and say how to show them in EXIF (Orientation, tag 0x112):
// a phone held upright writes a 6000×4000 landscape and "rotate 90°". Browsers and phones apply it; Go's image
// decoders don't, and re-encoding drops the EXIF. So an image scaled down here came out lying on its side (2026-11-22,
// a portrait photo sent from a phone showed landscape in the chat, and the model saw it sideways). Whoever re-encodes
// turns the pixels upright first (orient).

// jpegOrientation reads EXIF Orientation (1–8) from a JPEG; 1 (as stored) when there is none or it can't be read.
func jpegOrientation(b []byte) int {
	if len(b) < 4 || b[0] != 0xFF || b[1] != 0xD8 {
		return 1
	}
	for i := 2; i+4 <= len(b); {
		if b[i] != 0xFF {
			return 1
		}
		marker := b[i+1]
		if marker == 0xD9 || marker == 0xDA { // end of image, start of scan: no EXIF before the pixels
			return 1
		}
		n := int(binary.BigEndian.Uint16(b[i+2:]))
		if n < 2 || i+2+n > len(b) {
			return 1
		}
		seg := b[i+4 : i+2+n]
		if marker == 0xE1 && bytes.HasPrefix(seg, []byte("Exif\x00\x00")) {
			return exifOrientation(seg[6:])
		}
		i += 2 + n
	}
	return 1
}

// exifOrientation reads tag 0x112 from IFD0 of a TIFF block.
func exifOrientation(t []byte) int {
	if len(t) < 8 {
		return 1
	}
	var bo binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 1
	}
	ifd := int(bo.Uint32(t[4:]))
	if ifd+2 > len(t) {
		return 1
	}
	n := int(bo.Uint16(t[ifd:]))
	for k := 0; k < n; k++ {
		e := ifd + 2 + 12*k
		if e+12 > len(t) {
			return 1
		}
		if bo.Uint16(t[e:]) == 0x112 {
			if v := int(bo.Uint16(t[e+8:])); v >= 1 && v <= 8 {
				return v
			}
			return 1
		}
	}
	return 1
}

// orient turns src upright for EXIF orientation o (2–8 mirror and / or rotate; anything else returns src).
func orient(src image.Image, o int) image.Image {
	if o < 2 || o > 8 {
		return src
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	in := image.NewNRGBA(image.Rect(0, 0, w, h))
	draw.Draw(in, in.Bounds(), src, b.Min, draw.Src)
	dw, dh := w, h
	if o >= 5 {
		dw, dh = h, w
	}
	out := image.NewNRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var dx, dy int
			switch o {
			case 2: // mirrored
				dx, dy = w-1-x, y
			case 3: // rotated 180°
				dx, dy = w-1-x, h-1-y
			case 4: // mirrored vertically
				dx, dy = x, h-1-y
			case 5: // mirrored, then rotated 90° counter-clockwise (transpose)
				dx, dy = y, x
			case 6: // rotate 90° clockwise to show
				dx, dy = h-1-y, x
			case 7: // transverse
				dx, dy = h-1-y, w-1-x
			case 8: // rotate 90° counter-clockwise to show
				dx, dy = y, w-1-x
			}
			i, j := in.PixOffset(x, y), out.PixOffset(dx, dy)
			copy(out.Pix[j:j+4], in.Pix[i:i+4])
		}
	}
	return out
}
