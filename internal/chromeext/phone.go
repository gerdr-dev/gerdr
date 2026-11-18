package chromeext

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

// A page of the desktop App's browser is laid out at the desktop pane's size, which on a phone is a quarter-size
// picture (a 1500 px wide page on a 400 px screen). While the App's pane isn't showing the page (the window minimized,
// another tab, nobody at the desk), a remote viewer gets it laid out at its own size as a phone: a device-metrics
// override with mobile on (sites switch to their mobile layout) and touch events. The App's pane showing it again
// ends that (the desktop wins when both look), as does the last remote viewer leaving.
//
// The shell says when its pane shows a page (the "shown" event, desktop/core/views.go). Headless pages don't need
// this: their window is resized to the viewer (Tab.resize).

type phoneLayout struct {
	mu      sync.Mutex
	desktop bool // the App's pane shows the page now
	viewers map[*View][2]int
	on      bool // the override is set
}

func (t *Tab) phone() *phoneLayout {
	t.phoneOnce.Do(func() { t.phoneL = &phoneLayout{viewers: map[*View][2]int{}} })
	return t.phoneL
}

// remoteSize: a remote viewer's box (CSS px); 0 x 0 = it left.
func (t *Tab) remoteSize(v *View, w, h int) {
	p := t.phone()
	p.mu.Lock()
	if w > 0 && h > 0 {
		p.viewers[v] = [2]int{w, h}
	} else {
		delete(p.viewers, v)
	}
	p.mu.Unlock()
	t.applyPhone()
}

func (t *Tab) desktopShown(on bool) {
	p := t.phone()
	p.mu.Lock()
	p.desktop = on
	p.mu.Unlock()
	t.applyPhone()
}

// applyPhone sets or clears the override to match: on while a remote viewer is there and the desktop isn't showing
// the page, at the newest viewer's size.
func (t *Tab) applyPhone() {
	p := t.phone()
	p.mu.Lock()
	var size [2]int
	for _, s := range p.viewers {
		size = s
	}
	want := !p.desktop && size[0] > 0
	was := p.on
	p.on = want
	p.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if want {
		params, _ := json.Marshal(map[string]any{"width": size[0], "height": size[1], "deviceScaleFactor": 0, "mobile": true})
		t.send(ctx, "Emulation.setDeviceMetricsOverride", params)
		if !was {
			touch, _ := json.Marshal(map[string]any{"enabled": true, "maxTouchPoints": 5})
			t.send(ctx, "Emulation.setTouchEmulationEnabled", touch)
		}
		return
	}
	if was {
		t.send(ctx, "Emulation.clearDeviceMetricsOverride", nil)
		off, _ := json.Marshal(map[string]any{"enabled": false})
		t.send(ctx, "Emulation.setTouchEmulationEnabled", off)
	}
}

// DesktopShown: the desktop App's pane shows (or stopped showing) tab id as the real window.
func (b *Bridge) DesktopShown(id int, on bool) {
	b.mu.Lock()
	t := b.tabs[id]
	b.mu.Unlock()
	if t != nil && t.Native {
		go t.desktopShown(on)
	}
}
