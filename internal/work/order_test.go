package work

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
)

// Dragging a panel's button reorders the project's panels; ones added later go after the arranged ones.
func TestMovePanel(t *testing.T) {
	r := New(t.TempDir(), func(context.Context, string, string, map[string]any) (json.RawMessage, error) { return nil, nil })
	if _, err := r.Ensure("p"); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"b", "a"} {
		if _, err := r.NewPanel("p", n); err != nil {
			t.Fatal(err)
		}
	}
	if got := r.PanelNames("p"); !slices.Equal(got, []string{"", "a", "b"}) {
		t.Fatalf("default order: %q", got)
	}
	if err := r.MovePanel("p/b", 0); err != nil {
		t.Fatal(err)
	}
	if err := r.MovePanel("p", 99); err != nil {
		t.Fatal(err)
	}
	if got := r.PanelNames("p"); !slices.Equal(got, []string{"b", "a", ""}) {
		t.Fatalf("moved: %q", got)
	}
	if _, err := r.NewPanel("p", "c"); err != nil {
		t.Fatal(err)
	}
	if got := r.PanelNames("p"); !slices.Equal(got, []string{"b", "a", "", "c"}) {
		t.Fatalf("a new one goes last: %q", got)
	}
	if err := r.MovePanel("p/nope", 0); err == nil {
		t.Fatal("moving a missing panel should fail")
	}
}
