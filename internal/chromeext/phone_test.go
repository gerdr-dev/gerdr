package chromeext

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// An App page is laid out as a phone for a remote viewer only while the App's pane doesn't show it.
func TestPhoneLayout(t *testing.T) {
	tab := newTab(2000001, "https://example.com/")
	tab.Native = true
	var mu sync.Mutex
	var calls []string
	tab.send = func(_ context.Context, method string, _ json.RawMessage) (json.RawMessage, error) {
		mu.Lock()
		calls = append(calls, method)
		mu.Unlock()
		return json.RawMessage(`{}`), nil
	}
	take := func() []string {
		mu.Lock()
		defer mu.Unlock()
		out := calls
		calls = nil
		return out
	}
	v := &View{t: tab}
	tab.remoteSize(v, 390, 760) // a phone looks, nobody at the desk
	if got := take(); len(got) != 2 || got[0] != "Emulation.setDeviceMetricsOverride" || got[1] != "Emulation.setTouchEmulationEnabled" {
		t.Fatalf("phone alone: %v", got)
	}
	tab.desktopShown(true) // the App's pane shows it: the desktop wins
	if got := take(); len(got) != 2 || got[0] != "Emulation.clearDeviceMetricsOverride" {
		t.Fatalf("desktop shown: %v", got)
	}
	tab.remoteSize(v, 390, 700) // the phone resizes meanwhile: nothing while the desktop shows it
	if got := take(); len(got) != 0 {
		t.Fatalf("desktop showing, phone resized: %v", got)
	}
	tab.desktopShown(false) // the pane hidden again: the phone's layout back
	if got := take(); len(got) != 2 || got[0] != "Emulation.setDeviceMetricsOverride" {
		t.Fatalf("desktop hidden with a phone looking: %v", got)
	}
	tab.remoteSize(v, 0, 0) // the phone leaves
	if got := take(); len(got) != 2 || got[0] != "Emulation.clearDeviceMetricsOverride" {
		t.Fatalf("phone left: %v", got)
	}
}

// A floating pane's fixed size gives way to a phone's layout while one looks, and ends when the App's pane shows the
// page again.
func TestFloatFit(t *testing.T) {
	tab := newTab(2000002, "https://example.com/")
	tab.Native = true
	var mu sync.Mutex
	var calls []string
	tab.send = func(_ context.Context, method string, p json.RawMessage) (json.RawMessage, error) {
		mu.Lock()
		calls = append(calls, method+" "+string(p))
		mu.Unlock()
		return json.RawMessage(`{}`), nil
	}
	take := func() []string {
		mu.Lock()
		defer mu.Unlock()
		out := calls
		calls = nil
		return out
	}
	tab.desktopShown(false) // floated: the page is parked
	take()
	tab.setFit(&Fit{W: 1280, H: 800, Mobile: false})
	if got := take(); len(got) != 1 || !strings.Contains(got[0], `"width":1280`) || !strings.Contains(got[0], `"mobile":false`) {
		t.Fatalf("fit set: %v", got)
	}
	v := &View{t: tab}
	tab.remoteSize(v, 390, 760) // a phone looks: its own layout, with touch
	if got := take(); len(got) != 2 || !strings.Contains(got[0], `"width":390`) || !strings.Contains(got[1], `"enabled":true`) {
		t.Fatalf("phone over a fit: %v", got)
	}
	tab.remoteSize(v, 0, 0) // it leaves: the fit again, touch off
	if got := take(); len(got) != 2 || !strings.Contains(got[0], `"width":1280`) || !strings.Contains(got[1], `"enabled":false`) {
		t.Fatalf("phone left, fit back: %v", got)
	}
	tab.setFit(&Fit{W: 390, H: 844, Mobile: true}) // phone size picked: mobile, with touch
	if got := take(); len(got) != 2 || !strings.Contains(got[0], `"mobile":true`) || !strings.Contains(got[1], `"enabled":true`) {
		t.Fatalf("mobile fit: %v", got)
	}
	tab.desktopShown(true) // docked and shown: the pane's own size, no override
	if got := take(); len(got) != 2 || !strings.HasPrefix(got[0], "Emulation.clearDeviceMetricsOverride") {
		t.Fatalf("docked: %v", got)
	}
}

