package server

// The icon values a project (layout.json spaces[].icon / color) and a panel (its user/ui.json) take: the same as the
// UI's IconPicker (ui/src/components/IconPicker.tsx, ui/src/lib/iconImage.ts) — keep the two in step. An uploaded
// picture is kept inline as a small data URL rather than a file, so it goes wherever the value goes (another
// computer, the phone) without an address to serve it from; hence the size limit.

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"

	"github.com/gerdr-dev/gerdr/internal/i18n"
)

const (
	// TextIcon starts a text icon's value: "text:" and up to three characters.
	TextIcon = "text:"
	// iconPx is an uploaded picture's edge (iconImage.ts ICON_PX).
	iconPx = 112
	// maxIconBytes: a picture the UI shrank is a few KB; this leaves room for a PNG with detail and refuses a photo
	// pasted as is (it would be sent with every layout change).
	maxIconBytes = 96 << 10
)

var (
	imageIconRe = regexp.MustCompile(`^data:image/(png|jpeg|webp|gif);base64,`)
	hexColorRe  = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	iconKeyRe   = regexp.MustCompile(`^(sprite:)?[\w.:-]{1,64}$`)
	colorKeyRe  = regexp.MustCompile(`^[a-z]{1,20}$`)
)

// IconError says why icon / color can't be stored ("" icon and color are fine: the default).
func IconError(icon, color string) error {
	switch {
	case icon == "":
	case strings.HasPrefix(icon, TextIcon):
		if n := utf8.RuneCountInString(strings.TrimPrefix(icon, TextIcon)); n < 1 || n > 3 {
			return errors.New(i18n.S("文字图标是 1 到 3 个字", "a text icon is 1 to 3 characters"))
		}
	case strings.HasPrefix(icon, "data:"):
		if !imageIconRe.MatchString(icon) {
			return errors.New(i18n.S("图片图标只收 PNG、JPEG、WebP、GIF", "a picture icon must be PNG, JPEG, WebP or GIF"))
		}
		if len(icon) > maxIconBytes {
			return errors.New(i18n.S("图片太大了：先缩到 112×112 左右（gerdr project icon --image 会自动缩）", "the picture is too big: shrink it to about 112×112 first (--image does that for you)"))
		}
	case !iconKeyRe.MatchString(icon):
		return fmt.Errorf(i18n.S("不认识的图标 %q", "unknown icon %q"), icon)
	}
	if color != "" && !hexColorRe.MatchString(color) && !colorKeyRe.MatchString(color) {
		return fmt.Errorf(i18n.S("颜色写调色板里的名字（red、teal…）或 #rrggbb，不是 %q", "a colour is a palette name (red, teal…) or #rrggbb, not %q"), color)
	}
	return nil
}

// IconFromImage reads a picture file (PNG, JPEG, GIF, WebP) and returns it as an icon value: its middle square,
// shrunk to iconPx, as a PNG data URL — what the UI's upload does.
func IconFromImage(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return "", fmt.Errorf(i18n.S("读不了这张图片（支持 PNG、JPEG、GIF、WebP）：%v", "can't read this picture (PNG, JPEG, GIF and WebP work): %v"), err)
	}
	b := src.Bounds()
	side := min(b.Dx(), b.Dy())
	crop := image.Rect(b.Min.X+(b.Dx()-side)/2, b.Min.Y+(b.Dy()-side)/2, 0, 0)
	crop.Max = crop.Min.Add(image.Pt(side, side))
	dst := image.NewRGBA(image.Rect(0, 0, iconPx, iconPx))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, crop, draw.Src, nil)
	var out bytes.Buffer
	if err := png.Encode(&out, dst); err != nil {
		return "", err
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(out.Bytes()), nil
}
