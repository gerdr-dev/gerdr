package uisite

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInlineCSSImports(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	write := func(p, s string) {
		t.Helper()
		p = filepath.Join(src, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("styles/animate.css", "@import \"./keys.css\";\n.a{x:1}\n")
	write("styles/keys.css", "@keyframes k{}\n@import '../index.css';\n")
	write("index.css", "")
	if err := os.WriteFile(filepath.Join(dir, "outside.css"), []byte(".evil{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	index := filepath.Join(src, "index.css")
	in := "@import \"tailwindcss\";\n@import \"./styles/animate.css\";\n@import \"../outside.css\";\n@import \"./nope.css\";\n.b{y:2}\n"

	out, warns := inlineCSSImports(src, index, in)
	for _, want := range []string{"@keyframes k{}", ".a{x:1}", ".b{y:2}", `@import "tailwindcss";`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, ".evil") || strings.Contains(out, "@import \"./") || strings.Contains(out, "../index.css") {
		t.Errorf("a local import survived or escaped src/:\n%s", out)
	}
	// keys.css importing index.css back is a cycle; ../outside.css leaves src/; nope.css doesn't exist
	if len(warns) != 3 {
		t.Errorf("want 3 warnings (cycle, outside, missing), got %q", warns)
	}
	if strings.Index(out, "@keyframes k{}") > strings.Index(out, ".a{x:1}") {
		t.Errorf("an import must land where it was written:\n%s", out)
	}
}

// Tailwind browser drops every style when "@import" appears in a comment; browserCSS defuses it.
func TestBrowserCSSDefusesImportInComments(t *testing.T) {
	out, dropped := browserCSS("/* pulled in with @import \"./a.css\" */\n@import \"tailwindcss\";\n.a{x:1}\n")
	if strings.Contains(out, "@import \"./a.css\"") || !strings.Contains(out, "pulled in with import") || !strings.Contains(out, ".a{x:1}") {
		t.Errorf("got:\n%s", out)
	}
	if len(dropped) != 0 {
		t.Errorf("a comment is not an import to report: %q", dropped)
	}
}
