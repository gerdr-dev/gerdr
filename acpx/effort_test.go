package acpx

import "testing"

func TestEffortName(t *testing.T) {
	for v, want := range map[string]string{"medium": "Medium", "xhigh": "Extra high", "none": "Off", "ultra": "Ultra", "": ""} {
		if got := EffortName(v); got != want {
			t.Errorf("EffortName(%q) = %q, want %q", v, got, want)
		}
	}
}
