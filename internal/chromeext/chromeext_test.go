package chromeext

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

func TestSiteMatch(t *testing.T) {
	for _, c := range []struct {
		pattern, url string
		want         bool
	}{
		{"https://www.doubao.com/*", "https://www.doubao.com/chat/1", true},
		{"https://www.doubao.com/*", "http://www.doubao.com/", false},
		{"https://*.example.com/*", "https://a.b.example.com/x", true},
		{"https://*.example.com/*", "https://example.com/", true},
		{"https://*.example.com/*", "https://badexample.com/", false},
	} {
		u, _ := url.Parse(c.url)
		if got := siteMatch(c.pattern, u); got != c.want {
			t.Errorf("siteMatch(%q, %q) = %v", c.pattern, c.url, got)
		}
	}
}

func TestTabLog(t *testing.T) {
	l := newTabLog()
	ev := func(method string, v any) {
		b, _ := json.Marshal(v)
		l.record(method, b)
	}
	ev("Runtime.consoleAPICalled", map[string]any{"type": "log", "args": []any{map[string]any{"type": "string", "value": "hi"}, map[string]any{"type": "number", "value": 42}}})
	ev("Runtime.exceptionThrown", map[string]any{"exceptionDetails": map[string]any{"text": "Uncaught", "exception": map[string]any{"description": "TypeError: x"}, "url": "https://a/b.js", "lineNumber": 3}})
	if got := l.consoleText(false, "", 0, false); !strings.Contains(got, "[log] hi 42") || !strings.Contains(got, "[exception] TypeError: x  (https://a/b.js:4)") {
		t.Fatalf("console:\n%s", got)
	}
	if got := l.consoleText(true, "", 0, true); strings.Contains(got, "hi 42") {
		t.Fatalf("errors only:\n%s", got)
	}
	if got := l.consoleText(false, "", 0, false); got != "(no console messages)" {
		t.Fatalf("after clear: %q", got)
	}

	ev("Network.requestWillBeSent", map[string]any{"requestId": "1", "type": "Fetch", "request": map[string]any{"method": "POST", "url": "https://a/api/chat"}})
	ev("Network.responseReceived", map[string]any{"requestId": "1", "response": map[string]any{"status": 200, "mimeType": "application/json"}})
	ev("Network.loadingFinished", map[string]any{"requestId": "1", "encodedDataLength": 10})
	ev("Network.requestWillBeSent", map[string]any{"requestId": "2", "type": "Image", "request": map[string]any{"method": "GET", "url": "https://a/x.png"}})
	ev("Network.loadingFailed", map[string]any{"requestId": "2", "errorText": "net::ERR_BLOCKED"})
	got := l.netText("", "", 0, false)
	if !strings.Contains(got, "1  POST 200  Fetch  https://a/api/chat application/json") || !strings.Contains(got, "FAILED net::ERR_BLOCKED") {
		t.Fatalf("network:\n%s", got)
	}
	if got := l.netText("chat", "", 0, false); strings.Contains(got, "x.png") {
		t.Fatalf("filter:\n%s", got)
	}

	ev("Page.javascriptDialogOpening", map[string]any{"type": "confirm", "message": "Sure?"})
	if d := l.openDialog(); d == nil || d.Message != "Sure?" {
		t.Fatalf("dialog: %+v", d)
	}
	ev("Page.javascriptDialogClosed", map[string]any{})
	if l.openDialog() != nil {
		t.Fatal("dialog still open")
	}
}

func TestModifierBits(t *testing.T) {
	if got := modifierBits([]string{"cmd", "shift"}); got != 12 {
		t.Fatalf("cmd+shift = %d", got)
	}
	if got := modifierBits([]string{"Alt", "ctrl"}); got != 3 {
		t.Fatalf("alt+ctrl = %d", got)
	}
}
