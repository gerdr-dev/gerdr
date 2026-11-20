package llm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Image generation: one request, no history, no streaming. Two wire formats behind the same call, picked by the
// provider's protocol (native.GenerateImage):
//   - gemini: generateContent with responseModalities IMAGE (Nano Banana: gemini-2.5-flash-image,
//     gemini-3-pro-image-preview). Reference images go in as inlineData parts next to the prompt, which is how
//     editing works there. No n parameter: n requests, in parallel.
//   - OpenAI-compatible: /images/generations, or /images/edits (multipart) when there are reference images
//     (gpt-image-1, dall-e-3). Plenty of gateways speak Chat Completions but not these: their 404 says so.
//
// Imagen (imagen-4…) is neither: it is Google's :predict API, not supported.

// ImageRequest is what both formats can do.
type ImageRequest struct {
	Prompt string
	// Images: reference images (to edit, or to take a subject / style from). Type "image", base64 Data.
	Images []Part
	// Aspect: "16:9", "1:1", "9:16"… Gemini takes it as is; OpenAI gets the closest size it has.
	Aspect string
	// Size: Gemini's imageSize (1K / 2K / 4K, Pro models only), or an OpenAI size as WxH ("1536x1024"). Empty = default.
	Size string
	N    int // how many images; 0 = 1
}

type Image struct {
	MIME string
	Data []byte
}

type ImageResult struct {
	Images []Image
	Text   string // what the model said alongside (Gemini sometimes explains or refuses in text)
}

// imageTimeout bounds one generation: Pro models take up to a minute or two per image.
const imageTimeout = 5 * time.Minute

// GeminiImage generates with a Gemini image model.
func GeminiImage(ctx context.Context, ep Endpoint, model string, req ImageRequest) (ImageResult, error) {
	n := max(req.N, 1)
	results := make([]ImageResult, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], errs[i] = geminiImageOnce(ctx, ep, GeminiModel(model), req)
		}()
	}
	wg.Wait()
	var out ImageResult
	var texts []string
	for i := range n {
		out.Images = append(out.Images, results[i].Images...)
		if t := strings.TrimSpace(results[i].Text); t != "" && !contains(texts, t) {
			texts = append(texts, t)
		}
	}
	out.Text = strings.Join(texts, "\n\n")
	if len(out.Images) == 0 {
		if err := errors.Join(errs...); err != nil {
			return out, err
		}
		return out, noImage(out.Text)
	}
	return out, nil // some came back: keep those, the failed ones show as fewer images
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func noImage(text string) error {
	if text != "" {
		return &APIError{Msg: "the model returned no image: " + text}
	}
	return &APIError{Msg: "the model returned no image"}
}

func geminiImageOnce(ctx context.Context, ep Endpoint, model string, req ImageRequest) (ImageResult, error) {
	parts := []any{map[string]any{"text": req.Prompt}}
	parts = append(parts, geminiParts(req.Images)...)
	gc := map[string]any{"responseModalities": []string{"TEXT", "IMAGE"}}
	ic := map[string]any{}
	if req.Aspect != "" {
		ic["aspectRatio"] = req.Aspect
	}
	if req.Size != "" {
		ic["imageSize"] = strings.ToUpper(req.Size)
	}
	if len(ic) > 0 {
		gc["imageConfig"] = ic
	}
	body := map[string]any{
		"contents":         []any{map[string]any{"role": "user", "parts": parts}},
		"generationConfig": gc,
	}
	ctx, cancel := context.WithTimeout(ctx, imageTimeout)
	defer cancel()
	rc, err := postURL(ctx, GeminiURL(ep.BaseURL, "/models/"+url.PathEscape(model)+":generateContent"), GeminiAuth(ep.APIKey), ep.Headers, body)
	if err != nil {
		return ImageResult{}, err
	}
	defer rc.Close()
	raw, err := io.ReadAll(io.LimitReader(rc, 256<<20))
	if err != nil {
		return ImageResult{}, fmt.Errorf("reading the response: %w", err)
	}
	return parseGeminiImage(raw)
}

type geminiInline struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

