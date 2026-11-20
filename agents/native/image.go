package native

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/coder/acp-go-sdk"

	"github.com/gerdr-dev/gerdr/agents/native/llm"
	"github.com/gerdr-dev/gerdr/internal/i18n"
)

// Image generation, shared by the generate_image tool and the backend's /api/image (`gerdr image gen`, which
// Claude Code / Codex use through the CLI). The model is Config.ImageModel, or one picked from the providers;
// the request goes out from Gerdr, so agents never see the keys.

// imageModelID: ids that are image generation models — Nano Banana (gemini-2.5-flash-image,
// gemini-3-pro-image-preview), gpt-image-1, dall-e-3. Not Imagen: that's Google's :predict API, which isn't
// supported, so guessing it as an image model would only produce errors.
func imageModelID(id string) bool {
	id = strings.ToLower(id)
	if i := strings.LastIndex(id, "/"); i >= 0 {
		id = id[i+1:]
	}
	if strings.Contains(id, "imagen") {
		return false
	}
	return strings.Contains(id, "-image") || strings.HasPrefix(id, "gpt-image") || strings.HasPrefix(id, "dall-e") || strings.Contains(id, "nano-banana")
}

// canImage: the provider's protocol has an image API we speak. Anthropic doesn't make images.
func canImage(p Provider) bool { return p.API == "gemini" || p.API == "" }

// ImageModel is one image model for a picker.
type ImageModel struct {
	Key      string `json:"key"` // provider/model
	Name     string `json:"name"`
	Provider string `json:"provider"` // its display name
	Current  bool   `json:"current"`  // the one generation uses when no model is given
}

// ImageModels lists the image models, marking the one in use.
func (c Config) ImageModels() []ImageModel {
	cur, _ := c.resolveImage("")
	var out []ImageModel
	for _, m := range c.allModels() {
		if m.Kind != "image" || !canImage(m.Provider) {
			continue
		}
		pn := m.Provider.Name
		if pn == "" {
			pn = m.Provider.ID
		}
		out = append(out, ImageModel{Key: m.Key, Name: m.Name, Provider: pn, Current: m.Key == cur.Key})
	}
	return out
}

// ResolveImageModel is the provider/model a generation with this model ("" = the configured one) would use.
func (c Config) ResolveImageModel(model string) (string, error) {
	m, err := c.resolveImage(model)
	return m.Key, err
}

// resolveImage picks the model: the one asked for, else ImageModel, else the first image model on a Gemini
// provider (Nano Banana is cheap and good at edits), then on an OpenAI-compatible one. Only models whose kind is
// image count for the automatic pick: a gateway with the Gemini protocol doesn't necessarily have Nano Banana.
func (c Config) resolveImage(model string) (resolved, error) {
	all := c.allModels()
	for _, want := range []string{model, c.ImageModel} {
		if want == "" {
			continue
		}
		for _, m := range all {
			if m.Key == want {
				if !canImage(m.Provider) {
					return resolved{}, fmt.Errorf(i18n.S("%s 的服务商用 Anthropic 协议，不能生图", "%s is on an Anthropic-protocol provider, which can't generate images"), want)
				}
				return m, nil
			}
		}
		return resolved{}, fmt.Errorf(i18n.S("没有这个模型：%s（gerdr image models 列出能用的）", "no such model: %s (gerdr image models lists the ones available)"), want)
	}
	for _, api := range []string{"gemini", ""} {
		for _, m := range all {
			if m.Kind == "image" && m.Provider.API == api {
				return m, nil
			}
		}
	}
	return resolved{}, errors.New(i18n.S(
		"没有生图模型：在设置 → Agent 的「生图模型」里添加一个（比如 Gemini 的 gemini-2.5-flash-image 或 OpenAI 的 gpt-image-1）",
		"No image model: add one in Settings → Agent, under Image models (e.g. gemini-2.5-flash-image on Gemini, or gpt-image-1 on OpenAI)"))
}

// GenerateImage makes images with model ("" = the configured / picked one). It returns the provider/model used.
func GenerateImage(ctx context.Context, cfg Config, model string, req llm.ImageRequest) (llm.ImageResult, string, error) {
	m, err := cfg.resolveImage(model)
	if err != nil {
		return llm.ImageResult{}, "", err
	}
	if strings.TrimSpace(req.Prompt) == "" {
		return llm.ImageResult{}, m.Key, errors.New("the prompt is empty")
	}
	req.N = min(max(req.N, 1), 4)
	var res llm.ImageResult
	if m.Provider.API == "gemini" {
		res, err = llm.GeminiImage(ctx, m.endpoint(), m.ID, req)
	} else {
		res, err = llm.OpenAIImage(ctx, m.endpoint(), m.ID, req)
	}
	return res, m.Key, err
}

