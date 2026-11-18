package chromeext

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/draw"
	"image/jpeg"
	"strings"
	"sync"
	"time"

	"github.com/gerdr-dev/gerdr/internal/i18n"
	"github.com/gerdr-dev/gerdr/internal/screen"
)

// The Browser pane (ui/src/components/browser/) shows a tab through the same stream as Settings → Remote access →
// Control this computer (server/screenstream.go, internal/screen/diff.go: only changed tiles go out, as JPEG): a View
// is the picture source and the input sink in place of the Mac's display.
//
// The picture comes from Page.startScreencast rather than Page.captureScreenshot: Chrome pushes a frame only when the
// page painted, which is also the signal to diff and send, and a still page costs nothing. A frame is acknowledged
// after frameGap, so a page animating non-stop is sampled at ~30 fps instead of costing a JPEG encode per paint.
//
// Two ways out (server/screenstream.go): to a viewer on this computer the screencast's JPEG goes as it is (Raw): no
// decode, diff or encode, which cost 40–80 ms and 30–40 ms a frame at 1400×1800 (measured, TestLiveFrameCost).
// Remote viewers (a phone over the relay) get Grab's picture diffed into changed tiles, a few KB for a keystroke
// instead of a ~200 KB frame.

const frameGap = 30 * time.Millisecond

// View is one viewer's hold on a tab's screen.
type View struct {
	t       *Tab
	ctx     context.Context
	cancel  func()
	unwatch func()
	changed chan struct{}

	mu       sync.Mutex
	frame    []byte // the latest screencast JPEG
	frameSeq int64  // counts frames, so Raw's caller can tell a new one
	vw, vh   float64
	maxW     int
	casting  bool // holds the tab's caster
	page     screen.Page
	gone     string
	pageKick chan struct{}
}

// View starts streaming tab id's screen (0 = the one opened last). Close it when the viewer leaves.
func (b *Bridge) View(id int) (*View, error) {
	t, err := b.tab(id)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	v := &View{t: t, ctx: ctx, cancel: cancel, changed: make(chan struct{}, 1), pageKick: make(chan struct{}, 1)}
	v.unwatch = t.watch.add(v.event)
	go v.pageLoop()
	v.kickPage()
	return v, nil
}

// Tab is the tab's id.
func (v *View) Tab() int { return v.t.ID }

// Close stops the screencast.
func (v *View) Close() {
	v.unwatch()
	v.mu.Lock()
	casting := v.casting
	v.casting = false
	v.mu.Unlock()
	if casting {
		v.t.cast.release()
	}
	v.cancel()
}

// Changed fires when the page painted a new frame.
func (v *View) Changed() <-chan struct{} { return v.changed }

// Gone says why the tab can't be shown any more ("" while it can).
func (v *View) Gone() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.gone
}

// Page is where the tab is (the pane's address bar).
func (v *View) Page() screen.Page {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.page
}

// Size lays the page out at the pane's size (CSS px): a project browser's window is resized; the user's own Chrome
// window is left as it is and shown scaled.
func (v *View) Size(width, height int) {
	if width < 200 || height < 150 || v.t.resize == nil {
		return
	}
	ctx, cancel := context.WithTimeout(v.ctx, 5*time.Second)
	defer cancel()
	v.t.resize(ctx, width, height)
}

