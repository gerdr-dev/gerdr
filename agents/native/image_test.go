package native

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gerdr-dev/gerdr/agents/native/llm"
)

func TestImageModelID(t *testing.T) {
	for id, want := range map[string]bool{
		"gemini-2.5-flash-image":            true,
		"models/gemini-3-pro-image-preview": true,
		"gpt-image-1":                       true,
		"dall-e-3":                          true,
		"google/nano-banana-pro":            true,
		"imagen-4.0-generate-001":           false,
		"gemini-3-pro":                      false,
		"gpt-5.5":                           false,
	} {
		if imageModelID(id) != want {
			t.Errorf("imageModelID(%q) = %v", id, !want)
		}
	}
}

func imageCfg() Config {
	return Config{Providers: []Provider{
		{ID: "oa", BaseURL: "https://api.openai.com/v1", Models: []Model{{ID: "gpt-5.5"}, {ID: "gpt-image-1"}}},
		{ID: "ant", API: "anthropic", BaseURL: "https://api.anthropic.com", Models: []Model{{ID: "claude-opus-5-5"}, {ID: "fake-image", Kind: "image"}}},
		{ID: "g", Name: "Google", API: "gemini", BaseURL: "https://generativelanguage.googleapis.com", Models: []Model{{ID: "gemini-3-pro"}, {ID: "gemini-2.5-flash-image"}, {ID: "my-image", Kind: "chat"}}},
	}}
}

func TestImageModelsKeptOutOfChat(t *testing.T) {
	cfg := imageCfg()
	for _, k := range cfg.ModelKeys() {
		if k == "oa/gpt-image-1" || k == "g/gemini-2.5-flash-image" {
			t.Errorf("%s is an image model but listed as a chat model", k)
		}
	}
	if !strings.Contains(strings.Join(cfg.ModelKeys(), " "), "g/my-image") {
		t.Error(`kind "chat" should override the guess from the id`)
	}
	if m, _ := cfg.find("oa/gpt-image-1"); m.Key == "oa/gpt-image-1" {
		t.Error("a session asking for an image model should fall back to a chat model")
	}
}

func TestResolveImage(t *testing.T) {
	cfg := imageCfg()
	if k, err := cfg.ResolveImageModel(""); err != nil || k != "g/gemini-2.5-flash-image" {
		t.Errorf("auto = %q %v, want the Gemini one first", k, err)
	}
	cfg.ImageModel = "oa/gpt-image-1"
	if k, _ := cfg.ResolveImageModel(""); k != "oa/gpt-image-1" {
		t.Errorf("configured = %q", k)
	}
	if k, _ := cfg.ResolveImageModel("g/gemini-2.5-flash-image"); k != "g/gemini-2.5-flash-image" {
		t.Errorf("asked for = %q", k)
	}
	if _, err := cfg.ResolveImageModel("ant/fake-image"); err == nil {
		t.Error("an Anthropic provider can't generate images")
	}
	if _, err := cfg.ResolveImageModel("nope/x"); err == nil {
		t.Error("an unknown model should be an error, not a silent fallback")
	}
	ms := cfg.ImageModels()
	if len(ms) != 2 || !ms[0].Current || ms[0].Key != "oa/gpt-image-1" || ms[1].Provider != "Google" {
		t.Errorf("ImageModels = %+v", ms)
	}
	if _, err := (Config{Providers: imageCfg().Providers[1:2]}).ResolveImageModel(""); err == nil {
		t.Error("no image model should be an error")
	}
}

func TestGenerateImageGemini(t *testing.T) {
	img := base64.StdEncoding.EncodeToString([]byte("\x89PNG\r\n\x1a\n0000"))
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		fmt.Fprintf(w, `{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":%q}}]},"finishReason":"STOP"}]}`, img)
	}))
	defer srv.Close()
	cfg := Config{Providers: []Provider{{ID: "g", API: "gemini", BaseURL: srv.URL, Models: []Model{{ID: "gemini-2.5-flash-image"}}}}}
	res, key, err := GenerateImage(context.Background(), cfg, "", llm.ImageRequest{Prompt: "a cat"})
	if err != nil || key != "g/gemini-2.5-flash-image" || len(res.Images) != 1 || !strings.Contains(path, ":generateContent") {
		t.Fatalf("res %d images, key %q, path %q, err %v", len(res.Images), key, path, err)
	}
}

func TestImagePaths(t *testing.T) {
	dir := t.TempDir()
	if p := ImagePaths(filepath.Join(dir, "cat.jpg"), dir, 1, "png"); p[0] != filepath.Join(dir, "cat.jpg") {
		t.Errorf("a file name keeps its own extension: %v", p)
	}
	if p := ImagePaths(filepath.Join(dir, "cat"), dir, 2, "png"); p[0] != filepath.Join(dir, "cat-1.png") || p[1] != filepath.Join(dir, "cat-2.png") {
		t.Errorf("several: %v", p)
	}
	p := ImagePaths("", dir, 1, "png")
	if filepath.Dir(p[0]) != dir || !strings.HasPrefix(filepath.Base(p[0]), "image-") {
		t.Errorf("default: %v", p)
	}
	_ = os.WriteFile(p[0], nil, 0o644)
	if q := ImagePaths(dir+"/", dir, 1, "png"); q[0] == p[0] {
		t.Errorf("a made-up name overwrote %s", p[0])
	}
}
