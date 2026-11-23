package hub

import (
	"testing"

	"github.com/coder/acp-go-sdk"
)

// An agent reporting its modes both ways (agy acp) shows them once, as the modes list.
func TestWithoutModeOption(t *testing.T) {
	cat := func(c acp.SessionConfigOptionCategory) *acp.SessionConfigOptionCategory { return &c }
	sel := func(id string, c acp.SessionConfigOptionCategory) acp.SessionConfigOption {
		return acp.SessionConfigOption{Select: &acp.SessionConfigOptionSelect{Id: acp.SessionConfigId(id), Category: cat(c)}}
	}
	config := []acp.SessionConfigOption{sel("model", acp.SessionConfigOptionCategoryModel), sel("mode", acp.SessionConfigOptionCategoryMode), sel("effort", acp.SessionConfigOptionCategoryThoughtLevel)}
	modes := []acp.SessionMode{{Id: "default", Name: "Default"}}

	got := withoutModeOption(modes, config)
	if len(got) != 2 || got[0].Select.Id != "model" || got[1].Select.Id != "effort" {
		t.Fatalf("got %+v", got)
	}
	if len(config) != 3 || config[1].Select.Id != "mode" {
		t.Fatal("the agent's own list was changed")
	}
	// No modes list: the config option is the only way to pick a mode, keep it
	if got := withoutModeOption(nil, config); len(got) != 3 {
		t.Fatalf("kept %d", len(got))
	}
}