// Grab is the latest frame, no wider than maxWidth px, as pixels. The first call (and a new width) starts the
// screencast and waits briefly for its first frame.
func (v *View) Grab(maxWidth int) (*image.RGBA, error) {
	b, _, err := v.latest(maxWidth)
	if err != nil {
		return nil, err
	}
	m, err := jpeg.Decode(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	if rgba, ok := m.(*image.RGBA); ok {
		return rgba, nil
	}
	out := image.NewRGBA(m.Bounds())
	draw.Draw(out, out.Bounds(), m, m.Bounds().Min, draw.Src)
	return out, nil
}

// Raw is the latest frame as the screencast's own JPEG, its size, and its number (the same number = nothing new).
func (v *View) Raw(maxWidth int) (jpg []byte, w, h int, seq int64, err error) {
	b, seq, err := v.latest(maxWidth)
	if err != nil {
		return nil, 0, 0, 0, err
	}
	c, err := jpeg.DecodeConfig(bytes.NewReader(b)) // the header only
	if err != nil {
		return nil, 0, 0, 0, err
	}
	return b, c.Width, c.Height, seq, nil
}

// latest makes sure the screencast runs at maxWidth and returns its newest JPEG.
func (v *View) latest(maxWidth int) ([]byte, int64, error) {
	if g := v.Gone(); g != "" {
		return nil, 0, errors.New(g)
	}
	v.mu.Lock()
	first, resized := !v.casting, v.maxW != maxWidth
	v.casting, v.maxW = true, maxWidth
	if resized {
		v.frame = nil
	}
	v.mu.Unlock()
	if first || resized {
		ctx, cancel := context.WithTimeout(v.ctx, 10*time.Second)
		var err error
		if first {
			err = v.t.cast.acquire(ctx, maxWidth)
		} else {
			err = v.t.cast.width(ctx, maxWidth)
		}
		cancel()
		if err != nil {
			return nil, 0, err
		}
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			v.mu.Lock()
			got := v.frame != nil
			v.mu.Unlock()
			if got {
				break
			}
			select {
			case <-v.changed:
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
	v.mu.Lock()
	b, seq := v.frame, v.frameSeq
	v.mu.Unlock()
	if b == nil {
		return nil, 0, errors.New(i18n.S("页面还没画出来", "The page hasn't drawn anything yet"))
	}
	return b, seq, nil
}

func (v *View) event(method string, params json.RawMessage) {
	switch method {
	case "gerdr.gone":
		v.mu.Lock()
		if v.gone == "" {
			v.gone = v.t.Gone
			if v.gone == "" {
				v.gone = i18n.S("页面关了", "The page was closed")
			}
		}
		v.mu.Unlock()
		v.kick()
	case "Page.screencastFrame":
		var p struct {
			Data      string `json:"data"`
			SessionID int    `json:"sessionId"`
			Metadata  struct {
				DeviceWidth  float64 `json:"deviceWidth"`
				DeviceHeight float64 `json:"deviceHeight"`
			} `json:"metadata"`
		}
		if json.Unmarshal(params, &p) != nil {
			return
		}
		b, err := base64.StdEncoding.DecodeString(p.Data)
		if err == nil {
			v.mu.Lock()
			v.frame, v.vw, v.vh = b, p.Metadata.DeviceWidth, p.Metadata.DeviceHeight
			v.frameSeq++
			v.mu.Unlock()
			v.kick()
		}
	case "Page.navigatedWithinDocument", "Page.frameNavigated", "Page.loadEventFired", "Page.frameStartedLoading", "Page.frameStoppedLoading":
		v.kickPage()
	}
}

func (v *View) kick() {
	select {
	case v.changed <- struct{}{}:
	default:
	}
}

func (v *View) kickPage() {
	select {
	case v.pageKick <- struct{}{}:
	default:
	}
}

// pageLoop keeps Page() current: the address, title and history after each navigation.
func (v *View) pageLoop() {
	for {
		select {
		case <-v.ctx.Done():
			return
		case <-v.pageKick:
		}
		time.Sleep(50 * time.Millisecond)
		ctx, cancel := context.WithTimeout(v.ctx, 5*time.Second)
		var h struct {
			CurrentIndex int `json:"currentIndex"`
			Entries      []struct {
				URL   string `json:"url"`
				Title string `json:"title"`
			} `json:"entries"`
		}
		var p screen.Page
		if raw, err := v.t.send(ctx, "Page.getNavigationHistory", nil); err == nil && json.Unmarshal(raw, &h) == nil && h.CurrentIndex < len(h.Entries) {
			e := h.Entries[h.CurrentIndex]
			p = screen.Page{URL: e.URL, Title: e.Title, Back: h.CurrentIndex > 0, Forward: h.CurrentIndex < len(h.Entries)-1}
		}
		var ready string
		if raw, err := v.t.eval(ctx, editableJS); err == nil {
			var r []any
			if json.Unmarshal(raw, &r) == nil && len(r) == 3 {
				if s, _ := r[0].(string); s != "" {
					p.Title = s
				}
				ready, _ = r[1].(string)
				p.Editable, _ = r[2].(bool)
			}
		}
		cancel()
		p.Loading = ready != "" && ready != "complete"
		v.mu.Lock()
		changed := v.page != p
		v.page = p
		v.mu.Unlock()
		if changed {
			v.kick()
		}
		if p.Loading { // look again until it settles
			go func() {
				time.Sleep(500 * time.Millisecond)
				v.kickPage()
			}()
		}
	}
}

// editableJS: the title, the load state, and whether the focus is somewhere to type (inside iframes and shadow roots).
const editableJS = `(() => {
  let a = document.activeElement;
  for (;;) {
    if (a && a.shadowRoot && a.shadowRoot.activeElement) { a = a.shadowRoot.activeElement; continue }
    if (a && a.tagName === "IFRAME") { try { const d = a.contentDocument; if (d && d.activeElement) { a = d.activeElement; continue } } catch (e) {} }
    break
  }
  const t = a && a.tagName;
  const typing = !!a && (a.isContentEditable || t === "TEXTAREA" || (t === "INPUT" && !/^(button|submit|reset|checkbox|radio|range|color|file|image|hidden)$/i.test(a.type)));
  return [document.title, document.readyState, typing && !a.disabled && !a.readOnly]
})()`

// Do runs one input from the pane. X / Y are fractions of the viewport, as on the remote screen.
func (v *View) Do(in screen.Input) error {
	ctx, cancel := context.WithTimeout(v.ctx, 15*time.Second)
	defer cancel()
	switch in.Kind {
	case "click", "up", "key":
		// the focus may have moved into or out of a field: the pane opens / closes a phone's keyboard by it
		defer v.kickPage()
	}
	t := v.t
	v.mu.Lock()
	vw, vh := v.vw, v.vh
	v.mu.Unlock()
	x, y := clamp01(in.X)*vw, clamp01(in.Y)*vh
	mods := modifierBits(in.Mods)
	// the person's own mouse: quiet, so the extension doesn't draw its cursor over it or wait for it to glide there
	mouse := func(typ, button string, buttons, clicks int) error {
		return t.callQuiet(ctx, "Input.dispatchMouseEvent", map[string]any{"type": typ, "x": x, "y": y, "button": button,
			"buttons": buttons, "clickCount": clicks, "modifiers": mods}, nil)
	}
	button := func() (string, int) {
		if in.Button == "right" {
			return "right", 2
		}
		return "left", 1
	}
	switch in.Kind {
	case "click":
		b, bits := button()
		n := min(max(in.Count, 1), 3)
		for i := 1; i <= n; i++ {
			if err := mouse("mousePressed", b, bits, i); err != nil {
				return err
			}
			if err := mouse("mouseReleased", b, 0, i); err != nil {
				return err
			}
		}
		return nil
	case "down":
		b, bits := button()
		return mouse("mousePressed", b, bits, max(in.Count, 1))
	case "up":
		b, _ := button()
		return mouse("mouseReleased", b, 0, max(in.Count, 1))
	case "move":
		bits := 0
		if in.Button == "left" {
			bits = 1
		}
		b := "none"
		if bits != 0 {
			b = "left"
		}
		return mouse("mouseMoved", b, bits, 0)
	case "scroll":
		// Scrolled from inside the page like the scroll action: Input's mouseWheel never answers in a window of the
		// user's Chrome that isn't in front (docs/dev/CHROME-EXT.md). DX / DY are CSS px here, not lines.
		dx, dy := in.DXPx, in.DYPx
		if dx == 0 && dy == 0 {
			dy = float64(-in.DY) * 40
		}
		_, err := t.eval(ctx, scrollJS(x, y, dx, dy))
		return err
	case "text":
		if in.Text == "" {
			return nil
		}
		// a newline presses Enter (a form submits, an editor breaks the line), like the remote screen
		for i, part := range strings.Split(in.Text, "\n") {
			if i > 0 {
				if err := t.key(ctx, "Enter"); err != nil {
					return err
				}
			}
			if part != "" {
				if err := t.call(ctx, "Input.insertText", map[string]any{"text": part}, nil); err != nil {
					return err
				}
			}
		}
		return nil
	case "compose":
		// the input method's composition in progress (pinyin before a word is picked); "" ends it, and the word
		// picked then comes as text
		return t.call(ctx, "Input.imeSetComposition", map[string]any{"text": in.Text, "selectionStart": len([]rune(in.Text)), "selectionEnd": len([]rune(in.Text))}, nil)
	case "key":
		combo := in.Key
		for _, m := range in.Mods {
			combo = m + "+" + combo
		}
		return t.key(ctx, combo)
	case "go":
		_, err := t.act(ctx, "goto", ActArgs{URL: in.Text})
		return err
	case "back", "forward", "reload":
		_, err := t.act(ctx, in.Kind, ActArgs{})
		return err
	}
	return errors.New("unknown input kind: " + in.Kind)
}

// Selection is the page's selected text (the pane's ⌘C: the page's clipboard isn't the viewer's).
func (v *View) Selection() string {
	ctx, cancel := context.WithTimeout(v.ctx, 5*time.Second)
	defer cancel()
	raw, err := v.t.eval(ctx, `(() => { const a = document.activeElement; if (a && (a.tagName === "TEXTAREA" || a.tagName === "INPUT") && typeof a.selectionStart === "number") return a.value.slice(a.selectionStart, a.selectionEnd); return String(getSelection() || "") })()`)
	if err != nil {
		return ""
	}
	var s string
	json.Unmarshal(raw, &s)
	return s
}

func clamp01(f float64) float64 { return max(0, min(1, f)) }

// scrollJS scrolls the innermost scrollable box under (x, y), else the page.
func scrollJS(x, y, dx, dy float64) string {
	b, _ := json.Marshal([]float64{x, y, dx, dy})
	return `(([x, y, dx, dy]) => {
  for (let el = document.elementFromPoint(x, y); el && el !== document.documentElement; el = el.parentElement) {
    const s = getComputedStyle(el);
    const canY = dy && /(auto|scroll|overlay)/.test(s.overflowY) && el.scrollHeight > el.clientHeight;
    const canX = dx && /(auto|scroll|overlay)/.test(s.overflowX) && el.scrollWidth > el.clientWidth;
    if (canY || canX) { el.scrollBy({ left: dx, top: dy, behavior: "instant" }); return true }
  }
  scrollBy({ left: dx, top: dy, behavior: "instant" });
  return true
})(` + string(b) + `)`
}

// caster is a tab's screencast, shared by its viewers: Chrome has one per page, so one viewer stopping it (a pane
// reconnecting, a second device leaving) would freeze the others. Started by the first viewer, stopped by the last;
// the width is the latest asked for.
type caster struct {
	t       *Tab
	op      sync.Mutex // start / stop one at a time, so a leaving viewer's stop can't land after a new one's start
	mu      sync.Mutex
	refs    int
	maxW    int
	hidden  bool // the page said it is hidden (a background tab)
	unwatch func()
}

func (c *caster) acquire(ctx context.Context, w int) error {
	c.mu.Lock()
	c.refs++
	first := c.refs == 1
	if first {
		c.unwatch = c.t.watch.add(c.event)
	}
	c.maxW = w
	c.mu.Unlock()
	// The page a viewer looks at is the front tab of its window: a page that opened a new tab (target=_blank) is a
	// background tab otherwise, hidden, and Chrome sends no screencast frames for it (measured: the pane froze on
	// the page that opened a link in a new tab)
	c.t.send(ctx, "Page.bringToFront", nil)
	return c.start(ctx)
}

func (c *caster) width(ctx context.Context, w int) error {
	c.mu.Lock()
	same := c.maxW == w
	c.maxW = w
	c.mu.Unlock()
	if same {
		return nil
	}
	return c.start(ctx)
}

func (c *caster) release() {
	c.mu.Lock()
	c.refs--
	last := c.refs == 0
	var unwatch func()
	if last {
		unwatch, c.unwatch = c.unwatch, nil
	}
	c.mu.Unlock()
	if !last {
		return
	}
	if unwatch != nil {
		unwatch()
	}
	c.op.Lock()
	defer c.op.Unlock()
	c.mu.Lock()
	again := c.refs > 0 // a viewer came back meanwhile: it started the cast, leave it
	c.mu.Unlock()
	if again {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	c.t.send(ctx, "Page.stopScreencast", nil)
	cancel()
}

// start (re)starts the screencast at the current width; Chrome answers with a frame right away.
func (c *caster) start(ctx context.Context) error {
	c.op.Lock()
	defer c.op.Unlock()
	c.mu.Lock()
	w, on := c.maxW, c.refs > 0
	c.mu.Unlock()
	if !on {
		return nil
	}
	// 92: at 80 text in the pane looked soft (a viewer here gets this JPEG as it is); measured at the same speed and
	// about the size of PNG on a text page
	p, _ := json.Marshal(map[string]any{"format": "jpeg", "quality": 92, "maxWidth": w, "maxHeight": w * 4, "everyNthFrame": 1})
	c.t.send(ctx, "Page.stopScreencast", nil)
	_, err := c.t.send(ctx, "Page.startScreencast", p)
	return err
}

func (c *caster) event(method string, params json.RawMessage) {
	switch method {
	case "Page.screencastFrame":
		var p struct {
			SessionID int `json:"sessionId"`
		}
		json.Unmarshal(params, &p)
		go func() {
			time.Sleep(frameGap)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			ack, _ := json.Marshal(map[string]any{"sessionId": p.SessionID})
			c.t.send(ctx, "Page.screencastFrameAck", ack)
		}()
	case "Page.screencastVisibilityChanged":
		// shown again (its tab back in front): start the cast again rather than trust it to resume. Only on the
		// turn from hidden: starting a cast itself reports visible, so restarting on every "visible" would loop
		var p struct {
			Visible bool `json:"visible"`
		}
		json.Unmarshal(params, &p)
		c.mu.Lock()
		back := p.Visible && c.hidden
		c.hidden = !p.Visible
		c.mu.Unlock()
		if back {
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				c.start(ctx)
			}()
		}
	case "Page.frameNavigated":
		var p struct {
			Frame struct {
				ParentID string `json:"parentId"`
			} `json:"frame"`
		}
		json.Unmarshal(params, &p)
		if p.Frame.ParentID == "" {
			// A navigation that swaps the page's renderer process (another site) can end the screencast silently:
			// start it again
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				c.start(ctx)
			}()
		}
	}
}
