package hub

import (
	"testing"

	"github.com/gerdr-dev/gerdr/agents/native"
)

type fakeRoutes map[string]string

func (f fakeRoutes) Rewrite(agent, base string) string { return f[agent+" "+base] }

// Gerdr Agent providers on a routed address use the service's; others and the original config are left alone.
func TestRouteLLM(t *testing.T) {
	c := native.Config{Providers: []native.Provider{{ID: "a", BaseURL: "https://relay.example"}, {ID: "b", BaseURL: "https://api.other"}}}
	got := routeLLM(c, fakeRoutes{"agent https://relay.example": "http://127.0.0.1:7861"}, "agent")
	if got.Providers[0].BaseURL != "http://127.0.0.1:7861" || got.Providers[1].BaseURL != "https://api.other" {
		t.Fatalf("routed providers = %+v", got.Providers)
	}
	if c.Providers[0].BaseURL != "https://relay.example" {
		t.Fatalf("the caller's config was changed: %+v", c.Providers)
	}
	if routeLLM(c, fakeRoutes{"agent https://relay.example": "x"}, "other").Providers[0].BaseURL != "https://relay.example" {
		t.Fatal("another agent was routed")
	}
	if routeLLM(c, nil, "agent").Providers[0].BaseURL != "https://relay.example" {
		t.Fatal("nil routes changed something")
	}
}
