package folder

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPlan(t *testing.T) {
	base := map[string]string{"same": "1", "srcEdit": "1", "dstEdit": "1", "both": "1", "goneThere": "1", "goneHereEdited": "1"}
	src := []Entry{{Path: "same", Sha: "1"}, {Path: "srcEdit", Sha: "2"}, {Path: "dstEdit", Sha: "1"}, {Path: "both", Sha: "2"}, {Path: "new", Sha: "9"}, {Path: "goneHereEdited", Sha: "2"}, {Path: "newBoth", Sha: "a"}}
	dst := []Entry{{Path: "same", Sha: "1"}, {Path: "srcEdit", Sha: "1"}, {Path: "dstEdit", Sha: "3"}, {Path: "both", Sha: "3"}, {Path: "goneThere", Sha: "1"}, {Path: "newBoth", Sha: "b"}}
	got := map[string]string{}
	for _, c := range Plan(base, src, dst) {
		got[c.Path] = c.Action
	}
	want := map[string]string{"srcEdit": "copy", "both": "conflict", "new": "copy", "goneThere": "deletedThere", "goneHereEdited": "deletedHere", "newBoth": "conflict"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("plan:\n got %v\nwant %v", got, want)
	}
}

func TestWalkIgnores(t *testing.T) {
	d := t.TempDir()
	for _, p := range []string{"a.png", "out/b.mp4", "node_modules/x.js", "cache/c.tmp", ".gerdr-folder.json", "keep/cache/d.txt"} {
		os.MkdirAll(filepath.Join(d, filepath.Dir(p)), 0o755)
		os.WriteFile(filepath.Join(d, p), []byte(p), 0o644)
	}
	os.WriteFile(filepath.Join(d, IgnoreName), []byte("# render cache\n/cache/\n*.tmp\n"), 0o644)
	es, err := Walk(d)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range es {
		got = append(got, e.Path)
	}
	want := []string{IgnoreName, "a.png", "keep/cache/d.txt", "out/b.mp4"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("walk: %v, want %v", got, want)
	}
	if ConflictName("out/cover.png", "Mac mini") != "out/cover (Mac mini).png" {
		t.Fatal(ConflictName("out/cover.png", "Mac mini"))
	}
}
