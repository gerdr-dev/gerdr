package chromeext

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gerdr-dev/gerdr/internal/i18n"
	"golang.org/x/image/draw"
)

// What `gerdr browser` and its MCP server can do in a window, modelled on Claude in Chrome: an outline of the page
// with refs (e12) for the things on it, then clicks / typing / keys / scrolling with real input events
// (Input.dispatch*: the page sees isTrusted events, which rich editors require), waiting, the console and network
// logs, uploads, the viewport size, JavaScript dialogs. Everything goes through the extension's allow-list.

//go:embed page.js
var pageJS string

// ActArgs are every action's arguments; each action reads the ones it needs.
type ActArgs struct {
	Target    string          `json:"target,omitempty"` // a ref from snapshot (e12) or viewport coordinates "x,y" (CSS px, as in screenshot)
	To        string          `json:"to,omitempty"`     // drag: where to
	Text      string          `json:"text,omitempty"`   // type: what; wait: text to appear
	Submit    bool            `json:"submit,omitempty"` // type: press Enter afterwards
	Keys      []string        `json:"keys,omitempty"`   // key: Enter, Tab, cmd+a, shift+ArrowDown…
	Repeat    int             `json:"repeat,omitempty"`
	Value     *string         `json:"value,omitempty"` // fill
	Values    []string        `json:"values,omitempty"`
	Direction string          `json:"direction,omitempty"` // scroll: up / down / left / right
	Amount    int             `json:"amount,omitempty"`    // scroll: wheel ticks of 100 px (default 3)
	URL       string          `json:"url,omitempty"`
	Expr      string          `json:"expr,omitempty"`
	Full      bool            `json:"full,omitempty"`   // screenshot: the whole page
	Format    string          `json:"format,omitempty"` // screenshot: png (default) / jpeg
	Filter    string          `json:"filter,omitempty"` // console / network
	Type      string          `json:"type,omitempty"`   // network: resource type
	Errors    bool            `json:"errors,omitempty"` // console: errors only
	Clear     bool            `json:"clear,omitempty"`
	Limit     int             `json:"limit,omitempty"`
	Find      string          `json:"find,omitempty"` // snapshot: only lines containing this
	All       bool            `json:"all,omitempty"`  // snapshot: text too, not just controls and structure
	Viewport  bool            `json:"viewport,omitempty"`
	Max       int             `json:"max,omitempty"`
	Files     []string        `json:"files,omitempty"`
	Width     int             `json:"width,omitempty"`
	Height    int             `json:"height,omitempty"`
	Mobile    bool            `json:"mobile,omitempty"`
	Reset     bool            `json:"reset,omitempty"`
	Accept    bool            `json:"accept,omitempty"` // dialog
	Gone      string          `json:"gone,omitempty"`   // wait: text to disappear
	Selector  string          `json:"selector,omitempty"`
	Load      bool            `json:"load,omitempty"`
	Time      int             `json:"time,omitempty"`    // wait: ms
	Timeout   int             `json:"timeout,omitempty"` // ms
	Button    string          `json:"button,omitempty"`  // click: left / right / middle
	Double    bool            `json:"double,omitempty"`
	Modifiers []string        `json:"modifiers,omitempty"` // click: cmd / ctrl / shift / alt
	ID        string          `json:"id,omitempty"`        // body: the request id from network
	Method    string          `json:"method,omitempty"`    // cdp
	Params    json.RawMessage `json:"params,omitempty"`
}

// ActResult is what an action gives back: text for the agent, and an image for screenshot.
type ActResult struct {
	Tab   int    `json:"tab"`
	Text  string `json:"text"`
	Image string `json:"image,omitempty"` // base64
	MIME  string `json:"mime,omitempty"`
}

// Actions lists the action names (for the MCP server and help).
var Actions = []string{"snapshot", "text", "screenshot", "click", "hover", "drag", "type", "key", "fill", "scroll", "wait",
	"goto", "back", "forward", "reload", "eval", "console", "network", "body", "upload", "resize", "dialog", "cdp"}

// Act runs one action in a window (0 = the one opened last).
func (b *Bridge) Act(ctx context.Context, tabID int, action string, a ActArgs) (*ActResult, error) {
	t, err := b.tab(tabID)
	if err != nil {
		return nil, err
	}
	res, err := t.act(ctx, action, a)
	if err != nil {
		return nil, err
	}
	res.Tab = t.ID
	if d := t.log.openDialog(); d != nil && action != "dialog" {
		res.Text += fmt.Sprintf("\n\n"+i18n.S("页面弹出了 %s 对话框：%q。页面在等它，先用 dialog 回答（accept / dismiss）。", "The page opened a %s dialog: %q. The page waits for it: answer with dialog (accept / dismiss) first."), d.Type, d.Message)
	}
	return res, nil
}

