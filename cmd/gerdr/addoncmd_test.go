package main

import (
	"encoding/json"
	"testing"

	"github.com/gerdr-dev/gerdr/internal/addons"
)

func TestParseSetting(t *testing.T) {
	var sel addons.SettingSpec
	_ = json.Unmarshal([]byte(`{"key":"k","type":"select","options":[{"value":5},{"value":"big","label":"Big"}]}`), &sel)
	cases := []struct {
		spec addons.SettingSpec
		raw  string
		want any
		bad  bool
	}{
		{addons.SettingSpec{Key: "n", Type: "number"}, "2.5", 2.5, false},
		{addons.SettingSpec{Key: "n", Type: "number"}, "x", nil, true},
		{addons.SettingSpec{Key: "b", Type: "boolean"}, "off", false, false},
		{addons.SettingSpec{Key: "b", Type: "boolean"}, "maybe", nil, true},
		{sel, "5", float64(5), false}, // the option's own value (a JSON number), not the text
		{sel, "big", "big", false},
		{sel, "small", nil, true},
		{addons.SettingSpec{Key: "c", Type: "color"}, "#ABCDEF", "#abcdef", false},
		{addons.SettingSpec{Key: "c", Type: "color"}, "red", nil, true},
		{addons.SettingSpec{Key: "s", Type: "secret"}, "tok=1", "tok=1", false},
	}
	for _, c := range cases {
		got, err := parseSetting(c.spec, c.raw)
		if (err != nil) != c.bad || (!c.bad && got != c.want) {
			t.Errorf("%s %q: got %v, %v", c.spec.Type, c.raw, got, err)
		}
	}
}
