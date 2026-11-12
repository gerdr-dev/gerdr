package agentwire

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gerdr-dev/gerdr/acpx"
)

// A session's system file is read each time the prompt is built: rewriting it changes what the next start gets.
func TestSessionPromptSystemFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "system.md")
	f := SessionPrompt(nil)
	meta := map[string]any{acpx.MetaKey: map[string]any{SystemFileKey: p}}
	if got := f("s", "/", meta); got != "" {
		t.Fatalf("a missing file should add nothing, got %q", got)
	}
	_ = os.WriteFile(p, []byte("  rules v1\n"), 0o600)
	if got := f("s", "/", meta); got != "rules v1" {
		t.Fatalf("got %q", got)
	}
	_ = os.WriteFile(p, []byte("rules v2"), 0o600)
	if got := f("s", "/", meta); !strings.Contains(got, "v2") {
		t.Fatalf("the rewritten file should be read again, got %q", got)
	}
	if got := f("s", "/", nil); got != "" {
		t.Fatalf("no system file, got %q", got)
	}
}