func (t *Tab) act(ctx context.Context, action string, a ActArgs) (*ActResult, error) {
	switch action {
	case "snapshot":
		return t.snapshot(ctx, a)
	case "text":
		var r struct {
			URL, Title, Text string
			Total            int
		}
		max := a.Max
		if max <= 0 {
			max = 50000
		}
		if err := t.page(ctx, &r, "text", max); err != nil {
			return nil, err
		}
		s := r.URL + "\n" + r.Title + "\n\n" + r.Text
		if r.Total > len(r.Text) {
			s += fmt.Sprintf("\n\n… (%d of %d characters; --max for more)", len(r.Text), r.Total)
		}
		return &ActResult{Text: s}, nil
	case "screenshot":
		return t.screenshot(ctx, a)
	case "click":
		return t.click(ctx, a)
	case "hover":
		x, y, desc, err := t.target(ctx, a.Target)
		if err != nil {
			return nil, err
		}
		if err := t.mouse(ctx, "mouseMoved", x, y, "none", 0, 0); err != nil {
			return nil, err
		}
		time.Sleep(200 * time.Millisecond)
		return &ActResult{Text: "hovered " + desc}, nil
	case "drag":
		return t.drag(ctx, a)
	case "type":
		return t.typeText(ctx, a)
	case "key":
		return t.keys(ctx, a)
	case "fill":
		return t.fill(ctx, a)
	case "scroll":
		return t.scroll(ctx, a)
	case "wait":
		return t.wait(ctx, a)
	case "goto":
		if a.URL == "" {
			return nil, errors.New("url is required")
		}
		var r struct {
			ErrorText string `json:"errorText"`
		}
		if err := t.call(ctx, "Page.navigate", map[string]any{"url": a.URL}, &r); err != nil {
			return nil, err
		}
		if r.ErrorText != "" {
			return nil, errors.New(r.ErrorText)
		}
		return t.settled(ctx, "", true)
	case "back", "forward":
		before := t.url(ctx)
		d := -1
		if action == "forward" {
			d = 1
		}
		if _, err := t.eval(ctx, fmt.Sprintf("history.go(%d)", d)); err != nil {
			return nil, err
		}
		res, err := t.settled(ctx, before, true)
		if err == nil && res.Text == "" {
			res.Text = fmt.Sprintf("still at %s (no page to go %s to)", before, action)
		}
		return res, err
	case "reload":
		if err := t.call(ctx, "Page.reload", map[string]any{}, nil); err != nil {
			return nil, err
		}
		return t.settled(ctx, "", true)
	case "eval":
		v, err := t.eval(ctx, a.Expr)
		if err != nil {
			return nil, err
		}
		var s string
		if json.Unmarshal(v, &s) == nil {
			return &ActResult{Text: s}, nil
		}
		return &ActResult{Text: string(v)}, nil
	case "console":
		limit := a.Limit
		if limit <= 0 {
			limit = 100
		}
		return &ActResult{Text: t.log.consoleText(a.Errors, a.Filter, limit, a.Clear)}, nil
	case "network":
		limit := a.Limit
		if limit <= 0 {
			limit = 100
		}
		return &ActResult{Text: t.log.netText(a.Filter, a.Type, limit, a.Clear)}, nil
	case "body":
		var r struct {
			Body          string `json:"body"`
			Base64Encoded bool   `json:"base64Encoded"`
		}
		if err := t.call(ctx, "Network.getResponseBody", map[string]any{"requestId": a.ID}, &r); err != nil {
			return nil, err
		}
		body := r.Body
		if r.Base64Encoded {
			raw, err := base64.StdEncoding.DecodeString(r.Body)
			if err != nil {
				return nil, err
			}
			if !utf8.Valid(raw) {
				return &ActResult{Text: fmt.Sprintf("(binary, %d bytes)", len(raw))}, nil
			}
			body = string(raw)
		}
		max := a.Max
		if max <= 0 {
			max = 100000
		}
		if len(body) > max {
			body = body[:max] + fmt.Sprintf("\n… (%d of %d bytes)", max, len(body))
		}
		return &ActResult{Text: body}, nil
	case "upload":
		return t.upload(ctx, a)
	case "resize":
		if a.Reset {
			if err := t.call(ctx, "Emulation.clearDeviceMetricsOverride", map[string]any{}, nil); err != nil {
				return nil, err
			}
			return &ActResult{Text: "viewport back to the window's size"}, nil
		}
		if a.Width <= 0 || a.Height <= 0 {
			return nil, errors.New("width and height are required (or reset)")
		}
		if err := t.call(ctx, "Emulation.setDeviceMetricsOverride", map[string]any{"width": a.Width, "height": a.Height, "deviceScaleFactor": 0, "mobile": a.Mobile}, nil); err != nil {
			return nil, err
		}
		if a.Mobile {
			t.call(ctx, "Emulation.setTouchEmulationEnabled", map[string]any{"enabled": true, "maxTouchPoints": 5}, nil)
		}
		return &ActResult{Text: fmt.Sprintf("viewport %dx%d", a.Width, a.Height)}, nil
	case "dialog":
		d := t.log.openDialog()
		if d == nil {
			return nil, errors.New(i18n.S("现在没有对话框", "No dialog is open"))
		}
		p := map[string]any{"accept": a.Accept}
		if a.Text != "" {
			p["promptText"] = a.Text
		}
		if err := t.call(ctx, "Page.handleJavaScriptDialog", p, nil); err != nil {
			return nil, err
		}
		t.log.mu.Lock()
		t.log.dialog = nil
		t.log.mu.Unlock()
		verb := "dismissed"
		if a.Accept {
			verb = "accepted"
		}
		return &ActResult{Text: fmt.Sprintf("%s the %s dialog %q", verb, d.Type, d.Message)}, nil
	case "cdp":
		var raw json.RawMessage
		var params any = a.Params
		if len(a.Params) == 0 {
			params = map[string]any{}
		}
		if err := t.call(ctx, a.Method, params, &raw); err != nil {
			return nil, err
		}
		return &ActResult{Text: string(raw)}, nil
	}
	return nil, fmt.Errorf("unknown action %q (one of %s)", action, strings.Join(Actions, ", "))
}

