package chromeext

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/gerdr-dev/gerdr/internal/screen"
)

// An App page whose screencast went silent (the screen locked) is caught up on with screenshots after a change, until
// two in a row are the same; a screencast frame after the change stops it.
func TestViewCatchUp(t *testing.T) {
	b := New(nil)
	tab := newTab(8, "https://example.com/")
	tab.Native = true
	var mu sync.Mutex
	shots, pic := 0, "A"
	tab.send = func(_ context.Context, method string, _ json.RawMessage) (json.RawMessage, error) {
		mu.Lock()
		defer mu.Unlock()
		switch method {
		case "Page.getLayoutMetrics":
			return json.RawMessage(`{"cssVisualViewport":{"clientWidth":400,"clientHeight":780}}`), nil
		case "Page.captureScreenshot":
			shots++
			d, _ := json.Marshal(map[string][]byte{"data": []byte(pic)})
			return d, nil
		}
		return json.RawMessage(`{}`), nil
	}
	b.tabs[tab.ID] = tab
	v, err := b.View(tab.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	frame := func() string {
		v.mu.Lock()
		defer v.mu.Unlock()
		return string(v.frame)
	}
	// a change long enough ago that a live screencast would have answered
	ago := func() {
		v.mu.Lock()
		v.changedAt = v.changedAt.Add(-time.Second)
		v.stillAt = time.Time{}
		v.mu.Unlock()
	}
	v.frame = []byte("old")
	v.markChanged()
	v.catchUp()
	if frame() != "old" || shots != 0 {
		t.Fatalf("caught up before a screencast frame could come: %q, %d shots", frame(), shots)
	}
	ago()
	v.catchUp()
	if frame() != "A" {
		t.Fatalf("frame = %q, want the still", frame())
	}
	ago()
	v.catchUp() // the same picture: settled
	ago()
	v.catchUp()
	if shots != 2 {
		t.Errorf("shots = %d, want 2 (stop once two are the same)", shots)
	}
	// the screencast answered the next change: no still
	v.markChanged()
	ago()
	v.mu.Lock()
	v.castAt = time.Now()
	v.mu.Unlock()
	mu.Lock()
	pic = "B"
	mu.Unlock()
	v.catchUp()
	if shots != 2 || frame() != "A" {
		t.Errorf("a still over a live screencast: %d shots, frame %q", shots, frame())
	}
}

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
