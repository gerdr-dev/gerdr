package work

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func textResult(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	out, _ := json.Marshal(map[string]any{"content": []map[string]any{{"type": "text", "text": string(b)}}})
	return out
}

func TestErrorsAndInstall(t *testing.T) {
	dir := t.TempDir()
	r := New(dir, func(ctx context.Context, server, tool string, args map[string]any) (json.RawMessage, error) {
		return json.RawMessage(`{"content":[{"type":"text","text":"denied"}],"isError":true}`), nil
	})
	d, err := r.Ensure("")
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(d, "local", "my.ts")
	os.WriteFile(p, []byte(`export async function call(input, ctx) { return await ctx.mcp("x", "y", {}) }`), 0o644)
	if _, err := r.Run(context.Background(), "", "my.call", nil); err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("want the MCP error, got %v", err)
	}
	if _, err := r.Run(context.Background(), "", "my.nope", nil); err == nil {
		t.Fatal("missing export should fail")
	}
	if _, err := r.Run(context.Background(), "", "../x.y", nil); err == nil {
		t.Fatal("bad name should fail")
	}
	// A built-in file the user edited survives the next install (a new process).
	home := filepath.Join(d, "ui", "src", "pages", "index.tsx")
	os.WriteFile(home, []byte("// mine"), 0o644)
	r = New(dir, r.MCP)
	if _, err := r.Ensure(""); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(home); string(b) != "// mine" {
		t.Fatalf("edited file got replaced: %q", b)
	}
}

func TestProjects(t *testing.T) {
	root := t.TempDir()
	r := New(root, nil)
	for _, c := range []struct{ project, dir string }{
		{"", "work"}, {"default", "work"}, {"s3k2", "projects/s3k2/work"}, {"客户 A", "projects/客户 A/work"},
	} {
		d, err := r.Dir(c.project)
		if err != nil || d != filepath.Join(root, c.dir) {
			t.Fatalf("%q: %s %v", c.project, d, err)
		}
		want := c.project
		if want == "" {
			want = DefaultProject
		}
		if got := r.ProjectOf(filepath.Join(d, "ui", "src")); got != want {
			t.Fatalf("ProjectOf(%s) = %s", d, got)
		}
	}
	for _, bad := range []string{"../x", "a/b/c", "a/..", "../b", ".hidden", "v1.2", " pad", "a\\b"} {
		if _, err := r.Dir(bad); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
	d, err := r.Ensure("p1")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"CLAUDE.md", "ui/src/layout.tsx", "ui/src/pages/index.tsx", "ui/package.json"} {
		if _, err := os.Stat(filepath.Join(d, f)); err != nil {
			t.Fatal(err)
		}
	}
	// Every project, the default one included, starts with the same blank home page.
	def, _ := r.Ensure("")
	a, _ := os.ReadFile(filepath.Join(d, "ui/src/pages/index.tsx"))
	b, _ := os.ReadFile(filepath.Join(def, "ui/src/pages/index.tsx"))
	if len(a) == 0 || string(a) != string(b) {
		t.Fatal("default project should get the same blank home page")
	}
}

// A Work directory that got the OKR files from an older gerdr loses them on upgrade unless they were edited,
// and its untouched OKR home page goes back to the blank one.
func TestInstallRemovesStale(t *testing.T) {
	root := t.TempDir()
	r := New(root, nil)
	d, _ := r.Dir("")
	old := map[string][]byte{
		"local/okr.ts":           []byte("export async function summary() {}"),
		"local/okr2.ts":          []byte("export async function more() {}"),
		"ui/src/pages/index.tsx": []byte("// old OKR page: run('okr.summary')"),
	}
	marks := map[string]string{}
	for rel, b := range old {
		p := filepath.Join(d, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, b, 0o644)
		marks[rel] = hash(b)
	}
	os.WriteFile(filepath.Join(d, "local", "okr2.ts"), []byte("// mine now"), 0o644) // edited: stays
	mb, _ := json.Marshal(marks)
	os.WriteFile(filepath.Join(d, ".builtin.json"), mb, 0o644)
	if _, err := r.Ensure(""); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(d, "ui/src/pages/index.tsx")); string(b) != string(bundled()["ui/src/pages/index.tsx"]) {
		t.Fatal("untouched OKR page should become the blank home page")
	}
	if _, err := os.Stat(filepath.Join(d, "local", "okr.ts")); err == nil {
		t.Fatal("untouched okr.ts should be removed")
	}
	if b, _ := os.ReadFile(filepath.Join(d, "local", "okr2.ts")); string(b) != "// mine now" {
		t.Fatal("edited file removed")
	}
}