// ---------- plumbing ----------

// call sends one DevTools command. While a JavaScript dialog is open the page is paused and most commands never
// answer, so they fail at once; a command that opens one (a click on a button that calls confirm()) returns as soon
// as it is open instead of waiting for the dialog to be answered.
func (t *Tab) call(ctx context.Context, method string, params any, out any) error {
	if method != "Page.handleJavaScriptDialog" {
		if d := t.log.openDialog(); d != nil {
			return fmt.Errorf(i18n.S("页面开着一个 %s 对话框（%q），先用 dialog 回答", "A %s dialog is open (%q): answer it with dialog first"), d.Type, d.Message)
		}
	}
	p, err := json.Marshal(params)
	if err != nil {
		return err
	}
	type reply struct {
		raw json.RawMessage
		err error
	}
	ch := make(chan reply, 1)
	cctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	go func() {
		defer cancel()
		raw, err := cdp(cctx, t.conn, t.ID, method, p)
		ch <- reply{raw, err}
	}()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case r := <-ch:
			if r.err != nil {
				return r.err
			}
			if out != nil {
				if raw, ok := out.(*json.RawMessage); ok {
					*raw = r.raw
					return nil
				}
				return json.Unmarshal(r.raw, out)
			}
			return nil
		case <-tick.C:
			if method != "Page.handleJavaScriptDialog" && t.log.openDialog() != nil {
				return nil
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// eval runs JavaScript in the page and returns its value as JSON (promises are awaited).
func (t *Tab) eval(ctx context.Context, expr string) (json.RawMessage, error) {
	var r struct {
		Result struct {
			Value       json.RawMessage `json:"value"`
			Description string          `json:"description"`
			Type        string          `json:"type"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text      string `json:"text"`
			Exception *struct {
				Description string `json:"description"`
			} `json:"exception"`
		} `json:"exceptionDetails"`
	}
	err := t.call(ctx, "Runtime.evaluate", map[string]any{"expression": expr, "returnByValue": true, "awaitPromise": true, "userGesture": true}, &r)
	if err != nil {
		return nil, err
	}
	if e := r.ExceptionDetails; e != nil {
		msg := e.Text
		if e.Exception != nil && e.Exception.Description != "" {
			msg = e.Exception.Description
		}
		// "Error: ref e3 is gone …\n    at <anonymous>…": the first line is enough
		msg, _, _ = strings.Cut(msg, "\n    at ")
		return nil, errors.New(strings.TrimPrefix(msg, "Error: "))
	}
	if len(r.Result.Value) == 0 {
		if r.Result.Type == "undefined" {
			return json.RawMessage("null"), nil
		}
		v, _ := json.Marshal(r.Result.Description)
		return v, nil
	}
	return r.Result.Value, nil
}

// page calls a helper of page.js with JSON arguments and decodes its result into out.
func (t *Tab) page(ctx context.Context, out any, fn string, args ...any) error {
	parts := make([]string, len(args))
	for i, a := range args {
		b, _ := json.Marshal(a)
		parts[i] = string(b)
	}
	v, err := t.eval(ctx, "("+pageJS+")."+fn+"("+strings.Join(parts, ",")+")")
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(v, out)
}

func (t *Tab) url(ctx context.Context) string {
	v, err := t.eval(ctx, "location.href")
	if err != nil {
		return ""
	}
	var s string
	json.Unmarshal(v, &s)
	return s
}

// settled waits for a navigation the last action may have started, then says where the page is.
// before: the URL before the action ("" = always report).
func (t *Tab) settled(ctx context.Context, before string, nav bool) (*ActResult, error) {
	wait := 300 * time.Millisecond
	if nav {
		wait = 500 * time.Millisecond
	}
	time.Sleep(wait)
	deadline := time.Now().Add(15 * time.Second)
	var m struct {
		URL, Title, Ready string
	}
	for time.Now().Before(deadline) {
		if t.log.openDialog() != nil {
			return &ActResult{}, nil
		}
		err := t.page(ctx, &m, "metrics")
		if err == nil && m.Ready == "complete" {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	if before != "" && m.URL == before {
		return &ActResult{}, nil
	}
	return &ActResult{Text: fmt.Sprintf("now at %s — %s", m.URL, m.Title)}, nil
}

// target resolves "e12" or "x,y" to viewport coordinates.
func (t *Tab) target(ctx context.Context, target string) (x, y float64, desc string, err error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return 0, 0, "", errors.New(i18n.S("要指定目标：snapshot 里的 ref（如 e12），或坐标 x,y", "a target is required: a ref from snapshot (like e12) or coordinates x,y"))
	}
	if xs, ys, ok := strings.Cut(target, ","); ok {
		x, err1 := strconv.ParseFloat(strings.TrimSpace(xs), 64)
		y, err2 := strconv.ParseFloat(strings.TrimSpace(ys), 64)
		if err1 != nil || err2 != nil {
			return 0, 0, "", fmt.Errorf("bad coordinates %q", target)
		}
		return x, y, fmt.Sprintf("(%g, %g)", x, y), nil
	}
	var p struct {
		X, Y    float64
		Desc    string
		Covered string
	}
	if err := t.page(ctx, &p, "point", target); err != nil {
		return 0, 0, "", err
	}
	desc = target + " " + p.Desc
	if p.Covered != "" {
		desc += fmt.Sprintf(" (note: %s is on top of it there)", p.Covered)
	}
	return p.X, p.Y, desc, nil
}

func modifierBits(mods []string) int {
	bits := 0
	for _, m := range mods {
		switch strings.ToLower(m) {
		case "alt", "option", "opt":
			bits |= 1
		case "ctrl", "control":
			bits |= 2
		case "cmd", "meta", "command", "super", "win":
			bits |= 4
		case "shift":
			bits |= 8
		case "mod":
			if runtime.GOOS == "darwin" {
				bits |= 4
			} else {
				bits |= 2
			}
		}
	}
	return bits
}

func (t *Tab) mouse(ctx context.Context, typ string, x, y float64, button string, clicks, mods int) error {
	p := map[string]any{"type": typ, "x": x, "y": y, "modifiers": mods}
	if button != "" {
		p["button"] = button
	}
	if clicks > 0 {
		p["clickCount"] = clicks
	}
	if typ == "mousePressed" || (typ == "mouseMoved" && button != "none" && button != "") {
		p["buttons"] = map[string]int{"left": 1, "right": 2, "middle": 4}[button]
	}
	return t.call(ctx, "Input.dispatchMouseEvent", p, nil)
}

// ---------- actions ----------

func (t *Tab) snapshot(ctx context.Context, a ActArgs) (*ActResult, error) {
	var r struct {
		URL, Title string
		Viewport   struct{ W, H int }
		Scroll     struct{ X, Y, W, H int }
		Lines      []string
		Total      int
	}
	max := a.Max
	if max <= 0 {
		max = 400
	}
	if err := t.page(ctx, &r, "snapshot", map[string]any{"all": a.All, "viewport": a.Viewport, "find": a.Find, "max": max}); err != nil {
		return nil, err
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s — %s\nviewport %dx%d, scrolled to %d,%d of %dx%d\n\n", r.URL, r.Title, r.Viewport.W, r.Viewport.H, r.Scroll.X, r.Scroll.Y, r.Scroll.W, r.Scroll.H)
	sb.WriteString(strings.Join(r.Lines, "\n"))
	if len(r.Lines) == 0 {
		sb.WriteString("(nothing matched)")
	}
	if r.Total > len(r.Lines) {
		fmt.Fprintf(&sb, "\n\n… %d of %d lines shown: narrow it with find / viewport, or raise max", len(r.Lines), r.Total)
	}
	return &ActResult{Text: sb.String()}, nil
}

func (t *Tab) screenshot(ctx context.Context, a ActArgs) (*ActResult, error) {
	var m struct {
		DPR          float64
		W, H, SW, SH float64
		X, Y         float64
	}
	if err := t.page(ctx, &m, "metrics"); err != nil {
		return nil, err
	}
	if m.DPR <= 0 {
		m.DPR = 1
	}
	format := "png"
	if a.Format == "jpeg" || a.Format == "jpg" {
		format = "jpeg"
	}
	// The image is in CSS pixels, so a point on it is the same point for click x,y (as long as it's the viewport).
	// Asking Chrome for that with clip.scale makes a visible window flash: it re-renders the page at the smaller
	// scale on screen for the capture (measured: every clip+scale capture flickered, plain ones never did). So the
	// viewport and an element inside it are captured as they are and cropped / scaled down here; only a whole page
	// or an element bigger than the viewport still needs Chrome's clip.
	what := fmt.Sprintf("viewport %gx%g (coordinates match click x,y)", m.W, m.H)
	crop := image.Rect(0, 0, int(m.W), int(m.H))
	var clip map[string]any
	switch {
	case a.Target != "" && !strings.Contains(a.Target, ","):
		var bx struct{ X, Y, W, H float64 }
		if err := t.page(ctx, &bx, "box", a.Target); err != nil {
			return nil, err
		}
		what = a.Target
		// box is in page coordinates; after scrolling it into view, is it all inside the viewport?
		var now struct{ X, Y float64 }
		if err := t.page(ctx, &now, "metrics"); err != nil {
			return nil, err
		}
		vx, vy := bx.X-now.X, bx.Y-now.Y
		if vx >= 0 && vy >= 0 && vx+bx.W <= m.W && vy+bx.H <= m.H {
			crop = image.Rect(int(vx), int(vy), int(math.Ceil(vx+bx.W)), int(math.Ceil(vy+bx.H)))
		} else {
			clip = map[string]any{"x": bx.X, "y": bx.Y, "width": bx.W, "height": bx.H, "scale": 1 / m.DPR}
		}
	case a.Full:
		h := min(m.SH, 16000)
		clip = map[string]any{"x": 0, "y": 0, "width": m.SW, "height": h, "scale": 1 / m.DPR}
		what = fmt.Sprintf("whole page %gx%g", m.SW, h)
	}
	p := map[string]any{"format": format}
	if clip != nil {
		p["clip"] = clip
		p["captureBeyondViewport"] = true
	} else {
		p["format"] = "png" // re-encoded below
	}
	if p["format"] == "jpeg" {
		p["quality"] = 80
	}
	var r struct {
		Data string `json:"data"`
	}
	if err := t.call(ctx, "Page.captureScreenshot", p, &r); err != nil {
		return nil, err
	}
	if clip == nil {
		data, err := toCSSPixels(r.Data, crop, m.DPR, format)
		if err != nil {
			return nil, err
		}
		r.Data = data
	}
	return &ActResult{Text: "screenshot of " + what, Image: r.Data, MIME: "image/" + format}, nil
}

func (t *Tab) click(ctx context.Context, a ActArgs) (*ActResult, error) {
	before := t.url(ctx)
	x, y, desc, err := t.target(ctx, a.Target)
	if err != nil {
		return nil, err
	}
	button := a.Button
	if button == "" {
		button = "left"
	}
	mods := modifierBits(a.Modifiers)
	if err := t.mouse(ctx, "mouseMoved", x, y, "none", 0, mods); err != nil {
		return nil, err
	}
	clicks := 1
	if a.Double {
		clicks = 2
	}
	for i := 1; i <= clicks; i++ {
		if err := t.mouse(ctx, "mousePressed", x, y, button, i, mods); err != nil {
			return nil, err
		}
		if err := t.mouse(ctx, "mouseReleased", x, y, button, i, mods); err != nil {
			return nil, err
		}
	}
	verb := "clicked"
	if a.Double {
		verb = "double-clicked"
	}
	res, err := t.settled(ctx, before, false)
	if err != nil {
		return nil, err
	}
	res.Text = strings.TrimSpace(verb + " " + desc + "\n" + res.Text)
	return res, nil
}

func (t *Tab) drag(ctx context.Context, a ActArgs) (*ActResult, error) {
	x1, y1, d1, err := t.target(ctx, a.Target)
	if err != nil {
		return nil, err
	}
	x2, y2, d2, err := t.target(ctx, a.To)
	if err != nil {
		return nil, err
	}
	if err := t.mouse(ctx, "mouseMoved", x1, y1, "none", 0, 0); err != nil {
		return nil, err
	}
	if err := t.mouse(ctx, "mousePressed", x1, y1, "left", 1, 0); err != nil {
		return nil, err
	}
	const steps = 12
	for i := 1; i <= steps; i++ {
		f := float64(i) / steps
		if err := t.mouse(ctx, "mouseMoved", x1+(x2-x1)*f, y1+(y2-y1)*f, "left", 0, 0); err != nil {
			return nil, err
		}
		time.Sleep(16 * time.Millisecond)
	}
	if err := t.mouse(ctx, "mouseReleased", x2, y2, "left", 1, 0); err != nil {
		return nil, err
	}
	return &ActResult{Text: fmt.Sprintf("dragged %s to %s", d1, d2)}, nil
}

func (t *Tab) typeText(ctx context.Context, a ActArgs) (*ActResult, error) {
	msg := "typed"
	if a.Target != "" {
		if strings.Contains(a.Target, ",") {
			if _, err := t.click(ctx, ActArgs{Target: a.Target}); err != nil {
				return nil, err
			}
		} else {
			var desc string
			if err := t.page(ctx, &desc, "focus", a.Target); err != nil {
				return nil, err
			}
			msg = "typed into " + a.Target + " " + desc
		}
	}
	if a.Text != "" {
		if err := t.call(ctx, "Input.insertText", map[string]any{"text": a.Text}, nil); err != nil {
			return nil, err
		}
	}
	if a.Submit {
		before := t.url(ctx)
		if err := t.key(ctx, "Enter"); err != nil {
			return nil, err
		}
		res, err := t.settled(ctx, before, false)
		if err != nil {
			return nil, err
		}
		res.Text = strings.TrimSpace(msg + " and pressed Enter\n" + res.Text)
		return res, nil
	}
	return &ActResult{Text: msg}, nil
}

func (t *Tab) keys(ctx context.Context, a ActArgs) (*ActResult, error) {
	if len(a.Keys) == 0 {
		return nil, errors.New("keys are required, like Enter, Tab, cmd+a, shift+ArrowDown")
	}
	before := t.url(ctx)
	n := max(a.Repeat, 1)
	for i := 0; i < n; i++ {
		for _, k := range a.Keys {
			if err := t.key(ctx, k); err != nil {
				return nil, err
			}
		}
	}
	res, err := t.settled(ctx, before, false)
	if err != nil {
		return nil, err
	}
	res.Text = strings.TrimSpace("pressed " + strings.Join(a.Keys, " ") + "\n" + res.Text)
	return res, nil
}

type keyDef struct {
	key, code string
	vk        int
	text      string
}

var namedKeys = map[string]keyDef{
	"enter": {"Enter", "Enter", 13, "\r"}, "return": {"Enter", "Enter", 13, "\r"}, "tab": {"Tab", "Tab", 9, ""},
	"escape": {"Escape", "Escape", 27, ""}, "esc": {"Escape", "Escape", 27, ""}, "backspace": {"Backspace", "Backspace", 8, ""},
	"delete": {"Delete", "Delete", 46, ""}, "space": {" ", "Space", 32, " "}, "arrowup": {"ArrowUp", "ArrowUp", 38, ""},
	"arrowdown": {"ArrowDown", "ArrowDown", 40, ""}, "arrowleft": {"ArrowLeft", "ArrowLeft", 37, ""},
	"arrowright": {"ArrowRight", "ArrowRight", 39, ""}, "up": {"ArrowUp", "ArrowUp", 38, ""}, "down": {"ArrowDown", "ArrowDown", 40, ""},
	"left": {"ArrowLeft", "ArrowLeft", 37, ""}, "right": {"ArrowRight", "ArrowRight", 39, ""}, "home": {"Home", "Home", 36, ""},
	"end": {"End", "End", 35, ""}, "pageup": {"PageUp", "PageUp", 33, ""}, "pagedown": {"PageDown", "PageDown", 34, ""},
	"f1": {"F1", "F1", 112, ""}, "f2": {"F2", "F2", 113, ""}, "f3": {"F3", "F3", 114, ""}, "f4": {"F4", "F4", 115, ""},
	"f5": {"F5", "F5", 116, ""}, "f6": {"F6", "F6", 117, ""}, "f7": {"F7", "F7", 118, ""}, "f8": {"F8", "F8", 119, ""},
	"f9": {"F9", "F9", 120, ""}, "f10": {"F10", "F10", 121, ""}, "f11": {"F11", "F11", 122, ""}, "f12": {"F12", "F12", 123, ""},
}

// editing shortcuts on macOS: synthetic key events don't trigger them, the "commands" field does
var macCommands = map[string]string{"a": "selectAll", "c": "copy", "x": "cut", "v": "paste", "z": "undo"}

// key presses one key combination like "Enter", "cmd+a", "shift+Tab", "ctrl+shift+ArrowLeft".
func (t *Tab) key(ctx context.Context, combo string) error {
	parts := strings.Split(combo, "+")
	if strings.HasSuffix(combo, "++") { // "ctrl++"
		parts = append(strings.Split(strings.TrimSuffix(combo, "++"), "+"), "+")
	}
	name := parts[len(parts)-1]
	mods := modifierBits(parts[:len(parts)-1])
	var d keyDef
	if k, ok := namedKeys[strings.ToLower(name)]; ok {
		d = k
	} else if utf8.RuneCountInString(name) == 1 {
		r, _ := utf8.DecodeRuneInString(name)
		d = keyDef{key: name, text: name}
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
			up := strings.ToUpper(name)
			d.code, d.vk = "Key"+up, int(up[0])
			if mods&8 != 0 {
				d.key, d.text = up, up
			} else {
				d.key, d.text = strings.ToLower(name), strings.ToLower(name)
			}
		case r >= '0' && r <= '9':
			d.code, d.vk = "Digit"+name, int(r)
		}
	} else {
		return fmt.Errorf("unknown key %q (Enter, Tab, Escape, Backspace, Delete, Space, Arrow*, Home, End, PageUp, PageDown, F1–F12, or one character; modifiers cmd / ctrl / alt / shift / mod joined with +)", name)
	}
	p := map[string]any{"key": d.key, "code": d.code, "windowsVirtualKeyCode": d.vk, "nativeVirtualKeyCode": d.vk, "modifiers": mods}
	down := maps(p)
	if d.text != "" && mods&(1|2|4) == 0 {
		down["type"], down["text"], down["unmodifiedText"] = "keyDown", d.text, d.text
	} else {
		down["type"] = "rawKeyDown"
	}
	if runtime.GOOS == "darwin" && mods&4 != 0 {
		if c, ok := macCommands[strings.ToLower(name)]; ok {
			if c == "undo" && mods&8 != 0 {
				c = "redo"
			}
			down["commands"] = []string{c}
		}
	}
	if err := t.call(ctx, "Input.dispatchKeyEvent", down, nil); err != nil {
		return err
	}
	up := maps(p)
	up["type"] = "keyUp"
	return t.call(ctx, "Input.dispatchKeyEvent", up, nil)
}

func maps(m map[string]any) map[string]any {
	out := make(map[string]any, len(m)+3)
	for k, v := range m {
		out[k] = v
	}
	return out
}

func (t *Tab) fill(ctx context.Context, a ActArgs) (*ActResult, error) {
	if a.Target == "" || strings.Contains(a.Target, ",") {
		return nil, errors.New(i18n.S("fill 要一个 snapshot 里的 ref", "fill needs a ref from snapshot"))
	}
	var p struct {
		Kind, Desc string
		Checked    bool
	}
	if err := t.page(ctx, &p, "prepare", a.Target); err != nil {
		return nil, err
	}
	switch p.Kind {
	case "select":
		vals := a.Values
		if a.Value != nil {
			vals = append(vals, *a.Value)
		}
		var got string
		if err := t.page(ctx, &got, "selectOption", a.Target, vals); err != nil {
			return nil, err
		}
		return &ActResult{Text: fmt.Sprintf("selected %q in %s %s", got, a.Target, p.Desc)}, nil
	case "check":
		want := a.Value == nil || !(strings.EqualFold(*a.Value, "false") || *a.Value == "0" || strings.EqualFold(*a.Value, "off") || strings.EqualFold(*a.Value, "unchecked"))
		if want == p.Checked {
			return &ActResult{Text: fmt.Sprintf("%s %s was already %s", a.Target, p.Desc, map[bool]string{true: "checked", false: "unchecked"}[want])}, nil
		}
		return t.click(ctx, ActArgs{Target: a.Target})
	}
	v := ""
	if a.Value != nil {
		v = *a.Value
	}
	if v == "" {
		if err := t.key(ctx, "Delete"); err != nil {
			return nil, err
		}
		return &ActResult{Text: fmt.Sprintf("cleared %s %s", a.Target, p.Desc)}, nil
	}
	if err := t.call(ctx, "Input.insertText", map[string]any{"text": v}, nil); err != nil {
		return nil, err
	}
	return &ActResult{Text: fmt.Sprintf("filled %s %s", a.Target, p.Desc)}, nil
}

func (t *Tab) scroll(ctx context.Context, a ActArgs) (*ActResult, error) {
	dir := strings.ToLower(a.Direction)
	if dir == "into-view" || dir == "intoview" || (dir == "" && a.Target != "" && !strings.Contains(a.Target, ",")) {
		var desc string
		if err := t.page(ctx, &desc, "scrollIntoView", a.Target); err != nil {
			return nil, err
		}
		return &ActResult{Text: "scrolled " + a.Target + " " + desc + " into view"}, nil
	}
	if dir == "" {
		dir = "down"
	}
	amount := a.Amount
	if amount <= 0 {
		amount = 3
	}
	dx, dy := 0, 0
	switch dir {
	case "down":
		dy = amount * 100
	case "up":
		dy = -amount * 100
	case "right":
		dx = amount * 100
	case "left":
		dx = -amount * 100
	default:
		return nil, fmt.Errorf("direction must be up, down, left, right or into-view, not %q", a.Direction)
	}
	var ref any
	var x, y float64
	if a.Target != "" {
		if strings.Contains(a.Target, ",") {
			var err error
			if x, y, _, err = t.target(ctx, a.Target); err != nil {
				return nil, err
			}
		} else {
			ref = a.Target
		}
	} else {
		var m struct{ W, H float64 }
		if err := t.page(ctx, &m, "metrics"); err != nil {
			return nil, err
		}
		x, y = m.W/2, m.H/2
	}
	var r struct {
		What               string
		X, Y, W, H, VW, VH float64
	}
	if err := t.page(ctx, &r, "scrollBy", ref, x, y, dx, dy); err != nil {
		return nil, err
	}
	time.Sleep(300 * time.Millisecond) // let lazy loading start
	return &ActResult{Text: fmt.Sprintf("scrolled %s %s; now at %g,%g of %gx%g (visible %gx%g)", r.What, dir, r.X, r.Y, r.W, r.H, r.VW, r.VH)}, nil
}

func (t *Tab) wait(ctx context.Context, a ActArgs) (*ActResult, error) {
	if a.Time > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(min(a.Time, 300000)) * time.Millisecond):
		}
		return &ActResult{Text: fmt.Sprintf("waited %d ms", a.Time)}, nil
	}
	cond := map[string]any{}
	var what []string
	if a.Text != "" {
		cond["text"] = a.Text
		what = append(what, fmt.Sprintf("text %q appeared", a.Text))
	}
	if a.Gone != "" {
		cond["gone"] = a.Gone
		what = append(what, fmt.Sprintf("text %q is gone", a.Gone))
	}
	if a.Selector != "" {
		cond["selector"] = a.Selector
		what = append(what, fmt.Sprintf("%s is shown", a.Selector))
	}
	if a.URL != "" {
		cond["url"] = a.URL
		what = append(what, fmt.Sprintf("the URL has %q", a.URL))
	}
	if a.Load || len(cond) == 0 {
		cond["load"] = true
		what = append(what, "the page loaded")
	}
	timeout := time.Duration(a.Timeout) * time.Millisecond
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	deadline := time.Now().Add(min(timeout, 10*time.Minute))
	start := time.Now()
	for {
		var ok bool
		err := t.page(ctx, &ok, "check", cond)
		if err == nil && ok {
			return &ActResult{Text: fmt.Sprintf("%s (after %.1fs)", strings.Join(what, ", "), time.Since(start).Seconds())}, nil
		}
		if t.log.openDialog() != nil {
			return &ActResult{Text: "stopped waiting"}, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timed out after %s waiting until %s", timeout, strings.Join(what, ", "))
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
}

func (t *Tab) upload(ctx context.Context, a ActArgs) (*ActResult, error) {
	if a.Target == "" || strings.Contains(a.Target, ",") {
		return nil, errors.New(i18n.S("upload 要文件输入框的 ref（snapshot 里的 file）", "upload needs the ref of a file input (file in snapshot)"))
	}
	if len(a.Files) == 0 {
		return nil, errors.New("files are required")
	}
	for i, f := range a.Files {
		abs, err := filepath.Abs(f)
		if err != nil {
			return nil, err
		}
		if _, err := os.Stat(abs); err != nil {
			return nil, err
		}
		a.Files[i] = abs
	}
	var r struct {
		Result struct {
			ObjectID string `json:"objectId"`
		} `json:"result"`
		ExceptionDetails *struct {
			Exception *struct {
				Description string `json:"description"`
			} `json:"exception"`
		} `json:"exceptionDetails"`
	}
	ref, _ := json.Marshal(a.Target)
	if err := t.call(ctx, "Runtime.evaluate", map[string]any{"expression": "(" + pageJS + ").get(" + string(ref) + ")"}, &r); err != nil {
		return nil, err
	}
	if r.ExceptionDetails != nil || r.Result.ObjectID == "" {
		return nil, fmt.Errorf("ref %s is gone (the page changed): take a new snapshot", a.Target)
	}
	defer t.call(ctx, "Runtime.releaseObject", map[string]any{"objectId": r.Result.ObjectID}, nil)
	if err := t.call(ctx, "DOM.setFileInputFiles", map[string]any{"files": a.Files, "objectId": r.Result.ObjectID}, nil); err != nil {
		return nil, err
	}
	names := make([]string, len(a.Files))
	for i, f := range a.Files {
		names[i] = filepath.Base(f)
	}
	return &ActResult{Text: "attached " + strings.Join(names, ", ") + " to " + a.Target}, nil
}

// toCSSPixels takes a plain capture of the viewport (device pixels, base64 PNG), cuts out crop (CSS pixels) and
// scales it to one image pixel per CSS pixel. The capture can differ from innerWidth x innerHeight by a pixel or a
// scrollbar, so the scale comes from the image and the crop is kept inside it.
func toCSSPixels(b64 string, crop image.Rectangle, dpr float64, format string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return "", err
	}
	src, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	if dpr <= 0 {
		dpr = 1
	}
	b := src.Bounds()
	from := image.Rect(
		int(float64(crop.Min.X)*dpr), int(float64(crop.Min.Y)*dpr),
		int(math.Ceil(float64(crop.Max.X)*dpr)), int(math.Ceil(float64(crop.Max.Y)*dpr)),
	).Add(b.Min).Intersect(b)
	if from.Empty() {
		return "", errors.New("nothing to capture: the area is outside the window")
	}
	w := max(1, int(math.Round(float64(from.Dx())/dpr)))
	h := max(1, int(math.Round(float64(from.Dy())/dpr)))
	var img image.Image = src
	if w != b.Dx() || h != b.Dy() || from != b {
		dst := image.NewRGBA(image.Rect(0, 0, w, h))
		draw.CatmullRom.Scale(dst, dst.Bounds(), src, from, draw.Src, nil)
		img = dst
	}
	var out bytes.Buffer
	if format == "jpeg" {
		err = jpeg.Encode(&out, img, &jpeg.Options{Quality: 80})
	} else if img == src {
		out.Write(raw)
	} else {
		err = png.Encode(&out, img)
	}
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(out.Bytes()), nil
}