func parseGeminiImage(raw []byte) (ImageResult, error) {
	var res struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text       string        `json:"text"`
					Thought    bool          `json:"thought"`
					InlineData *geminiInline `json:"inlineData"`
					// some gateways pass the REST field names through in snake case
					InlineSnake *geminiInline `json:"inline_data"`
				} `json:"parts"`
			} `json:"content"`
			FinishReason  string `json:"finishReason"`
			FinishMessage string `json:"finishMessage"`
		} `json:"candidates"`
		PromptFeedback *struct {
			BlockReason string `json:"blockReason"`
		} `json:"promptFeedback"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return ImageResult{}, fmt.Errorf("unreadable response: %w: %s", err, oneLine(string(raw), 300))
	}
	if len(res.Candidates) == 0 {
		if res.PromptFeedback != nil && res.PromptFeedback.BlockReason != "" {
			return ImageResult{}, &APIError{Msg: "the prompt was blocked (" + res.PromptFeedback.BlockReason + ")"}
		}
		return ImageResult{}, noImage("")
	}
	var out ImageResult
	var text strings.Builder
	c := res.Candidates[0]
	for _, p := range c.Content.Parts {
		if p.Thought { // Pro models' drafts while thinking: not the answer
			continue
		}
		in := p.InlineData
		if in == nil {
			in = p.InlineSnake
		}
		if in != nil && in.Data != "" {
			b, err := base64.StdEncoding.DecodeString(in.Data)
			if err != nil {
				return out, fmt.Errorf("bad image data: %w", err)
			}
			out.Images = append(out.Images, Image{MIME: imageMIME(in.MimeType, b), Data: b})
		}
		text.WriteString(p.Text)
	}
	out.Text = strings.TrimSpace(text.String())
	if len(out.Images) == 0 {
		msg := out.Text
		if r := c.FinishReason; r != "" && r != "STOP" {
			msg = strings.TrimSpace("stopped: " + r + " " + c.FinishMessage + " " + msg)
		}
		return out, noImage(msg)
	}
	return out, nil
}

// OpenAIImage generates with OpenAI's Images API (or a gateway's copy of it).
func OpenAIImage(ctx context.Context, ep Endpoint, model string, req ImageRequest) (ImageResult, error) {
	ctx, cancel := context.WithTimeout(ctx, imageTimeout)
	defer cancel()
	size := openAISize(model, req.Aspect, req.Size)
	n := max(req.N, 1)
	auth := http.Header{}
	if ep.APIKey != "" {
		auth.Set("Authorization", "Bearer "+ep.APIKey)
	}
	base := strings.TrimRight(ep.BaseURL, "/")
	dalle := strings.HasPrefix(strings.ToLower(model), "dall-e")
	var rc io.ReadCloser
	var err error
	if len(req.Images) == 0 {
		body := map[string]any{"model": model, "prompt": req.Prompt, "n": n}
		if size != "" {
			body["size"] = size
		}
		if dalle { // gpt-image always answers in base64 and refuses response_format; DALL·E defaults to a URL
			body["response_format"] = "b64_json"
		}
		rc, err = postURL(ctx, base+"/images/generations", auth, ep.Headers, body)
	} else {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		_ = mw.WriteField("model", model)
		_ = mw.WriteField("prompt", req.Prompt)
		_ = mw.WriteField("n", strconv.Itoa(n))
		if size != "" {
			_ = mw.WriteField("size", size)
		}
		if dalle {
			_ = mw.WriteField("response_format", "b64_json")
		}
		field := "image[]" // gpt-image takes several; dall-e-2 one, as "image"
		if dalle {
			field = "image"
		}
		for i, p := range req.Images {
			b, derr := base64.StdEncoding.DecodeString(p.Data)
			if derr != nil {
				return ImageResult{}, fmt.Errorf("reference image %d: %w", i+1, derr)
			}
			mime := imageMIME(p.MIME, b)
			h := textproto.MIMEHeader{}
			h.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename="image%d.%s"`, field, i+1, extOf(mime)))
			h.Set("Content-Type", mime)
			w, _ := mw.CreatePart(h)
			_, _ = w.Write(b)
		}
		_ = mw.Close()
		rc, err = postBytes(ctx, base+"/images/edits", auth, ep.Headers, mw.FormDataContentType(), buf.Bytes())
	}
	if err != nil {
		var he *HTTPError
		if errors.As(err, &he) && he.Status == 404 {
			return ImageResult{}, fmt.Errorf("%w (this provider may not have OpenAI's Images API: /images/generations, /images/edits)", err)
		}
		return ImageResult{}, err
	}
	defer rc.Close()
	raw, err := io.ReadAll(io.LimitReader(rc, 256<<20))
	if err != nil {
		return ImageResult{}, fmt.Errorf("reading the response: %w", err)
	}
	return parseOpenAIImage(ctx, raw)
}

