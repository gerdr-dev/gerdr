package hub

import (
	"testing"

	"github.com/coder/acp-go-sdk"
)

// get hands out copies: a session setting its own current values must not change what the next session gets, and
// the session's own state handed to set must not alias the cache.
func TestOptCacheGetCopies(t *testing.T) {
	c := newOptCache(t.TempDir())
	modes := &acp.SessionModeState{CurrentModeId: "default", AvailableModes: []acp.SessionMode{{Id: "default", Name: "Default"}}}
	choices := acp.SessionConfigSelectOptionsUngrouped{{Name: "A", Value: "a"}, {Name: "B", Value: "b"}}
	cfg := []acp.SessionConfigOption{{Select: &acp.SessionConfigOptionSelect{Id: "model", Name: "Model", Type: "select", CurrentValue: "a",
		Options: acp.SessionConfigSelectOptions{Ungrouped: &choices}}}}
	c.set("claude", modes, cfg)
	modes.CurrentModeId = "plan" // the session moves on
	cfg[0].Select.CurrentValue = "b"

	o, ok := c.get("claude")
	if !ok || o.Modes.CurrentModeId != "default" || o.Config[0].Select.CurrentValue != "a" {
		t.Fatalf("cache follows the session's own state: %+v", o)
	}
	o.Modes.CurrentModeId = "x"
	o.Config[0].Select.CurrentValue = "y"
	o2, _ := c.get("claude")
	if o2.Modes.CurrentModeId != "default" || o2.Config[0].Select.CurrentValue != "a" {
		t.Fatalf("a caller's change leaked into the cache: %+v", o2)
	}
	if _, ok := newOptCache(c.path[:len(c.path)-len("/options.json")]).get("claude"); !ok {
		t.Fatal("not read back from disk")
	}
}
