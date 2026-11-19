package chromeext

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
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
// fixed size instead (Fit: a desktop, a tablet or a phone) and its pictures are scaled down from it. A remote viewer's
// phone layout wins over it while one looks: at the fit's size the page was taller than the phone's pane (its keyboard
// up), and the bottom of the page couldn't be reached (user report). It ends when the App's pane shows the page
// again (docked) or the pane says so.
//
// The shell says when its pane shows a page (the "shown" event, desktop/core/views.go). Headless pages don't need
// this: their window is resized to the viewer (Tab.resize).

type phoneLayout struct {
	mu      sync.Mutex
	desktop bool // the App's pane shows the page now
	viewers map[*View][2]int
	fit     *Fit   // a floating pane's fixed size
	engine  string // the browser's Chrome version (engineVersion)
	on      Fit    // the override set now; zero = none
}

// Fit is a page's laid-out size (CSS px) and whether it is laid out as a mobile device (sites' mobile layout, touch).
// Device "phone" / "tablet" also makes the page that device to the sites (fitUA): the size alone left a site that
// tells devices apart by the user agent sending its desktop page (asked for).
type Fit struct {
	W, H   int
	Mobile bool
	Device string `json:"-"` // "", "phone", "tablet"
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
	was := deviceOf(p.fit)
	if on {
		p.fit = nil // docked again: the page takes the pane's size, and is the browser it is again
	}
	p.mu.Unlock()
	t.applyPhone()
	if on {
		t.applyDevice(was, "")
	}
}

// setFit: a floating pane's fixed size for the page; nil = none.
func (t *Tab) setFit(f *Fit) {
	p := t.phone()
	p.mu.Lock()
	was := deviceOf(p.fit)
	p.fit = f
	p.mu.Unlock()
	t.applyPhone()
	t.applyDevice(was, deviceOf(f))
}

func deviceOf(f *Fit) string {
	if f == nil {
		return ""
	}
	return f.Device
}

// applyDevice: the page's user agent (and its client hints) for device, then a reload when that changed, as the sites
// that pick a page by it decide on the request (a phone picked showed the same desktop page until reloaded).
func (t *Tab) applyDevice(was, device string) {
	if was == device {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ua := map[string]any{"userAgent": ""} // "" = the browser's own again
	if device != "" {
		ua = fitUA(device, t.engineVersion(ctx))
	}
	params, _ := json.Marshal(ua)
	t.send(ctx, "Emulation.setUserAgentOverride", params)
	t.send(ctx, "Page.reload", nil)
}

// engineVersion: the browser's Chrome version ("131.0.6778.85"), read once from the page before any override, for a
// device's user agent to name the engine it really is.
func (t *Tab) engineVersion(ctx context.Context) string {
	p := t.phone()
	p.mu.Lock()
	v := p.engine
	p.mu.Unlock()
	if v != "" {
		return v
	}
	v = "131.0.0.0"
	if raw, err := t.eval(ctx, "navigator.userAgent"); err == nil {
		var ua string
		if json.Unmarshal(raw, &ua) == nil {
			if m := chromeVersion.FindStringSubmatch(ua); m != nil {
				v = m[1]
			}
		}
	}
	p.mu.Lock()
	p.engine = v
	p.mu.Unlock()
	return v
}

var chromeVersion = regexp.MustCompile(`Chrome/([\d.]+)`)

// fitUA: Chrome on an Android phone or tablet (the engine it is, so what sites detect matches what they get): the
// user agent, its platform and the client hints sites read as sec-ch-ua-mobile / -platform.
func fitUA(device, version string) map[string]any {
	major := strings.SplitN(version, ".", 2)[0]
	model, mobile, tail := "Pixel 8", true, " Mobile Safari/537.36"
	if device == "tablet" {
		model, mobile, tail = "SM-X710", false, " Safari/537.36"
	}
	brands := []map[string]string{{"brand": "Chromium", "version": major}, {"brand": "Google Chrome", "version": major}, {"brand": "Not?A_Brand", "version": "99"}}
	return map[string]any{
		"userAgent": "Mozilla/5.0 (Linux; Android 14; " + model + ") AppleWebKit/537.36 (KHTML, like Gecko) Chrome/" + version + tail,
		"platform":  "Linux armv8l",
		"userAgentMetadata": map[string]any{
			"brands": brands, "fullVersion": version, "platform": "Android", "platformVersion": "14.0.0",
			"architecture": "", "model": model, "mobile": mobile,
		},
	}
}

// applyPhone sets or clears the override to match: a phone layout while a remote viewer is there and the desktop
// isn't showing the page, at the newest viewer's size; else a floating pane's Fit when it has one.
func (t *Tab) applyPhone() {
	p := t.phone()
	p.mu.Lock()
	var size [2]int
	for _, s := range p.viewers {
		size = s
	}
	var want Fit
	if !p.desktop && size[0] > 0 {
		want = Fit{W: size[0], H: size[1], Mobile: true}
	} else if p.fit != nil {
		want = *p.fit
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
