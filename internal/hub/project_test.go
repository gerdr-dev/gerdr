package hub

import "testing"

// The adapter learns a session's project from _meta: Options.Project decides (where it's shown now), else the
// project it was started in.
func TestExtMetaProject(t *testing.T) {
	h, err := New(Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	meta := map[string]any{}
	h.extMeta(Record{Project: "lb"}, meta)
	if meta["project"] != "lb" {
		t.Fatalf("Record.Project: %v", meta)
	}
	h.opt.Project = func(rec Record) string { return "moved" }
	meta = map[string]any{}
	h.extMeta(Record{Project: "lb"}, meta)
	if meta["project"] != "moved" {
		t.Fatalf("Options.Project: %v", meta)
	}
	h.opt.Project = func(rec Record) string { return "" }
	meta = map[string]any{}
	h.extMeta(Record{}, meta)
	if _, ok := meta["project"]; ok {
		t.Fatalf("no project: %v", meta)
	}
}