// One screencast feeds every viewer of a page: it runs at the widest one's width, and narrows when that one leaves.
func TestCastWidest(t *testing.T) {
	tab := newTab(2000003, "https://example.com/")
	var mu sync.Mutex
	var starts []string
	tab.send = func(_ context.Context, method string, p json.RawMessage) (json.RawMessage, error) {
		if method == "Page.startScreencast" {
			mu.Lock()
			starts = append(starts, string(p))
			mu.Unlock()
		}
		return json.RawMessage(`{}`), nil
	}
	last := func() string {
		mu.Lock()
		defer mu.Unlock()
		if len(starts) == 0 {
			return ""
		}
		return starts[len(starts)-1]
	}
	ctx := context.Background()
	pane, phone := &View{t: tab}, &View{t: tab}
	tab.cast.acquire(ctx, pane, 960)
	tab.cast.acquire(ctx, phone, 1200)
	if !strings.Contains(last(), `"maxWidth":1200`) {
		t.Fatalf("not the widest: %s", last())
	}
	if changed, _ := tab.cast.width(ctx, pane, 800); changed {
		t.Fatal("a narrower viewer's resize restarted the cast")
	}
	tab.cast.release(phone)
	if !strings.Contains(last(), `"maxWidth":800`) {
		t.Fatalf("not narrowed to the one left: %s", last())
	}
}

// A phone or tablet fit is that device to sites (user agent, client hints), reloaded so the server picks its page;
// back to the browser's own (and reloaded) when the fit ends.
func TestFitDevice(t *testing.T) {
	tab := newTab(2000004, "https://example.com/")
	tab.Native = true
	var mu sync.Mutex
	var calls []string
	tab.send = func(_ context.Context, method string, p json.RawMessage) (json.RawMessage, error) {
		mu.Lock()
		calls = append(calls, method+" "+string(p))
		mu.Unlock()
		if method == "Runtime.evaluate" {
			return json.RawMessage(`{"result":{"type":"string","value":"Mozilla/5.0 (Macintosh) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.7339.80 Safari/537.36"}}`), nil
		}
		return json.RawMessage(`{}`), nil
	}
	find := func(prefix string) []string {
		mu.Lock()
		defer mu.Unlock()
		var out []string
		for _, c := range calls {
			if strings.HasPrefix(c, prefix) {
				out = append(out, c)
			}
		}
		calls = nil
		return out
	}
	tab.desktopShown(false)
	find("")
	tab.setFit(&Fit{W: 390, H: 844, Mobile: true, Device: "phone"})
	all := find("")
	var ua, reload string
	for _, c := range all {
		if strings.HasPrefix(c, "Emulation.setUserAgentOverride") {
			ua = c
		}
		if strings.HasPrefix(c, "Page.reload") {
			reload = c
		}
	}
	if !strings.Contains(ua, "Android 14; Pixel 8") || !strings.Contains(ua, "Chrome/140.0.7339.80 Mobile") || !strings.Contains(ua, `"mobile":true`) || reload == "" {
		t.Fatalf("phone device: %v", all)
	}
	tab.setFit(&Fit{W: 390, H: 844, Mobile: true, Device: "phone"}) // the same device again: no reload
	if got := find("Page.reload"); len(got) != 0 {
		t.Fatalf("reloaded for the same device: %v", got)
	}
	tab.desktopShown(true) // docked: the browser's own user agent, reloaded
	all = find("")
	if !strings.Contains(strings.Join(all, "\n"), `Emulation.setUserAgentOverride {"userAgent":""}`) || !strings.Contains(strings.Join(all, "\n"), "Page.reload") {
		t.Fatalf("docked: %v", all)
	}
}

// A size picked for the page in its pane's menu (Pin) wins over a phone's own layout: the person on the phone asked
// for the desktop page.
func TestPinnedFit(t *testing.T) {
	tab := newTab(2000003, "https://example.com/")
	tab.Native = true
	var mu sync.Mutex
	var sizes []string
	tab.send = func(_ context.Context, method string, p json.RawMessage) (json.RawMessage, error) {
		if method == "Emulation.setDeviceMetricsOverride" {
			var m struct{ Width, Height int }
			json.Unmarshal(p, &m)
			mu.Lock()
			sizes = append(sizes, fmt.Sprintf("%dx%d", m.Width, m.Height))
			mu.Unlock()
		}
		return json.RawMessage(`{}`), nil
	}
	last := func() string {
		mu.Lock()
		defer mu.Unlock()
		if len(sizes) == 0 {
			return ""
		}
		return sizes[len(sizes)-1]
	}
	v := &View{t: tab}
	tab.remoteSize(v, 390, 760)
	if got := last(); got != "390x760" {
		t.Fatalf("auto on a phone: %s", got)
	}
	tab.setFit(&Fit{W: 1280, H: 800, Pin: true})
	if got := last(); got != "1280x800" {
		t.Fatalf("pinned pc with a phone looking: %s", got)
	}
	tab.remoteSize(v, 390, 700) // the phone's keyboard: the pinned size stays
	if got := last(); got != "1280x800" {
		t.Fatalf("phone resized: %s", got)
	}
	tab.setFit(nil) // back to auto: the phone's layout
	if got := last(); got != "390x700" {
		t.Fatalf("auto again: %s", got)
	}
}

