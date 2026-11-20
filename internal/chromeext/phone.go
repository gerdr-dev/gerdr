package chromeext

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
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
	// what each cross-site iframe gets too while the page plays a device (frameDevice); nil = nothing
	frameCmds []frameCmd
	unframe   func()
}

type frameCmd struct {
	Method string `json:"method"`
	Params any    `json:"params"`
}

// Fit is a page's laid-out size (CSS px) and whether it is laid out as a mobile device (sites' mobile layout, touch).
// Device "phone" / "tablet" also makes the page that device to the sites (fitUA): the size alone left a site that
// tells devices apart by the user agent sending its desktop page (asked for).
type Fit struct {
	W, H   int
	Mobile bool
	Device string `json:"-"` // "", "phone", "tablet"
	// Pin: the person picked this size for the page in its pane's menu (any device, docked or not), so it wins over a
	// remote viewer's phone layout. A floating pane's size doesn't: at its size the page ran below a phone's pane
	Pin bool `json:"-"`
	// Scale: the page drawn at this fraction of its size (Emulation's scale), for the App's real page in a pane
	// narrower than the size picked (a desktop page in a 600 px pane): the page keeps that layout and is shrunk to
	// fit, as pictures would be, but stays the real page. 0 = 1. Headless pages show as pictures, scaled by the viewer.
	Scale float64 `json:"-"`
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
	// docked again: the page takes the pane's size, and is the browser it is again; unless the person picked a size
	// for it (Pin): the pane then shows the real page at that size (asked for: not pictures of it)
	clear := on && p.fit != nil && !p.fit.Pin
	if clear {
		p.fit = nil
	}
	p.mu.Unlock()
	t.applyPhone()
	if clear {
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
	if device != "" {
		t.frameDevice(ctx, []frameCmd{
			{"Emulation.setUserAgentOverride", ua},
			{"Emulation.setTouchEmulationEnabled", map[string]any{"enabled": true, "maxTouchPoints": 5}},
		})
	} else {
		t.frameDevice(ctx, nil)
	}
	t.send(ctx, "Page.reload", nil)
}

// frameDevice makes every cross-site iframe of the page the same device as the page (cmds; nil = stop). Overrides are
// per DevTools target, and such an iframe is a target of its own: a page playing an Android phone had iframes still
// saying Mac (measured: the iframe's navigator was MacIntel with no touch while the page's was Android), and a bot
// check in one (Cloudflare Turnstile on Bing) failed on the mismatch (user report). Each iframe is attached as it
// starts, paused until it has the same overrides (Target.setAutoAttach with waitForDebuggerOnStart), and attaches
// its own iframes the same way. Non-flattened sessions (commands wrapped in Target.sendMessageToTarget, events in
// Target.receivedMessageFromTarget): the App's shell only carries the page's own session, and this needs no change
// there. Only the App's pages: the headless browser's pipe speaks flattened sessions.
func (t *Tab) frameDevice(ctx context.Context, cmds []frameCmd) {
	if !t.Native {
		return
	}
	p := t.phone()
	p.mu.Lock()
	p.frameCmds = cmds
	if cmds != nil && p.unframe == nil {
		p.unframe = t.watch.add(func(method string, params json.RawMessage) { t.frameEvent(nil, method, params) })
	}
	var unframe func()
	if cmds == nil {
		unframe, p.unframe = p.unframe, nil
	}
	p.mu.Unlock()
	if unframe != nil {
		unframe()
	}
	params, _ := json.Marshal(autoAttach(cmds != nil))
	t.send(ctx, "Target.setAutoAttach", params)
}

func autoAttach(on bool) map[string]any {
	return map[string]any{"autoAttach": on, "waitForDebuggerOnStart": on, "flatten": false}
}

// frameEvent: an event of the page (path nil) or of an attached iframe (path: the sessions down to it).
func (t *Tab) frameEvent(path []string, method string, params json.RawMessage) {
	switch method {
	case "Target.attachedToTarget":
		var a struct {
			SessionID  string `json:"sessionId"`
			Waiting    bool   `json:"waitingForDebugger"`
			TargetInfo struct {
				Type string `json:"type"`
			} `json:"targetInfo"`
		}
		if json.Unmarshal(params, &a) != nil || a.SessionID == "" {
			return
		}
		child := append(append([]string{}, path...), a.SessionID)
		go t.frameSetUp(child, a.TargetInfo.Type == "iframe", a.Waiting)
	case "Target.receivedMessageFromTarget":
		var m struct {
			SessionID string `json:"sessionId"`
			Message   string `json:"message"`
		}
		if json.Unmarshal(params, &m) != nil {
			return
		}
		var inner struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal([]byte(m.Message), &inner) != nil || inner.Method == "" {
			return // a command's answer
		}
		t.frameEvent(append(append([]string{}, path...), m.SessionID), inner.Method, inner.Params)
	}
}

// frameSetUp gives an attached target the page's device and lets it run. A worker or anything else is only let go:
// it was paused all the same.
func (t *Tab) frameSetUp(path []string, iframe, waiting bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p := t.phone()
	p.mu.Lock()
	cmds := p.frameCmds
	p.mu.Unlock()
	if iframe {
		for _, c := range cmds {
			t.sendIn(ctx, path, c.Method, c.Params)
		}
		if cmds != nil {
			t.sendIn(ctx, path, "Target.setAutoAttach", autoAttach(true))
		}
	}
	// always, whatever failed above: a target left waiting never runs (a blank iframe)
	if waiting {
		t.sendIn(ctx, path, "Runtime.runIfWaitingForDebugger", map[string]any{})
	}
}

var frameMsgID atomic.Int64

// sendIn sends a command to the target at the end of path, each step wrapped in Target.sendMessageToTarget. The
// answer comes back as an event, unread: what's sent here can't usefully fail.
func (t *Tab) sendIn(ctx context.Context, path []string, method string, params any) {
	msg, _ := json.Marshal(map[string]any{"id": frameMsgID.Add(1), "method": method, "params": params})
	for i := len(path) - 1; i >= 1; i-- {
		msg, _ = json.Marshal(map[string]any{"id": frameMsgID.Add(1), "method": "Target.sendMessageToTarget",
			"params": map[string]any{"sessionId": path[i], "message": string(msg)}})
	}
	outer, _ := json.Marshal(map[string]any{"sessionId": path[0], "message": string(msg)})
	t.send(ctx, "Target.sendMessageToTarget", outer)
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
	if p.fit != nil && p.fit.Pin {
		want = *p.fit
	} else if !p.desktop && size[0] > 0 {
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
		m := map[string]any{"width": want.W, "height": want.H, "deviceScaleFactor": 0, "mobile": want.Mobile}
		if want.Scale > 0 && want.Scale < 1 {
			m["scale"] = want.Scale
		}
		params, _ := json.Marshal(m)
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
