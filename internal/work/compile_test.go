package work

import (
	"os"
	"path/filepath"
	"testing"
)

// A cached build is reused only while the files it was built from are unchanged: editing a file the function
// imports (not just the file itself) is picked up on the next run.
func TestCompileCacheFollowsImports(t *testing.T) {
	r, dir := newProject(t, map[string]string{
		"_lib.ts": `export const word = "one"`,
		"t.ts":    `import { word } from "./_lib"; export function get() { return word }`,
	})
	res, _, err := run(t, r, "t.get", nil)
	if err != nil || res != "one" {
		t.Fatalf("first run: %v %v", res, err)
	}
	p1, _ := compile(filepath.Join(dir, "local", "t.ts"))
	p2, _ := compile(filepath.Join(dir, "local", "t.ts"))
	if p1 != p2 {
		t.Fatal("unchanged files compiled again")
	}
	if err := os.WriteFile(filepath.Join(dir, "local", "_lib.ts"), []byte(`export const word = "two!"`), 0o644); err != nil {
		t.Fatal(err)
	}
	res, _, err = run(t, r, "t.get", nil)
	if err != nil || res != "two!" {
		t.Fatalf("after editing the import: %v %v", res, err)
	}
}
