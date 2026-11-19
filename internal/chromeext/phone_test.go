package chromeext

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
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

// A floating pane's fixed size wins over a phone's layout and ends when the App's pane shows the page again.
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
	tab.setFit(&Fit{1280, 800, false})
	if got := take(); len(got) != 1 || !strings.Contains(got[0], `"width":1280`) || !strings.Contains(got[0], `"mobile":false`) {
		t.Fatalf("fit set: %v", got)
	}
	v := &View{t: tab}
	tab.remoteSize(v, 390, 760) // a phone looks: the fit stays
	if got := take(); len(got) != 0 {
		t.Fatalf("phone over a fit: %v", got)
	}
	tab.setFit(&Fit{390, 844, true}) // phone size picked: mobile, with touch
	if got := take(); len(got) != 2 || !strings.Contains(got[0], `"mobile":true`) || !strings.Contains(got[1], `"enabled":true`) {
		t.Fatalf("mobile fit: %v", got)
	}
	tab.desktopShown(true) // docked and shown: the pane's own size, no override
	if got := take(); len(got) != 2 || !strings.HasPrefix(got[0], "Emulation.clearDeviceMetricsOverride") {
		t.Fatalf("docked: %v", got)
	}
}
