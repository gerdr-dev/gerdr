package uisite

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestExtractClasses(t *testing.T) {
	src := "import x from \"./x\";\n" +
		"// don't: an apostrophe in a comment\n" +
		"const a = cn(\"px-3 py-1.5 hover:bg-accent\", on && 'data-[state=open]:animate-in', \"content-['x']\");\n" +
		"const b = `w-[min(42rem,90vw)] ${wide ? \"max-w-3xl\" : \"\"} gap-${n} min(50vh, [&>*:first-child]:mt-0`;\n" +
		"const c = \"标题 text-sm \\\"quoted\\\" Can't\";\n"
	got := extractClasses([]byte(src))
	for _, want := range []string{"px-3", "py-1.5", "hover:bg-accent", "data-[state=open]:animate-in", "content-['x']", "w-[min(42rem,90vw)]", "max-w-3xl", "[&>*:first-child]:mt-0"} {
		if !slices.Contains(got, want) {
			t.Errorf("missing %q in %v", want, got)
		}
	}
	for _, bad := range []string{"min(50vh,", "gap-${n}", "标题", "${wide", "Can't"} {
		if slices.Contains(got, bad) {
			t.Errorf("%q must be dropped (unbalanced brackets / quotes, braces or non-ASCII break @source inline): %v", bad, got)
		}
	}
	for _, w := range got {
		if strings.ContainsAny(w, "\"\\{}") {
			t.Errorf("%q would break out of @source inline(\"...\")", w)
		}
	}
}

func TestBuildPrecompilesSourceClasses(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, s string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(s), 0o644)
	}
	write("package.json", `{"dependencies":{}}`)
	write("src/index.css", "@import \"tailwindcss\";\n")
	write("src/pages/index.tsx", `import { Dlg } from "../Dlg"; export default function P() { return <div className="flex p-4"><Dlg /></div> }`)
	write("src/Dlg.tsx", `export function Dlg() { return <div className="sm:max-w-3xl">x</div> }`)
	write("src/unused.tsx", `export const u = "never-imported-class";`)

	r := New(dir, t.TempDir(), nil)
	css := r.ensure().css
	for _, want := range []string{`@source inline("flex p-4 sm:max-w-3xl")`} {
		if !strings.Contains(css, want) {
			t.Errorf("css lacks %q:\n%s", want, css)
		}
	}
	if strings.Contains(css, "never-imported-class") {
		t.Error("only files the page loads count")
	}
	id := r.ensure().id

	// Same classes, different code: the CSS (and so the hot update) must not change
	write("src/Dlg.tsx", `export function Dlg() { const n = 1; return <div className="sm:max-w-3xl">{n}</div> }`)
	if b := r.ensure(); b.css != css || b.id == id {
		t.Errorf("css changed without new classes (id %s → %s)", id, b.id)
	}
	write("src/Dlg.tsx", `export function Dlg() { return <div className="sm:max-w-3xl ring-2">x</div> }`)
	if b := r.ensure(); !strings.Contains(b.css, "ring-2") {
		t.Error("a class added in an edit isn't picked up")
	}
}
