package agentwire

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gerdr-dev/gerdr/acpx"
	"github.com/gerdr-dev/gerdr/internal/ext"
)

func TestProjectPrompt(t *testing.T) {
	dir := t.TempDir()
	f := ProjectPrompt(ext.New(dir))
	if got := f("s", "/x", map[string]any{}); got != "" {
		t.Fatalf("no project: %q", got)
	}
	meta := map[string]any{acpx.MetaKey: map[string]any{ProjectKey: "lb"}}
	if got := f("s", "/x", meta); !strings.Contains(got, "(empty)") || !strings.Contains(got, filepath.Join(dir, "projects", "lb", "memory")) {
		t.Fatalf("empty memory: %q", got)
	}
	mem := filepath.Join(dir, "projects", "lb", "memory")
	_ = os.MkdirAll(mem, 0o755)
	_ = os.WriteFile(filepath.Join(mem, "MEMORY.md"), []byte("- [Deploy](deploy.md) — via drone\n"), 0o644)
	if got := f("s", "/x", meta); !strings.Contains(got, "via drone") {
		t.Fatalf("index missing: %q", got)
	}
}
