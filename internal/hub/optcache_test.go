package hub

import (
	"reflect"
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

// A Summary carries the dropdown lists as a key shared by sessions with the same lists, and the current values on
// their own: a session not attached yet takes its record's values over the cached ones, an attached one its agent's.
func TestSummaryOptions(t *testing.T) {
	h, err := New(Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	choices := acp.SessionConfigSelectOptionsUngrouped{{Name: "A", Value: "a"}, {Name: "B", Value: "b"}}
	opt := func(cur string) []acp.SessionConfigOption {
		return []acp.SessionConfigOption{{Select: &acp.SessionConfigOptionSelect{Id: "model", Name: "Model", Type: "select", CurrentValue: acp.SessionConfigValueId(cur),
			Options: acp.SessionConfigSelectOptions{Ungrouped: &choices}}}}
	}
	modes := []acp.SessionMode{{Id: "default", Name: "Ask"}, {Id: "plan", Name: "Read only"}}
	h.opts.set("claude", &acp.SessionModeState{CurrentModeId: "default", AvailableModes: modes}, opt("a"))

	idle1 := &Session{h: h, rec: Record{ID: "i1", Agent: "claude", Mode: "plan", Config: map[string]string{"model": "b"}}}
	idle2 := &Session{h: h, rec: Record{ID: "i2", Agent: "claude", Config: map[string]string{"model": ""}}}
	live := &Session{h: h, rec: Record{ID: "l1", Agent: "claude", Config: map[string]string{"model": "a"}},
		modes: &acp.SessionModeState{CurrentModeId: "plan", AvailableModes: modes}, config: opt("b")}
	s1, s2, s3 := idle1.Summary(), idle2.Summary(), live.Summary()

	if s1.OptsKey == "" || s1.OptsKey != s2.OptsKey || s3.OptsKey != s1.OptsKey {
		t.Fatalf("same lists, different keys: %q %q %q", s1.OptsKey, s2.OptsKey, s3.OptsKey)
	}
	if s1.ModeID != "plan" || s1.OptValues["model"] != "b" {
		t.Fatalf("idle session ignores its record: %q %v", s1.ModeID, s1.OptValues)
	}
	if s2.ModeID != "default" || s2.OptValues["model"] != "a" {
		t.Fatalf("an empty record value should keep the cached one: %q %v", s2.ModeID, s2.OptValues)
	}
	if s3.ModeID != "plan" || s3.OptValues["model"] != "b" {
		t.Fatalf("attached session should report its agent's values: %q %v", s3.ModeID, s3.OptValues)
	}
	set, ok := h.OptionSet(s1.OptsKey)
	if !ok || len(set.Modes) != 2 || len(set.Config) != 1 || set.Config[0].Select.CurrentValue != "" {
		t.Fatalf("option set: %v %+v", ok, set)
	}
	if live.config[0].Select.CurrentValue != "b" {
		t.Fatal("making the set cleared the session's own current value")
	}
	// Unchanged values: the very same map (no allocation per list); changed: a new one with the new value
	again := live.Summary()
	if reflect.ValueOf(again.OptValues).Pointer() != reflect.ValueOf(s3.OptValues).Pointer() {
		t.Fatal("unchanged values built a new map")
	}
	live.config[0].Select.CurrentValue = "a"
	if v := live.Summary().OptValues["model"]; v != "a" || s3.OptValues["model"] != "b" {
		t.Fatalf("a changed value: got %v, and the old map must stay as it was: %v", v, s3.OptValues["model"])
	}
	if n := testing.AllocsPerRun(100, func() { idle1.Summary() }); n > 40 {
		t.Logf("Summary allocations: %v", n)
	}
}