// ImagePaths is where n images go. out is a file (with n > 1 it gets -1, -2… before the extension) or a
// directory (an existing one, or ending in a slash); empty = dir. Names made up here are image-<time>.<ext>.
// ext is the generated image's (png / jpg…); a file name given without an extension gets it.
func ImagePaths(out, dir string, n int, ext string) []string {
	isDir := out == "" || strings.HasSuffix(out, "/") || strings.HasSuffix(out, string(filepath.Separator))
	if !isDir {
		if st, err := os.Stat(out); err == nil && st.IsDir() {
			isDir = true
		}
	}
	var base string
	if isDir {
		if out == "" {
			out = dir
		}
		base = filepath.Join(out, "image-"+time.Now().Format("20060102-150405"))
	} else {
		base = strings.TrimSuffix(out, filepath.Ext(out))
		if e := strings.TrimPrefix(filepath.Ext(out), "."); e != "" {
			ext = e
		}
	}
	var paths []string
	for i := range n {
		p := base
		if n > 1 {
			p += fmt.Sprintf("-%d", i+1)
		}
		p += "." + ext
		if isDir { // made-up names never overwrite
			for j := 2; exists(p); j++ {
				p = strings.TrimSuffix(p, "."+ext)
				p = strings.TrimSuffix(p, fmt.Sprintf("_%d", j-1)) + fmt.Sprintf("_%d.%s", j, ext)
			}
		}
		paths = append(paths, p)
	}
	return paths
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// ---- generate_image ----

// generateImageTool is only offered when an image model resolves (newRun). Not read-only: it costs money and
// writes files, so it is asked about like other tools ("tool:generate_image").
var generateImageTool = &tool{
	name: "generate_image",
	desc: "Generate an image from a text prompt, or edit / combine existing images (pass their paths in images), with the image model configured in Gerdr. " +
		"Saves the result as a file and returns its path. Write a detailed prompt: subject, composition, style, lighting, any text to render. " +
		"To change an image you generated, call again with it in images and say what to change. " +
		"Show the result to the user by putting it in your reply as a Markdown image with its absolute path: ![what it shows](/abs/path.png).",
	params: obj(map[string]any{
		"prompt": prop("string", "What to draw, or what to change in the given images"),
		"images": map[string]any{"type": "array", "items": map[string]any{"type": "string"},
			"description": "Paths of reference images: to edit, or to take a subject / style from (optional)"},
		"aspect_ratio": prop("string", "1:1, 3:2, 2:3, 4:3, 3:4, 16:9, 9:16, 21:9… (optional)"),
		"size":         prop("string", "Gemini Pro image models: 1K, 2K or 4K; OpenAI: WxH such as 1536x1024 (optional)"),
		"n":            prop("integer", "How many images, 1-4 (optional, default 1)"),
		"path":         prop("string", "File or directory to save to (optional; default: the working directory, named image-<time>.png)"),
		"model":        prop("string", "provider/model of another image model, only when the user asks for one (optional)"),
	}, "prompt"),
	show: func(args map[string]any, cwd string) (string, acp.ToolKind, []acp.ToolCallLocation) {
		return "Generate image: " + oneLine(str(args, "prompt"), 120), acp.ToolKindOther, nil
	},
	run: func(c *call, args map[string]any) result {
		req := llm.ImageRequest{Prompt: str(args, "prompt"), Aspect: str(args, "aspect_ratio"), Size: str(args, "size"), N: num(args, "n")}
		refs, _ := args["images"].([]any)
		for _, x := range refs {
			s, _ := x.(string)
			if s == "" {
				continue
			}
			p := c.abs(s)
			mime, ok := imageExt[strings.ToLower(filepath.Ext(p))]
			if !ok {
				return errResult("%s: not a png / jpg / gif / webp image", p)
			}
			b, err := c.readFile(p)
			if err != nil {
				return errResult("%v", err)
			}
			req.Images = append(req.Images, llm.Part{Type: "image", MIME: mime, Data: base64.StdEncoding.EncodeToString(b)})
		}
		res, key, err := GenerateImage(c.ctx, c.r.s.a.config(), str(args, "model"), req)
		if err != nil {
			return errResult("%v", err)
		}
		out := str(args, "path")
		if out != "" {
			sep := strings.HasSuffix(out, "/")
			out = c.abs(out)
			if sep {
				out += string(filepath.Separator)
			}
		}
		paths := ImagePaths(out, c.cwd(), len(res.Images), llm.ImageExt(res.Images[0].MIME))
		var b strings.Builder
		fmt.Fprintf(&b, "Generated with %s:\n", key)
		var parts []llm.Part
		for i, img := range res.Images {
			if err := c.writeFile(paths[i], img.Data); err != nil {
				return errResult("saving %s: %v", paths[i], err)
			}
			fmt.Fprintf(&b, "%s (%d bytes)\n", paths[i], len(img.Data))
			if c.r.model.seesImages() {
				parts = append(parts, c.r.s.a.imagePart(img.MIME, img.Data))
			}
		}
		if res.Text != "" {
			fmt.Fprintf(&b, "The image model said: %s\n", res.Text)
		}
		b.WriteString("Show it in your reply as ![…](absolute path).")
		return result{text: b.String(), images: parts}
	},
}
