package hub

import (
	"encoding/base64"

	"github.com/coder/acp-go-sdk"

	"github.com/gerdr-dev/gerdr/agents/native"
)

// A message reaches an agent as one JSON line, its images inline in base64, and acp-go-sdk refuses a line over 10MB
// (agenthost lineWatch): on 2026-11-22 a phone sent a 6000×4000 camera photo (13MB, 19MB as base64 with a
// screenshot) and the turn could not even start. So a message's images get a budget before any agent sees them:
// within it nothing changes; over it the larger ones are scaled down (long edge promptImageEdge) and re-encoded
// until each fits its share. A model gains nothing from a 24-megapixel photo anyway: Claude scales images down to
// ~1.15 megapixels, and Anthropic's API refuses one over 5MB.
const (
	// promptImageBudget is what a message's images may take before base64 (×4/3: 8MB on the line), leaving the
	// SDK's 10MB room for the text and the JSON around them.
	promptImageBudget = 6 << 20
	// promptImageMin: a share never goes below this, however many images there are.
	promptImageMin = 512 << 10
	// promptImageEdge: the long edge a shrunk image gets, sharp enough for screenshots of a big screen.
	promptImageEdge = 2560
)

// fitPromptImages returns content with its images within the budget, and how many bytes it saved. Images within
// their share, and formats it can't decode, are left as they are.
func fitPromptImages(content []acp.ContentBlock) ([]acp.ContentBlock, int) {
	type img struct {
		i    int
		data []byte
	}
	var imgs []img
	total := 0
	for i, b := range content {
		if b.Image == nil || b.Image.Data == "" {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(b.Image.Data)
		if err != nil {
			continue
		}
		imgs = append(imgs, img{i, raw})
		total += len(raw)
	}
	if total <= promptImageBudget {
		return content, 0
	}
	share := max(promptImageBudget/len(imgs), promptImageMin)
	out := append([]acp.ContentBlock(nil), content...)
	saved := 0
	for _, m := range imgs {
		if len(m.data) <= share {
			continue
		}
		mime, small := native.FitImage(out[m.i].Image.MimeType, m.data, promptImageEdge, share)
		if len(small) >= len(m.data) {
			continue
		}
		im := *out[m.i].Image
		im.MimeType, im.Data = mime, base64.StdEncoding.EncodeToString(small)
		out[m.i] = acp.ContentBlock{Image: &im}
		saved += len(m.data) - len(small)
	}
	return out, saved
}
