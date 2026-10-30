package work

import (
	"encoding/json"
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
