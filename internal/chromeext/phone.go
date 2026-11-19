package chromeext

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

// A page of the desktop App's browser is laid out at the desktop pane's size, which on a phone is a quarter-size
// picture (a 1500 px wide page on a 400 px screen). While the App's pane isn't showing the page (the window minimized,
// another tab, nobody at the desk), a remote viewer gets it laid out at its own size as a phone: a device-metrics
// override with mobile on (sites switch to their mobile layout) and touch events. The App's pane showing it again
// ends that (the desktop wins when both look), as does the last remote viewer leaving.
//
// A floating Browser pane (picture-in-picture) shows stills of the page shrunk into a small box. Laid out at that
// box's size, sites switched to their phone layout, which isn't what the person floated it for, so the pane picks a
// fixed size instead (Fit: a desktop, a tablet or a phone) and the stills are scaled down from it. That wins over a
// remote viewer's phone layout, and ends when the App's pane shows the page again (docked) or the pane says so.
//
// The shell says when its pane shows a page (the "shown" event, desktop/core/views.go). Headless pages don't need
// this: their window is resized to the viewer (Tab.resize).

type phoneLayout struct {
	mu      sync.Mutex
	desktop bool // the App's pane shows the page now
	viewers map[*View][2]int
	fit     *Fit // a floating pane's fixed size
	on      Fit  // the override set now; zero = none
}

// Fit is a page's laid-out size (CSS px) and whether it is laid out as a mobile device (sites' mobile layout, touch).
type Fit struct {
	W, H   int
	Mobile bool
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
	if on {
		p.fit = nil // docked again: the page takes the pane's size
	}
	p.mu.Unlock()
	t.applyPhone()
}

// setFit: a floating pane's fixed size for the page; nil = none.
func (t *Tab) setFit(f *Fit) {
	p := t.phone()
	p.mu.Lock()
	p.fit = f
	p.mu.Unlock()
	t.applyPhone()
}

// applyPhone sets or clears the override to match: a floating pane's Fit when it has one; else on while a remote
// viewer is there and the desktop isn't showing the page, at the newest viewer's size.
func (t *Tab) applyPhone() {
	p := t.phone()
	p.mu.Lock()
	var size [2]int
	for _, s := range p.viewers {
		size = s
	}
	var want Fit
	if p.fit != nil {
		want = *p.fit
	} else if !p.desktop && size[0] > 0 {
		want = Fit{size[0], size[1], true}
	}
	was := p.on
	p.on = want
	p.mu.Unlock()
	if want == was && want.W > 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	touch := func(on bool) {
		m := map[string]any{"enabled": on}
		if on {
			m["maxTouchPoints"] = 5
		}
		params, _ := json.Marshal(m)
		t.send(ctx, "Emulation.setTouchEmulationEnabled", params)
	}
	if want.W > 0 {
		params, _ := json.Marshal(map[string]any{"width": want.W, "height": want.H, "deviceScaleFactor": 0, "mobile": want.Mobile})
		t.send(ctx, "Emulation.setDeviceMetricsOverride", params)
		if want.Mobile != (was.W > 0 && was.Mobile) {
			touch(want.Mobile)
		}
		return
	}
	if was.W > 0 {
		t.send(ctx, "Emulation.clearDeviceMetricsOverride", nil)
		touch(false)
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

// SetFit lays tab id out at a fixed size while a pane floats it (nil = back to the pane's own size).
func (b *Bridge) SetFit(id int, f *Fit) error {
	b.mu.Lock()
	t := b.tabs[id]
	b.mu.Unlock()
	if t == nil {
		return errors.New("no such tab")
	}
	t.setFit(f)
	return nil
}
