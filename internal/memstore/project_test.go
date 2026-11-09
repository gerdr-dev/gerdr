package memstore

import (
	"path/filepath"
	"testing"
)

func TestProjectMemory(t *testing.T) {
	dir := t.TempDir()
	r := Roots{Projects: filepath.Join(dir, "projects")}
	ok := filepath.Join(dir, "projects", "lb", "memory", "deploy.md")
	if err := Write(r, ok, "---\nname: deploy\ndescription: how\ntype: project\n---\nx"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		filepath.Join(dir, "projects", "lb", "layout.md"),
		filepath.Join(dir, "projects", "lb", "panels", "x", "a.md"),
		filepath.Join(dir, "projects", "lb", "memory", "..", "a.md"),
	} {
		if _, err := Allowed(r, bad); err == nil {
			t.Errorf("%s allowed", bad)
		}
	}
	got := ListProjects(r, map[string]string{"default": "my", "lb": "lb"})
	if len(got) != 2 || got[0].Label != "lb" || len(got[0].Entries) != 1 || got[0].Project != "lb" || got[1].Label != "my" || len(got[1].Entries) != 0 {
		t.Fatalf("ListProjects = %+v", got)
	}
}
