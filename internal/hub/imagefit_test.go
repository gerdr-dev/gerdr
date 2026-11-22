package hub

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/jpeg"
	"math/rand"
	"os"
	"testing"
	"time"

	"github.com/coder/acp-go-sdk"
)

// noisyJPEG is an image that compresses badly, so a few thousand pixels make a big file, like a camera photo.
func noisyJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	r := rand.New(rand.NewSource(1))
	for i := range img.Pix {
		img.Pix[i] = uint8(r.Intn(256))
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func imageBytes(t *testing.T, content []acp.ContentBlock) (total int) {
	t.Helper()
	for _, b := range content {
		if b.Image != nil {
			raw, err := base64.StdEncoding.DecodeString(b.Image.Data)
			if err != nil {
				t.Fatal(err)
			}
			total += len(raw)
		}
	}
	return total
}

// A message's images over the budget are shrunk until they fit; small ones and the text are left alone.
func TestFitPromptImages(t *testing.T) {
	big := noisyJPEG(t, 3000, 2000)
	small := noisyJPEG(t, 200, 100)
	if len(big) < 4<<20 {
		t.Fatalf("test image only %d bytes", len(big))
	}
	content := []acp.ContentBlock{
		acp.TextBlock("look"),
		acp.ImageBlock(base64.StdEncoding.EncodeToString(big), "image/jpeg"),
		acp.ImageBlock(base64.StdEncoding.EncodeToString(big), "image/jpeg"),
		acp.ImageBlock(base64.StdEncoding.EncodeToString(small), "image/jpeg"),
	}
	start := time.Now()
	out, saved := fitPromptImages(content)
	t.Logf("%d → %d bytes in %v", imageBytes(t, content), imageBytes(t, out), time.Since(start))
	if saved <= 0 || imageBytes(t, out) > promptImageBudget {
		t.Fatalf("images now %d bytes (saved %d), budget %d", imageBytes(t, out), saved, promptImageBudget)
	}
	if out[0].Text == nil || out[0].Text.Text != "look" || out[3].Image.Data != content[3].Image.Data {
		t.Fatal("the text or the small image changed")
	}
	if content[1].Image.Data == out[1].Image.Data {
		t.Fatal("the caller's content was changed in place, or the big image wasn't shrunk")
	}

	// Within the budget: nothing to do
	if _, saved := fitPromptImages(content[3:]); saved != 0 {
		t.Fatalf("a small image was touched (saved %d)", saved)
	}
}

// The real case, when the photo is on this machine (GERDR_FIT_PHOTO=<path>): how long a camera photo takes.
func TestFitPromptImagesPhoto(t *testing.T) {
	p := os.Getenv("GERDR_FIT_PHOTO")
	if p == "" {
		t.Skip("set GERDR_FIT_PHOTO to a large photo")
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	content := []acp.ContentBlock{acp.ImageBlock(base64.StdEncoding.EncodeToString(b), "image/jpeg")}
	start := time.Now()
	out, _ := fitPromptImages(content)
	t.Logf("%d → %d bytes in %v", len(b), imageBytes(t, out), time.Since(start))
}