func parseOpenAIImage(ctx context.Context, raw []byte) (ImageResult, error) {
	var res struct {
		Data []struct {
			B64           string `json:"b64_json"`
			URL           string `json:"url"`
			RevisedPrompt string `json:"revised_prompt"`
		} `json:"data"`
		OutputFormat string `json:"output_format"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return ImageResult{}, fmt.Errorf("unreadable response: %w: %s", err, oneLine(string(raw), 300))
	}
	var out ImageResult
	for _, d := range res.Data {
		var b []byte
		switch {
		case d.B64 != "":
			var err error
			if b, err = base64.StdEncoding.DecodeString(d.B64); err != nil {
				return out, fmt.Errorf("bad image data: %w", err)
			}
		case d.URL != "": // a gateway that answers with links despite b64_json
			var err error
			if b, err = download(ctx, d.URL); err != nil {
				return out, err
			}
		default:
			continue
		}
		mime := ""
		if res.OutputFormat != "" {
			mime = "image/" + strings.ReplaceAll(res.OutputFormat, "jpg", "jpeg")
		}
		out.Images = append(out.Images, Image{MIME: imageMIME(mime, b), Data: b})
		if d.RevisedPrompt != "" && out.Text == "" {
			out.Text = "Revised prompt: " + d.RevisedPrompt
		}
	}
	if len(out.Images) == 0 {
		return out, noImage("")
	}
	return out, nil
}

func download(ctx context.Context, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	res, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("downloading the image: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("downloading the image: HTTP %d", res.StatusCode)
	}
	return io.ReadAll(io.LimitReader(res.Body, 64<<20))
}

var wxh = regexp.MustCompile(`^\d+x\d+$`)

// openAISize: an explicit WxH as is; else the size closest to the aspect ratio. gpt-image has 1024x1024, 1536x1024
// and 1024x1536; DALL·E 3 1024x1024, 1792x1024 and 1024x1792. Empty = let the API choose ("auto" on gpt-image).
func openAISize(model, aspect, size string) string {
	if wxh.MatchString(strings.ToLower(size)) {
		return strings.ToLower(size)
	}
	r, ok := ratio(aspect)
	if !ok {
		return ""
	}
	long := "1536"
	if strings.HasPrefix(strings.ToLower(model), "dall-e-3") {
		long = "1792"
	}
	switch {
	case r > 1.1:
		return long + "x1024"
	case r < 0.9:
		return "1024x" + long
	}
	return "1024x1024"
}

func ratio(aspect string) (float64, bool) {
	w, h, ok := strings.Cut(aspect, ":")
	if !ok {
		return 0, false
	}
	a, err1 := strconv.ParseFloat(strings.TrimSpace(w), 64)
	b, err2 := strconv.ParseFloat(strings.TrimSpace(h), 64)
	if err1 != nil || err2 != nil || a <= 0 || b <= 0 {
		return 0, false
	}
	return a / b, true
}

// imageMIME trusts the bytes over the label: gateways have labelled JPEGs image/png.
func imageMIME(label string, b []byte) string {
	if m := http.DetectContentType(b); strings.HasPrefix(m, "image/") {
		return m
	}
	if strings.HasPrefix(label, "image/") {
		return label
	}
	return "image/png"
}

func extOf(mime string) string {
	switch mime {
	case "image/jpeg":
		return "jpg"
	case "image/webp":
		return "webp"
	case "image/gif":
		return "gif"
	}
	return "png"
}

// ImageExt is the file extension (without the dot) for an image's MIME type.
func ImageExt(mime string) string { return extOf(mime) }
