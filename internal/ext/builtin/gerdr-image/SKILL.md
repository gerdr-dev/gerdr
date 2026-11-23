---
name: gerdr-image
description: "Making images from inside Gerdr: generate a picture from a description, or edit / restyle / combine existing images (`gerdr image gen`, with -i for the images to change), using the image model the person set up in Gerdr (Nano Banana / Gemini image models, gpt-image). Use when asked to draw, generate, design or edit an image, illustration, icon, poster, mockup or photo. Also which image models there are (`gerdr image models`) and how to set one up. Requires GERDR_ENV=1."
---

# Gerdr: making images

Only inside Gerdr (the GUI you run in). Check first; if not, say so and stop:

```bash
test "${GERDR_ENV:-}" = 1
```

Gerdr makes the request with the image model set up in Settings → Agent → Roles ("Image models", under the engines), with the
person's own keys; you never need a key. Gerdr Agent has the same thing as its `generate_image` tool: use the tool
there, the command anywhere else.

```bash
gerdr image gen "an orange cat on a windowsill, watercolor, warm afternoon light" --aspect 3:2 -o cat.png
gerdr image gen "make the background a city at night, keep the cat as is" -i cat.png -o cat-night.png
gerdr image gen "the two characters side by side, in the style of the first" -i style.png -i character.png
gerdr image gen "app icon: a paw print, flat, rounded square" -n 4 -o icons/      # 4 to choose from
gerdr image models                     # the image models; * = the one used by default
```

It prints each saved file's absolute path, one per line (stderr says which model and anything it said). Without
`-o` the files go in the current directory as `image-<time>.png`; in a task a panel started, put them in the
artifacts folder instead (gerdr-events skill).

- **Prompt**: be specific — subject, composition, style, lighting, colours, and any text to render (quoted). To
  change an image, give it with `-i` and say what to change and what to keep; iterate on the result the same way.
- `--aspect` 1:1, 3:2, 2:3, 4:3, 3:4, 16:9, 9:16, 21:9…; `--size` 1K / 2K / 4K on Gemini Pro image models, WxH
  (1536x1024) on OpenAI; `-n` 1-4; `--model provider/model` only when the person asks for a particular model.
- **Show the result** in your reply as a Markdown image with the absolute path: `![the cat at night](/abs/cat-night.png)`.
  Look at it yourself first (read the file) when you can see images, and redo it if it misses.
- Takes from seconds to a minute or two per image, and each image costs the person money: don't generate many
  variations unasked.

## Setting it up

"No image model" means none is configured. An image model is a model on a provider whose id says so
(`gemini-2.5-flash-image`, `gemini-3-pro-image-preview`, `gpt-image-1`) or that is marked `"kind": "image"`.
Add one to a provider the person already has (Gemini or OpenAI-compatible; Anthropic can't make images):

```bash
gerdr config get llm.providers                                   # which providers there are (keys show as <hidden>)
gerdr config add llm.providers.<gemini provider id>.models '{"id": "gemini-2.5-flash-image"}'
gerdr config set llm.imageModel <provider id>/gemini-2.5-flash-image  # optional: unset = the first one, Gemini first
```

Image models never show up as a session's model. A gateway that speaks Chat Completions may still not have
OpenAI's Images API (`/images/generations`): the error says so; use another provider then.
