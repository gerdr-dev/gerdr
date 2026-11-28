package hub

import (
	"encoding/json"
	"testing"

	"github.com/coder/acp-go-sdk"
)

func TestACPUsageUpdateSetsContext(t *testing.T) {
	h := newHubWith(t, hubDir(t), "s1")
	s, err := h.Session("s1")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		wire string
		want int
	}{
		{`{"sessionUpdate":"usage_update","used":730,"size":1000}`, 730},
		{`{"sessionUpdate":"usage_update","used":180,"size":1000}`, 180},
		{`{"sessionUpdate":"usage_update","used":0,"size":0}`, 180},
		{`{"sessionUpdate":"usage_update","used":0,"size":1000}`, 0},
	} {
		var update acp.SessionUpdate
		if err := json.Unmarshal([]byte(tc.wire), &update); err != nil {
			t.Fatal(err)
		}
		s.onUpdate(update)
		sum := s.Summary()
		if sum.CtxTokens != tc.want || sum.CtxWindow != 1000 {
			t.Fatalf("after %s: got %d/%d, want %d/1000", tc.wire, sum.CtxTokens, sum.CtxWindow, tc.want)
		}
	}
}
