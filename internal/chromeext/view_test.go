package chromeext

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/gerdr-dev/gerdr/internal/screen"
)

// The person's mouse in the Browser pane goes quiet (no extension cursor, no glide); an agent's click doesn't.
func TestViewMouseQuiet(t *testing.T) {
	b := New(nil)
	tab := newTab(7, "https://example.com/")
	var mu sync.Mutex
	calls := map[string][]string{} // "loud" / "quiet" → methods
	rec := func(kind string) func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		return func(_ context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
			mu.Lock()
			calls[kind] = append(calls[kind], method)
			mu.Unlock()
			if method == "Runtime.evaluate" {
				return json.RawMessage(`{"result":{"value":{"x":10,"y":10,"w":20,"h":20,"desc":"button","hit":true}}}`), nil
			}
			return json.RawMessage(`{}`), nil
		}
	}
	tab.send, tab.sendQuiet = rec("loud"), rec("quiet")
	b.tabs[tab.ID] = tab
	v, err := b.View(tab.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	for _, in := range []screen.Input{{Kind: "move", X: 0.5, Y: 0.5}, {Kind: "down", X: 0.5, Y: 0.5}, {Kind: "up", X: 0.5, Y: 0.5}, {Kind: "click", X: 0.2, Y: 0.2}} {
		if err := v.Do(in); err != nil {
			t.Fatal(in.Kind, err)
		}
	}
	mu.Lock()
	for _, m := range calls["loud"] {
		if m == "Input.dispatchMouseEvent" {
			t.Errorf("the pane's mouse went loud: %v", calls["loud"])
		}
	}
	if n := len(calls["quiet"]); n != 5 { // move, down, up, click = press + release
		t.Errorf("quiet calls = %v, want 5 mouse events", calls["quiet"])
	}
	calls = map[string][]string{}
	mu.Unlock()

	tab.mouse(context.Background(), "mousePressed", 1, 1, "left", 1, 0)
	mu.Lock()
	defer mu.Unlock()
	if len(calls["quiet"]) != 0 || len(calls["loud"]) != 1 {
		t.Errorf("an agent's mouse event must show the cursor: %v", calls)
	}
}
