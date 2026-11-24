package native

import (
	"regexp"
	"strings"
)

// ModelName is the short name a model shows under when its config gives none: the composer's button, the model menu.
// Ids are long ("claude-fable-5-2", "anthropic/claude-opus-4.5", "gemini-2.5-pro-preview-06-05")
// and on a phone the composer's row had room for little else, while Claude Code names the same model "Fable 5.2".
// Only the families recognised here are renamed; anything else keeps its id, as a wrong guess would be worse than a
// long name. A name in the config always wins.
func ModelName(id string) string {
	base := id
	if i := strings.LastIndexByte(base, '/'); i >= 0 { // a gateway's vendor/ prefix (OpenRouter)
		base = base[i+1:]
	}
	if i := strings.IndexByte(base, '@'); i >= 0 { // Vertex's claude-sonnet-4-5@20250929
		base = base[:i]
	}
	words := strings.FieldsFunc(strings.ToLower(base), func(r rune) bool { return r == '-' || r == '_' || r == ' ' })
	if len(words) < 2 {
		return id
	}
	// A release date at the end (20250929, 2024-08-06, preview-06-05) and "latest" say nothing a person picks by
	for len(words) > 2 && (dateWord.MatchString(words[len(words)-1]) || words[len(words)-1] == "latest") {
		words = words[:len(words)-1]
	}
	var out string
	switch words[0] {
	case "claude":
		out = claudeName(words[1:])
	case "gpt":
		// gpt-5.1-codex-max → GPT-5.1 Codex Max
		out = "GPT-" + words[1] + titled(words[2:])
	case "gemini", "gemma":
		out = strings.ToUpper(words[0][:1]) + words[0][1:] + titled(words[1:])
	case "deepseek":
		out = "DeepSeek" + titled(words[1:])
	}
	if out == "" {
		return id
	}
	return out
}

var dateWord = regexp.MustCompile(`^(\d{8}|\d{4}|\d{2})$`)
var numWord = regexp.MustCompile(`^\d+(\.\d+)?$`)

// claudeName: claude-opus-4-1 and the older claude-3-5-sonnet both → the family, then the version with dots
// ("Opus 4.1", "Sonnet 3.5"), as Claude Code shows them.
func claudeName(words []string) string {
	var family []string
	var ver []string
	for _, w := range words {
		if numWord.MatchString(w) {
			ver = append(ver, w)
		} else {
			family = append(family, w)
		}
	}
	if len(family) == 0 || len(ver) == 0 {
		return ""
	}
	return strings.TrimSpace(titled(family)) + " " + strings.Join(ver, ".")
}

// titled: " Pro Preview" for [pro preview], each word capitalised and led by a space; v4 → V4.
func titled(words []string) string {
	var b strings.Builder
	for _, w := range words {
		b.WriteString(" ")
		b.WriteString(strings.ToUpper(w[:1]) + w[1:])
	}
	return b.String()
}
