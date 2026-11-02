package work

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestProjectLayouts(t *testing.T) {
	r := New(t.TempDir(), nil)
	r.SaveLayouts(json.RawMessage(`{
		"version": 1, "active": "w2",
		"spaces": [{"id": "default", "active": "w1"}, {"id": "客户 A", "active": "w2"}, {"id": "ccc", "active": "w4"}],
		"workspaces": [
			{"id": "w1", "tabs": []},
			{"id": "w2", "space": "客户 A", "tabs": [{"id": "t1"}]},
			{"id": "w3", "space": "客户 A", "tabs": []},
			{"id": "w4", "space": "ccc", "tabs": []}
		]}`))
	a, err := r.Layout("客户 A")
	if err != nil || a == nil || len(a.Workspaces) != 2 || a.Active != "w2" {
		t.Fatalf("客户 A: %+v %v", a, err)
	}
	if d, _ := r.Layout("default"); d == nil || d.Active != "w1" || len(d.Workspaces) != 1 {
		t.Fatalf("default: %+v", d)
	}
	// ccc gets closed: the next save doesn't have it, and its copy stays as it was.
	r.SaveLayouts(json.RawMessage(`{"version": 1, "active": "w1", "spaces": [{"id": "default"}], "workspaces": [{"id": "w1", "tabs": []}]}`))
	if c, _ := r.Layout("ccc"); c == nil || c.Active != "w4" {
		t.Fatalf("ccc's copy should survive closing: %+v", c)
	}
	if n, _ := r.Layout("never"); n != nil {
		t.Fatal("unknown project has no layout")
	}
	if _, err := r.Layout("../x"); err == nil {
		t.Fatal("bad name accepted")
	}
}

func TestRemoveProject(t *testing.T) {
	r := New(t.TempDir(), nil)
	if _, err := r.Ensure("gone"); err != nil {
		t.Fatal(err)
	}
	r.SaveLayouts(json.RawMessage(`{"version": 1, "active": "w1", "spaces": [{"id": "gone"}], "workspaces": [{"id": "w1", "space": "gone", "tabs": []}]}`))
	trash, err := r.Remove("gone")
	if err != nil {
		t.Fatal(err)
	}
	// Soft: everything is still there, in the trash
	if _, err := os.Stat(filepath.Join(trash, "work", "CLAUDE.md")); err != nil {
		t.Fatalf("not kept in the trash (%s): %v", trash, err)
	}
	if l, _ := r.Layout("gone"); l != nil {
		t.Fatalf("layout copy should be gone: %+v", l)
	}
	// Undo: it comes back as it was; only from the trash, and not over a project made since
	if r.Restore("gone", filepath.Join(r.Root, "work")) == nil || r.Restore("other", trash) == nil {
		t.Fatal("restore must only take this project's trash")
	}
	if err := r.Restore("gone", trash); err != nil {
		t.Fatal(err)
	}
	if l, _ := r.Layout("gone"); l == nil {
		t.Fatal("restored project lost its layout copy")
	}
	if trash, err = r.Remove("gone"); err != nil {
		t.Fatal(err)
	}
	_, e1 := r.Remove("default")
	_, e2 := r.Remove("../x")
	if e1 == nil || e2 == nil {
		t.Fatal("default / bad names must be refused")
	}
	// Re-created under the same name: the bundled files come back
	dir, err := r.Ensure("gone")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "CLAUDE.md")); err != nil {
		t.Fatalf("re-created project not reinstalled: %v", err)
	}
}
