package herdrimport

import (
	"encoding/json"
	"testing"
)

func TestCut(t *testing.T) {
	// 左边一个，右边上下两个
	r := map[string]rect{
		"a": {0, 0, 100, 50},
		"b": {101, 0, 60, 25},
		"c": {101, 26, 60, 24},
	}
	n := cut([]string{"a", "b", "c"}, r)
	if n == nil || n.Dir != "row" || n.A.Pane != "a" || n.B.Dir != "col" || n.B.A.Pane != "b" || n.B.B.Pane != "c" {
		t.Fatalf("%+v", n)
	}
}

func TestCleanLabel(t *testing.T) {
	for in, want := range map[string]string{"1.portai": "portai", "2.portai Ⅱ": "portai", "creght": "creght", "12.a.b": "a.b", "1": ""} {
		if got := cleanLabel(in); got != want {
			t.Errorf("%q → %q", in, got)
		}
	}
}

func TestRectJSON(t *testing.T) {
	var r rect
	_ = json.Unmarshal([]byte(`{"height":55,"width":104,"x":3,"y":0}`), &r)
	if r.W != 104 || r.H != 55 || r.X != 3 {
		t.Fatalf("%+v", r)
	}
}
