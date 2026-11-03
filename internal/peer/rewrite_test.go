package peer

import (
	"encoding/json"
	"strings"
	"testing"
)

// A sender that isn't on the host survives the round trip: marked on the way there, restored on the way back; a
// sender on the host gets its prefix.
func TestFromRoundTrip(t *testing.T) {
	b := RewriteBody("mini", []byte(`{"from":"c0ffee","near":"c0ffee"}`))
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	if m["from"] != "console~c0ffee" || m["near"] != nil {
		t.Fatalf("to the host: %s", b)
	}
	if b := RewriteBody("mini", []byte(`{"from":"mini~w1"}`)); !strings.Contains(string(b), `"from":"w1"`) {
		t.Fatalf("same-host sender: %s", b)
	}
	evs := RewriteEvents("mini", []byte(`[{"seq":1,"from":"console~c0ffee"},{"seq":2,"from":"w1"},{"seq":3,"blob":"/api/blobs/ab"}]`))
	if !strings.Contains(string(evs), `"from":"c0ffee"`) || !strings.Contains(string(evs), `"from":"mini~w1"`) || !strings.Contains(string(evs), `"/h/mini/api/blobs/ab"`) {
		t.Fatalf("back: %s", evs)
	}
}
