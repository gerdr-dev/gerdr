package main

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/gerdr-dev/gerdr/agents/native"
	"github.com/gerdr-dev/gerdr/agents/native/llm"
	"github.com/gerdr-dev/gerdr/internal/i18n"
)

// gerdr image: image generation with the image model configured in Gerdr (config.json llm.imageModel), for every
// agent — Claude Code and Codex through this CLI, the native agent through its generate_image tool. The request is
// made by the backend (POST /api/image), so the keys never reach the agent; the images come back here and are
// written where the command runs.
func imageCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "image",
		Short: i18n.S("生图 / 改图（用设置里配的生图模型：Nano Banana、gpt-image…）", "generate / edit images (with the image model set up in Gerdr: Nano Banana, gpt-image…)"),
	}

	var refs []string
	var out, aspect, size, model string
	var n int
	gen := &cobra.Command{
		Use:     "gen <prompt>",
		Aliases: []string{"generate"},
		Short:   i18n.S("按描述生成图片，或带 -i 改图 / 参考别的图；打印保存下来的文件路径", "generate an image from a prompt, or edit / build on images given with -i; prints the saved files' paths"),
		Long: i18n.S(`按描述生成图片；用 -i 带上图片就是改图（说要改什么）或参照它们画（主体、风格），可以给几张。
图存成文件，打印绝对路径（每行一个）；默认存在当前目录，名字是 image-<时间>.png。
描述写具体：主体、构图、风格、光线、图里要写的字。用哪个模型看 gerdr image models；
在 Gerdr 里给人看图：回复里写 ![说明](绝对路径)。

  gerdr image gen "一只橘猫坐在窗台上，水彩风格，午后阳光" --aspect 3:2 -o cat.png
  gerdr image gen "把背景换成夜晚的城市" -i cat.png -o cat-night.png
  gerdr image gen "海报：Gerdr 1.0 发布，深色背景" --model openai/gpt-image-1`,
			`Generate an image from a prompt; with -i images it edits them (say what to change) or builds on them (subject,
style). Several -i are fine.
The images are saved as files and their absolute paths printed, one per line; by default in the current
directory, named image-<time>.png. Write a detailed prompt: subject, composition, style, lighting, any text to
render. Which model is used: gerdr image models. To show an image to the person in Gerdr, put
![what it shows](/absolute/path) in your reply.

  gerdr image gen "an orange cat on a windowsill, watercolor, afternoon light" --aspect 3:2 -o cat.png
  gerdr image gen "make the background a city at night" -i cat.png -o cat-night.png
  gerdr image gen "poster: Gerdr 1.0 is out, dark background" --model openai/gpt-image-1`),
		Args: cobra.MinimumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			body := map[string]any{"prompt": strings.Join(args, " "), "aspect": aspect, "size": size, "n": n, "model": model}
			var images []map[string]string
			for _, p := range refs {
				mime := map[string]string{".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif", ".webp": "image/webp"}[strings.ToLower(filepath.Ext(p))]
				if mime == "" {
					fail(fmt.Errorf(i18n.S("%s 不是 png / jpg / gif / webp 图片", "%s: not a png / jpg / gif / webp image"), p))
				}
				b, err := os.ReadFile(p)
				fail(err)
				images = append(images, map[string]string{"mime": mime, "data": base64.StdEncoding.EncodeToString(b)})
			}
			body["images"] = images
			c, err := newAPIClient()
			fail(err)
			c.http.Timeout = 10 * time.Minute // a Pro model makes several images in minutes, not seconds
			var res struct {
				Model  string `json:"model"`
				Text   string `json:"text"`
				Images []struct {
					MIME string `json:"mime"`
					Data string `json:"data"`
				} `json:"images"`
			}
			fail(c.do("POST", "/api/image", body, &res))
			if len(res.Images) == 0 {
				fail(errors.New(i18n.S("模型没给图", "the model returned no image")))
			}
			dir, _ := os.Getwd()
			dest := out
			if dest != "" {
				sep := strings.HasSuffix(dest, "/")
				if dest, err = filepath.Abs(dest); err != nil {
					fail(err)
				}
				if sep {
					dest += string(filepath.Separator)
				}
			}
			paths := native.ImagePaths(dest, dir, len(res.Images), llm.ImageExt(res.Images[0].MIME))
			for i, im := range res.Images {
				b, err := base64.StdEncoding.DecodeString(im.Data)
				fail(err)
				fail(os.MkdirAll(filepath.Dir(paths[i]), 0o755))
				fail(os.WriteFile(paths[i], b, 0o644))
				fmt.Println(paths[i])
			}
			fmt.Fprintln(os.Stderr, i18n.S("模型：", "Model: ")+res.Model)
			if res.Text != "" {
				fmt.Fprintln(os.Stderr, i18n.S("模型说：", "The model said: ")+res.Text)
			}
		},
	}
	gen.Flags().StringArrayVarP(&refs, "image", "i", nil, i18n.S("参考图 / 要改的图（可以给多次）", "an image to edit or build on (repeatable)"))
	gen.Flags().StringVarP(&out, "out", "o", "", i18n.S("存到哪：文件，或目录（以 / 结尾或已存在）；几张时文件名后加 -1、-2", "where to save: a file, or a directory (ending in / or existing); several images get -1, -2… in the name"))
	gen.Flags().StringVar(&aspect, "aspect", "", i18n.S("宽高比：1:1、3:2、2:3、4:3、3:4、16:9、9:16、21:9…", "aspect ratio: 1:1, 3:2, 2:3, 4:3, 3:4, 16:9, 9:16, 21:9…"))
	gen.Flags().StringVar(&size, "size", "", i18n.S("尺寸：Gemini Pro 生图模型 1K / 2K / 4K；OpenAI 写 1536x1024 这样", "size: 1K / 2K / 4K on Gemini Pro image models; WxH such as 1536x1024 on OpenAI"))
	gen.Flags().IntVarP(&n, "n", "n", 1, i18n.S("几张（1-4）", "how many (1-4)"))
	gen.Flags().StringVar(&model, "model", "", i18n.S("这次用别的生图模型（provider/model，见 gerdr image models）", "use another image model this time (provider/model, see gerdr image models)"))

	models := &cobra.Command{
		Use:   "models",
		Short: i18n.S("列出生图模型，标出默认用哪个（设置：gerdr config set llm.imageModel <provider/model>）", "list the image models and which one is used by default (set it: gerdr config set llm.imageModel <provider/model>)"),
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			c, err := newAPIClient()
			fail(err)
			var res struct {
				Models []native.ImageModel `json:"models"`
				Error  string              `json:"error"`
			}
			fail(c.do("GET", "/api/image/models", nil, &res))
			for _, m := range res.Models {
				mark := "  "
				if m.Current {
					mark = "* "
				}
				fmt.Printf("%s%s\t%s\n", mark, m.Key, m.Provider)
			}
			if res.Error != "" {
				fail(errors.New(res.Error))
			}
		},
	}
	cmd.AddCommand(gen, models)
	return cmd
}
