package agentwire

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gerdr-dev/gerdr/acpx"
	"github.com/gerdr-dev/gerdr/internal/ext"
	"github.com/gerdr-dev/gerdr/internal/repomem"
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

func TestSessionPromptArtifacts(t *testing.T) {
	p := SessionPrompt(nil)
	if got := p("s", "/repo", map[string]any{}); strings.Contains(got, "Artifacts folder") {
		t.Fatal("no artifacts dir, but the section is there")
	}
	got := p("s", "/repo", map[string]any{acpx.MetaKey: map[string]any{acpx.ArtifactsKey: "/Users/me/Gerdr/my/weave"}})
	for _, want := range []string{"/Users/me/Gerdr/my/weave", "committed as usual", "gerdr artifacts link"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestRepoMemoryText(t *testing.T) {
	root := t.TempDir()
	repo := t.TempDir()
	other := "/src/other-repo"
	if _, _, err := repomem.Add(root, repo, "ships straight to main"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repomem.Add(root, other, "release approved by Zhang"); err != nil {
		t.Fatal(err)
	}
	got := repoMemoryText(root, repo)
	for _, want := range []string{"# Repository memory", "- ships straight to main", repomem.Path(root, repo), "- " + other + ": " + repomem.Path(root, other), "gerdr memory add"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "release approved by Zhang") {
		t.Error("another repository's notes went in whole")
	}
}
