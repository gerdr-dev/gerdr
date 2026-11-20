package llm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{255, 0, 0, 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestGeminiImage(t *testing.T) {
	pngData := tinyPNG(t)
	b64 := base64.StdEncoding.EncodeToString(pngData)
	var calls atomic.Int32
	var got map[string]any
	var path, key string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		path = r.URL.Path
		key = r.Header.Get("X-Goog-Api-Key")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &got)
		// a draft while thinking (skipped), then the answer: text and the image
		fmt.Fprintf(w, `{"candidates":[{"content":{"parts":[{"thought":true,"inlineData":{"mimeType":"image/png","data":"AAAA"}},{"text":"Here it is."},{"inlineData":{"mimeType":"image/jpeg","data":%q}}]},"finishReason":"STOP"}]}`, b64)
	}))
	defer srv.Close()
	res, err := GeminiImage(context.Background(), Endpoint{BaseURL: srv.URL, APIKey: "k"}, "models/gemini-2.5-flash-image",
		ImageRequest{Prompt: "a cat", Aspect: "16:9", N: 2, Images: []Part{{Type: "image", MIME: "image/png", Data: b64}}})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 || len(res.Images) != 2 {
		t.Fatalf("calls %d images %d, want 2 / 2", calls.Load(), len(res.Images))
	}
	if res.Images[0].MIME != "image/png" || !bytes.Equal(res.Images[0].Data, pngData) {
		t.Errorf("image %q %d bytes: MIME should come from the bytes", res.Images[0].MIME, len(res.Images[0].Data))
	}
	if res.Text != "Here it is." {
		t.Errorf("text %q (the same text from each request once)", res.Text)
	}
	if !strings.HasSuffix(path, "/models/gemini-2.5-flash-image:generateContent") || key != "k" {
		t.Errorf("path %q key %q", path, key)
	}
	gc := got["generationConfig"].(map[string]any)
	if fmt.Sprint(gc["responseModalities"]) != "[IMAGE]" || fmt.Sprint(gc["imageConfig"]) != "map[aspectRatio:16:9]" {
		t.Errorf("generationConfig %v", gc)
	}
	parts := got["contents"].([]any)[0].(map[string]any)["parts"].([]any)
	if len(parts) != 2 || parts[1].(map[string]any)["inlineData"] == nil {
		t.Errorf("parts %v: want the prompt then the reference image", parts)
	}
}

func TestGeminiImageNone(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{`{"candidates":[{"content":{"parts":[{"text":"I can't draw that."}]},"finishReason":"STOP"}]}`, "I can't draw that."},
		{`{"candidates":[{"content":{},"finishReason":"IMAGE_SAFETY"}]}`, "IMAGE_SAFETY"},
		{`{"promptFeedback":{"blockReason":"PROHIBITED_CONTENT"}}`, "PROHIBITED_CONTENT"},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, tc.body) }))
		_, err := GeminiImage(context.Background(), Endpoint{BaseURL: srv.URL}, "gemini-2.5-flash-image", ImageRequest{Prompt: "x"})
		srv.Close()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err %v, want it to mention %q", tc.body, err, tc.want)
		}
	}
}

func TestOpenAIImageGenerate(t *testing.T) {
	b64 := base64.StdEncoding.EncodeToString(tinyPNG(t))
	var got map[string]any
	var path, auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, auth = r.URL.Path, r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &got)
		fmt.Fprintf(w, `{"data":[{"b64_json":%q},{"b64_json":%q}]}`, b64, b64)
	}))
	defer srv.Close()
	res, err := OpenAIImage(context.Background(), Endpoint{BaseURL: srv.URL + "/v1/", APIKey: "sk"}, "gpt-image-1", ImageRequest{Prompt: "a cat", Aspect: "9:16", N: 2})
	if err != nil {
		t.Fatal(err)
	}
	if path != "/v1/images/generations" || auth != "Bearer sk" || len(res.Images) != 2 {
		t.Fatalf("path %q auth %q images %d", path, auth, len(res.Images))
	}
	if got["size"] != "1024x1536" || got["n"] != float64(2) || got["response_format"] != nil {
		t.Errorf("body %v", got)
	}
}

func TestOpenAIImageEdit(t *testing.T) {
	pngData := tinyPNG(t)
	b64 := base64.StdEncoding.EncodeToString(pngData)
	var fields map[string][]string
	var files int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/images/edits" {
			http.NotFound(w, r)
			return
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Error(err)
		}
		fields = r.MultipartForm.Value
		files = len(r.MultipartForm.File["image[]"])
		fmt.Fprintf(w, `{"data":[{"b64_json":%q}]}`, b64)
	}))
	defer srv.Close()
	ref := Part{Type: "image", MIME: "image/png", Data: b64}
	res, err := OpenAIImage(context.Background(), Endpoint{BaseURL: srv.URL}, "gpt-image-1", ImageRequest{Prompt: "make it blue", Images: []Part{ref, ref}, Size: "1536x1024"})
	if err != nil {
		t.Fatal(err)
	}
	if files != 2 || fields["prompt"][0] != "make it blue" || fields["size"][0] != "1536x1024" || len(res.Images) != 1 {
		t.Errorf("files %d fields %v images %d", files, fields, len(res.Images))
	}
}

func TestOpenAIImageNoAPI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) }))
	defer srv.Close()
	_, err := OpenAIImage(context.Background(), Endpoint{BaseURL: srv.URL}, "gpt-image-1", ImageRequest{Prompt: "x"})
	if err == nil || !strings.Contains(err.Error(), "Images API") {
		t.Errorf("err %v: a 404 should say the provider may not have the Images API", err)
	}
}

func TestOpenAISize(t *testing.T) {
	for _, tc := range []struct{ model, aspect, size, want string }{
		{"gpt-image-1", "", "", ""},
		{"gpt-image-1", "16:9", "", "1536x1024"},
		{"gpt-image-1", "3:4", "", "1024x1536"},
		{"gpt-image-1", "1:1", "", "1024x1024"},
		{"dall-e-3", "16:9", "", "1792x1024"},
		{"gpt-image-1", "16:9", "1024X1024", "1024x1024"},
		{"gpt-image-1", "wide", "2K", ""},
	} {
		if got := openAISize(tc.model, tc.aspect, tc.size); got != tc.want {
			t.Errorf("openAISize(%q, %q, %q) = %q, want %q", tc.model, tc.aspect, tc.size, got, tc.want)
		}
	}
}

// A gateway that refuses IMAGE alone gets asked again with TEXT allowed.
func TestGeminiImageModalityFallback(t *testing.T) {
	b64 := base64.StdEncoding.EncodeToString(tinyPNG(t))
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var got map[string]any
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &got)
		m := fmt.Sprint(got["generationConfig"].(map[string]any)["responseModalities"])
		seen = append(seen, m)
		if m == "[IMAGE]" {
			w.WriteHeader(400)
			fmt.Fprint(w, `{"error":{"message":"Model does not support the requested response modalities: image"}}`)
			return
		}
		fmt.Fprintf(w, `{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":%q}}]},"finishReason":"STOP"}]}`, b64)
	}))
	defer srv.Close()
	res, err := GeminiImage(context.Background(), Endpoint{BaseURL: srv.URL}, "gemini-x-image", ImageRequest{Prompt: "x"})
	if err != nil || len(res.Images) != 1 || fmt.Sprint(seen) != "[[IMAGE] [TEXT IMAGE]]" {
		t.Fatalf("err %v images %d requests %v", err, len(res.Images), seen)
	}
}
