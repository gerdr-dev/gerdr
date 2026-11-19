package server

import (
	"encoding/json"
	"testing"
)

// A Browser pane's page closes with the pane, unless another pane shows it, its pane only switched pages, or a live
// agent opened it.
func TestBrowserPanesClosePages(t *testing.T) {
	lay := func(panes string) json.RawMessage {
		return json.RawMessage(`{"workspaces":[{"id":"w","tabs":[{"id":"t","root":` + panes + `}]}]}`)
	}
	split := func(a, b string) string { return `{"type":"split","id":"x","a":` + a + `,"b":` + b + `}` }
	br := func(id string, page int) string {
		return `{"type":"pane","id":"` + id + `","browser":{"project":"default","tab":` + itoa(page) + `}}`
	}
	chat := `{"type":"pane","id":"c","sessionId":"S"}`
	var bp browserPanes
	alive := map[string]bool{"A": true}
	is := func(s string) bool { return alive[s] }
	if got := bp.seen(lay(split(chat, split(br("p1", 1), br("p2", 2)))), is); got != nil {
		t.Fatalf("first layout closed %v", got)
	}
	// p1 switched to page 3: page 1 stays (its pane is still there)
	if got := bp.seen(lay(split(chat, split(br("p1", 3), br("p2", 2)))), is); got != nil {
		t.Fatalf("a page switch closed %v", got)
	}
	// p2 closed: page 2 closes
	if got := bp.seen(lay(split(chat, br("p1", 3))), is); len(got) != 1 || got[0] != 2 {
		t.Fatalf("closing p2: %v", got)
	}
	// p1 closed, but agent A (alive) opened page 3: kept (it stays in the page list)
	bp.opener = map[int]string{3: "A"}
	if got := bp.seen(lay(chat), is); got != nil {
		t.Fatalf("closed a live agent's page: %v", got)
	}
}

