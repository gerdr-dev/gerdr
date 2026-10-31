package hub

import (
	"testing"

	"github.com/coder/acp-go-sdk"
)

// Equal lists get one key whichever slice they come in; a replaced list gets a new one.
func TestCmdSets(t *testing.T) {
	h := &Hub{}
	c := &h.cmdSets
	a := []acp.AvailableCommand{{Name: "review"}, {Name: "init"}}
	b := []acp.AvailableCommand{{Name: "review"}, {Name: "init"}}
	ka, kb := c.key(a), c.key(b)
	if ka == "" || ka != kb {
		t.Fatalf("equal lists should share a key: %q %q", ka, kb)
	}
	if c.key(a) != ka {
		t.Fatal("same slice twice should give the same key")
	}
	if k := c.key([]acp.AvailableCommand{{Name: "review"}}); k == ka || k == "" {
		t.Fatalf("a different list needs its own key, got %q", k)
	}
	if c.key(nil) != "" {
		t.Fatal("no commands, no key")
	}
	if got := h.CommandSet(ka); len(got) != 2 || got[0].Name != "review" {
		t.Fatalf("CommandSet(%q) = %v", ka, got)
	}
}
