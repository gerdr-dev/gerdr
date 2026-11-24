package native

import "testing"

func TestModelName(t *testing.T) {
	for id, want := range map[string]string{
		"claude-fable-5-2":             "Fable 5.2",
		"claude-opus-5-5":              "Opus 5.5",
		"claude-opus-4-1-20250805":     "Opus 4.1",
		"claude-3-5-sonnet-20241022":   "Sonnet 3.5",
		"claude-sonnet-4-5@20250929":   "Sonnet 4.5",
		"anthropic/claude-opus-4.5":    "Opus 4.5",
		"claude-3-7-sonnet-latest":     "Sonnet 3.7",
		"gpt-5.5":                      "GPT-5.5",
		"gpt-5.1-codex-max":            "GPT-5.1 Codex Max",
		"gpt-4o-2024-08-06":            "GPT-4o",
		"gemini-3-pro-preview":         "Gemini 3 Pro Preview",
		"gemini-2.5-pro-preview-06-05": "Gemini 2.5 Pro Preview",
		"google/gemini-2.5-flash":      "Gemini 2.5 Flash",
		"deepseek-v4":                  "DeepSeek V4",
		"deepseek-chat":                "DeepSeek Chat",
		"o3":                           "o3",
		"o4-mini":                      "o4-mini",
		"qwen3-coder-plus":             "qwen3-coder-plus",
		"my-model":                     "my-model",
		"claude":                       "claude",
	} {
		if got := ModelName(id); got != want {
			t.Errorf("ModelName(%q) = %q, want %q", id, got, want)
		}
	}
}
