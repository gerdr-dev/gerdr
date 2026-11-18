package projbrowser

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gerdr-dev/gerdr/internal/chromeext"
	"github.com/gerdr-dev/gerdr/internal/screen"
)

// TestLive drives a real browser: GERDR_LIVE_BROWSER=1 go test ./internal/projbrowser -run Live -v
func TestLive(t *testing.T) {
	if os.Getenv("GERDR_LIVE_BROWSER") == "" {
		t.Skip("set GERDR_LIVE_BROWSER=1")
	}
	b := chromeext.New(nil)
	m := New(t.TempDir(), b, nil)
	defer m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	id, err := m.Open(ctx, "test", "data:text/html,x", 0, 0)
	if err == nil {
		t.Fatal("data: URL should be refused")
	}
	id, err = m.Open(ctx, "test", "https://example.com/", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Act(ctx, id, "wait", chromeext.ActArgs{Load: true, Timeout: 20000}); err != nil {
		t.Fatal(err)
	}
	res, err := b.Act(ctx, id, "eval", chromeext.ActArgs{Expr: "[navigator.userAgent, innerWidth, innerHeight, devicePixelRatio]"})
	if err != nil {
		t.Fatal(err)
	}
	t.Log("eval:", res.Text)
	res, err = b.Act(ctx, id, "snapshot", chromeext.ActArgs{})
	if err != nil {
		t.Fatal(err)
	}
	t.Log("snapshot:", res.Text)
	v, err := b.View(id)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	v.Size(800, 600)
	start := time.Now()
	img, err := v.Grab(1600)
	if err != nil {
		t.Fatal(err)
	}
	t.Log("frame", img.Bounds(), time.Since(start))
	time.Sleep(300 * time.Millisecond)
	t.Logf("page %+v", v.Page())
	// click the link (fractions of the viewport)
	if err := v.Do(screen.Input{Kind: "text", Text: "hi"}); err != nil {
		t.Fatal(err)
	}
	res, _ = b.Act(ctx, id, "eval", chromeext.ActArgs{Expr: "[innerWidth, innerHeight]"})
	t.Log("after resize:", res.Text)
	if err := v.Do(screen.Input{Kind: "go", Text: "https://www.wikipedia.org/"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second)
	t.Logf("page %+v", v.Page())
	select {
	case <-v.Changed():
	case <-time.After(3 * time.Second):
		t.Fatal("no new frame after navigating")
	}
	img, _ = v.Grab(1600)
	t.Log("frame", img.Bounds())
	if err := b.Close(id, false); err != nil {
		t.Fatal(err)
	}
}

// TestLiveNavigate: frames keep coming after a navigation to another site (the renderer process changes).
func TestLiveNavigate(t *testing.T) {
	if os.Getenv("GERDR_LIVE_BROWSER") == "" {
		t.Skip("set GERDR_LIVE_BROWSER=1")
	}
	b := chromeext.New(nil)
	m := New(t.TempDir(), b, nil)
	defer m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	id, err := m.Open(ctx, "test", "https://example.com/", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	b.Act(ctx, id, "wait", chromeext.ActArgs{Load: true, Timeout: 20000})
	v, err := b.View(id)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	v.Size(600, 760)
	if _, err := v.Grab(1200); err != nil {
		t.Fatal(err)
	}
	frames := func(d time.Duration) int {
		n := 0
		end := time.After(d)
		for {
			select {
			case <-v.Changed():
				n++
			case <-end:
				return n
			}
		}
	}
	t.Log("frames before:", frames(time.Second))
	// a second viewer comes and goes (a pane reconnecting): the first keeps its frames
	v2, err := b.View(id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v2.Grab(1200); err != nil {
		t.Fatal(err)
	}
	v2.Close()
	for _, u := range []string{"https://www.wikipedia.org/", "https://zh.wikipedia.org/wiki/Border_Collie", "https://example.org/"} {
		if err := v.Do(screen.Input{Kind: "go", Text: u}); err != nil {
			t.Fatal(err)
		}
		n := frames(3 * time.Second)
		img, _ := v.Grab(1200)
		t.Logf("%s: %d frames, last %v, page %+v", u, n, img.Bounds(), v.Page())
		if n == 0 {
			t.Error("no frames after navigating to", u)
		}
	}
}

// TestLiveEarlyView: the pane connects while the page is still loading its first URL (it opened from the address bar).
func TestLiveEarlyView(t *testing.T) {
	if os.Getenv("GERDR_LIVE_BROWSER") == "" {
		t.Skip("set GERDR_LIVE_BROWSER=1")
	}
	b := chromeext.New(nil)
	m := New(t.TempDir(), b, nil)
	defer m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	id, err := m.Open(ctx, "test", "https://www.wikipedia.org/", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	v, err := b.View(id)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	go v.Size(600, 760)
	img, err := v.Grab(1200)
	t.Log("first grab", err, img != nil && img.Bounds().Dx() > 0)
	n := 0
	end := time.After(4 * time.Second)
loop:
	for {
		select {
		case <-v.Changed():
			n++
		case <-end:
			break loop
		}
	}
	img, _ = v.Grab(1200)
	t.Logf("%d frames, last %v, page %+v", n, img.Bounds(), v.Page())
	if n < 2 {
		t.Error("frames stopped")
	}
}

// TestLiveTwoWindows: a page keeps painting for its viewer while another window of the same browser gets the input
// (the Gerdr page itself opened in the project browser, or two Browser panes).
func TestLiveTwoWindows(t *testing.T) {
	if os.Getenv("GERDR_LIVE_BROWSER") == "" {
		t.Skip("set GERDR_LIVE_BROWSER=1")
	}
	b := chromeext.New(nil)
	m := New(t.TempDir(), b, nil)
	defer m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	other, err := m.Open(ctx, "test", "https://example.com/", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	id, err := m.Open(ctx, "test", "https://www.wikipedia.org/", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	b.Act(ctx, id, "wait", chromeext.ActArgs{Load: true, Timeout: 20000})
	v, err := b.View(id)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	v.Size(600, 760)
	v.Grab(1200)
	frames := func(d time.Duration) int {
		n := 0
		end := time.After(d)
		for {
			select {
			case <-v.Changed():
				n++
			case <-end:
				return n
			}
		}
	}
	// input into the other window, as the Gerdr page gets it
	b.Act(ctx, other, "click", chromeext.ActArgs{Target: "100,100"})
	// a search typed into the page and submitted: a navigation the page makes itself
	b.Act(ctx, id, "fill", chromeext.ActArgs{Target: "#searchInput", Text: "边境牧羊犬"})
	v.Do(screen.Input{Kind: "key", Key: "Enter"})
	n := frames(5 * time.Second)
	t.Logf("after submit: %d frames, page %+v", n, v.Page())
	if n == 0 {
		t.Error("no frames")
	}
}

// TestLiveFrameCost: where a frame's time goes (decode the screencast JPEG, diff, encode the changed tiles).
func TestLiveFrameCost(t *testing.T) {
	if os.Getenv("GERDR_LIVE_BROWSER") == "" {
		t.Skip("set GERDR_LIVE_BROWSER=1")
	}
	b := chromeext.New(nil)
	m := New(t.TempDir(), b, nil)
	defer m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	id, err := m.Open(ctx, "test", "https://zh.wikipedia.org/wiki/Border_Collie", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	b.Act(ctx, id, "wait", chromeext.ActArgs{Load: true, Timeout: 20000})
	v, _ := b.View(id)
	defer v.Close()
	v.Size(700, 900)
	time.Sleep(500 * time.Millisecond)
	var d screen.Differ
	for i := 0; i < 6; i++ {
		t0 := time.Now()
		img, err := v.Grab(1400)
		if err != nil {
			t.Fatal(err)
		}
		t1 := time.Now()
		rects, payload, full, _ := d.Diff(img, 70)
		t2 := time.Now()
		t.Logf("grab+decode %v, diff+encode %v, %d rects, %d KB, full=%v, %v", t1.Sub(t0), t2.Sub(t1), len(rects), len(payload)/1024, full, img.Bounds())
		v.Do(screen.Input{Kind: "scroll", X: 0.5, Y: 0.5, DYPx: 300})
		time.Sleep(200 * time.Millisecond)
	}
}

// TestLiveInputLatency: from an input to the first new frame (what a viewer on this computer waits, raw mode).
func TestLiveInputLatency(t *testing.T) {
	if os.Getenv("GERDR_LIVE_BROWSER") == "" {
		t.Skip("set GERDR_LIVE_BROWSER=1")
	}
	b := chromeext.New(nil)
	m := New(t.TempDir(), b, nil)
	defer m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	id, err := m.Open(ctx, "test", "https://zh.wikipedia.org/wiki/Border_Collie", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	b.Act(ctx, id, "wait", chromeext.ActArgs{Load: true, Timeout: 20000})
	v, _ := b.View(id)
	defer v.Close()
	v.Size(700, 900)
	_, _, _, seq, err := v.Raw(1400)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	for i := 0; i < 5; i++ {
		_, _, _, seq, _ = v.Raw(1400)
		t0 := time.Now()
		v.Do(screen.Input{Kind: "scroll", X: 0.5, Y: 0.5, DYPx: 200})
		for {
			select {
			case <-v.Changed():
			case <-time.After(2 * time.Second):
				t.Fatal("no frame")
			}
			jpg, w, h, s, _ := v.Raw(1400)
			if s != seq {
				t.Logf("scroll → new frame %v (%dx%d, %d KB)", time.Since(t0).Round(time.Millisecond), w, h, len(jpg)/1024)
				break
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// TestLiveNewTab: a page opening a link in a new tab (same window) becomes a background tab, hidden, with no
// screencast frames: the new tab is adopted with its opener, and a viewer of the opener brings it back to front.
func TestLiveNewTab(t *testing.T) {
	if os.Getenv("GERDR_LIVE_BROWSER") == "" {
		t.Skip("set GERDR_LIVE_BROWSER=1")
	}
	b := chromeext.New(nil)
	m := New(t.TempDir(), b, nil)
	defer m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	id, err := m.Open(ctx, "test", "https://example.com/", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	b.Act(ctx, id, "wait", chromeext.ActArgs{Load: true, Timeout: 20000})
	b.Act(ctx, id, "eval", chromeext.ActArgs{Expr: `(() => { const a = document.createElement("a"); a.href = "https://example.org/"; a.target = "_blank"; a.id = "nt"; a.textContent = "NEW TAB"; a.style = "position:fixed;left:0;top:0;font-size:40px"; document.body.append(a) })()`})
	if _, err := b.Act(ctx, id, "click", chromeext.ActArgs{Target: "20,20"}); err != nil {
		t.Fatal(err)
	}
	var popup chromeext.Tab
	for i := 0; i < 50 && popup.ID == 0; i++ {
		time.Sleep(100 * time.Millisecond)
		for _, x := range b.Tabs() {
			if x.ID != id {
				popup = x
			}
		}
	}
	if popup.Opener != id {
		t.Fatalf("new tab %+v, want opener %d", popup, id)
	}
	vis, _ := b.Act(ctx, id, "eval", chromeext.ActArgs{Expr: "document.visibilityState"})
	t.Log("opener after the new tab:", vis.Text)
	v, err := b.View(id)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	if _, err := v.Grab(1000); err != nil {
		t.Fatal("viewing the opener:", err)
	}
	vis, _ = b.Act(ctx, id, "eval", chromeext.ActArgs{Expr: "document.visibilityState"})
	t.Log("opener while viewed:", vis.Text)
	if !strings.Contains(vis.Text, "visible") || strings.Contains(vis.Text, "hidden") {
		t.Error("the viewed page should be in front")
	}
}

// TestLiveEditable: the page says when its focus is in a field (a phone's keyboard follows it).
func TestLiveEditable(t *testing.T) {
	if os.Getenv("GERDR_LIVE_BROWSER") == "" {
		t.Skip("set GERDR_LIVE_BROWSER=1")
	}
	b := chromeext.New(nil)
	m := New(t.TempDir(), b, nil)
	defer m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	id, _ := m.Open(ctx, "test", "https://example.com/", 800, 600)
	b.Act(ctx, id, "wait", chromeext.ActArgs{Load: true, Timeout: 20000})
	b.Act(ctx, id, "eval", chromeext.ActArgs{Expr: `(() => { const i = document.createElement("input"); i.style = "position:fixed;left:0;top:0;width:400px;height:100px"; document.body.append(i) })()`})
	v, _ := b.View(id)
	defer v.Close()
	v.Grab(800)
	editable := func() bool {
		time.Sleep(400 * time.Millisecond)
		return v.Page().Editable
	}
	v.Do(screen.Input{Kind: "click", X: 0.1, Y: 0.05})
	if !editable() {
		t.Error("focus in the input: want editable")
	}
	v.Do(screen.Input{Kind: "click", X: 0.5, Y: 0.8})
	if editable() {
		t.Error("clicked outside: want not editable")
	}
}