// A page playing a phone gives each cross-site iframe the same device as it attaches (paused until then), its nested
// ones too, and always lets it run.
func TestFrameDevice(t *testing.T) {
	tab := newTab(2000004, "https://example.com/")
	tab.Native = true
	var mu sync.Mutex
	var sent []string // "method" or "sendMessageToTarget sid→inner method"
	tab.send = func(_ context.Context, method string, p json.RawMessage) (json.RawMessage, error) {
		mu.Lock()
		defer mu.Unlock()
		if method != "Target.sendMessageToTarget" {
			sent = append(sent, method)
			return json.RawMessage(`{}`), nil
		}
		// unwrap to the innermost command, recording the sessions on the way
		var route []string
		for {
			var m struct {
				SessionID string `json:"sessionId"`
				Message   string `json:"message"`
			}
			json.Unmarshal(p, &m)
			route = append(route, m.SessionID)
			var in struct {
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			json.Unmarshal([]byte(m.Message), &in)
			if in.Method != "Target.sendMessageToTarget" {
				sent = append(sent, strings.Join(route, "/")+" "+in.Method)
				break
			}
			p = in.Params
		}
		return json.RawMessage(`{}`), nil
	}
	take := func() []string {
		time.Sleep(50 * time.Millisecond) // set-up runs in a goroutine
		mu.Lock()
		defer mu.Unlock()
		out := sent
		sent = nil
		return out
	}
	tab.applyDevice("", "phone")
	if got := strings.Join(take(), ","); !strings.Contains(got, "Target.setAutoAttach") || !strings.HasSuffix(got, "Page.reload") {
		t.Fatalf("phone picked: %s", got)
	}
	tab.event("Target.attachedToTarget", json.RawMessage(`{"sessionId":"A","waitingForDebugger":true,"targetInfo":{"type":"iframe"}}`))
	want := "A Emulation.setUserAgentOverride,A Emulation.setTouchEmulationEnabled,A Target.setAutoAttach,A Runtime.runIfWaitingForDebugger"
	if got := strings.Join(take(), ","); got != want {
		t.Fatalf("iframe set up:\n got %s\nwant %s", got, want)
	}
	// an iframe inside that one, reported through it
	inner, _ := json.Marshal(map[string]any{"method": "Target.attachedToTarget", "params": map[string]any{"sessionId": "B", "waitingForDebugger": true, "targetInfo": map[string]any{"type": "iframe"}}})
	ev, _ := json.Marshal(map[string]any{"sessionId": "A", "message": string(inner)})
	tab.event("Target.receivedMessageFromTarget", ev)
	if got := take(); len(got) != 4 || got[0] != "A/B Emulation.setUserAgentOverride" || got[3] != "A/B Runtime.runIfWaitingForDebugger" {
		t.Fatalf("nested iframe: %v", got)
	}
	// a worker is only let go
	tab.event("Target.attachedToTarget", json.RawMessage(`{"sessionId":"W","waitingForDebugger":true,"targetInfo":{"type":"worker"}}`))
	if got := strings.Join(take(), ","); got != "W Runtime.runIfWaitingForDebugger" {
		t.Fatalf("worker: %s", got)
	}
	// back to the browser's own: no more attaching, and attach events are ignored
	tab.applyDevice("phone", "")
	take()
	tab.event("Target.attachedToTarget", json.RawMessage(`{"sessionId":"C","waitingForDebugger":false,"targetInfo":{"type":"iframe"}}`))
	if got := take(); len(got) != 0 {
		t.Fatalf("set up after the device ended: %v", got)
	}
}
