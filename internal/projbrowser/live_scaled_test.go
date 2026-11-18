package projbrowser

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/gerdr-dev/gerdr/internal/chromeext"
	"github.com/gerdr-dev/gerdr/internal/screen"
)

// TestLiveScaledClick: a page with no mobile viewport, laid out as a phone (the App's page seen from a phone, phone.go):
// the browser fits its ~980 px layout into the phone's width, and a tap on the picture must land where it shows.
func TestLiveScaledClick(t *testing.T) {
	if os.Getenv("GERDR_LIVE_BROWSER") == "" {
		t.Skip("set GERDR_LIVE_BROWSER=1")
	}
	b := chromeext.New(nil)
	m := New(t.TempDir(), b, nil)
	defer m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	// a page with no viewport meta, 980 px wide as a phone lays such pages out
	id, _ := m.Open(ctx, "test", "https://example.com/", 1280, 900)
	b.Act(ctx, id, "wait", chromeext.ActArgs{Load: true, Timeout: 20000})
	b.Act(ctx, id, "eval", chromeext.ActArgs{Expr: `(() => { document.querySelector("meta[name=viewport]")?.remove(); document.body.innerHTML = '<div style="width:960px"><button id=b style="margin-left:600px;margin-top:300px;width:200px;height:80px" onclick="this.dataset.n=+(this.dataset.n||0)+1">x</button></div>' })()`})
	raw, _ := json.Marshal(map[string]any{"width": 400, "height": 760, "deviceScaleFactor": 0, "mobile": true})
	b.Act(ctx, id, "cdp", chromeext.ActArgs{Method: "Emulation.setDeviceMetricsOverride", Params: raw})
	time.Sleep(500 * time.Millisecond)
	v, _ := b.View(id)
	defer v.Close()
	v.Grab(800)
	time.Sleep(300 * time.Millisecond)
	res, _ := b.Act(ctx, id, "eval", chromeext.ActArgs{Expr: `(() => { const r = document.getElementById("b").getBoundingClientRect(); return [innerWidth, innerHeight, visualViewport.scale, (r.left + r.width/2) / innerWidth, (r.top + r.height/2) / innerHeight] })()`})
	var f []float64
	json.Unmarshal([]byte(res.Text), &f)
	t.Logf("layout %vx%v, the picture %v; the button at %.2f,%.2f", f[0], f[1], v.Viewport(), f[3], f[4])
	v.Do(screen.Input{Kind: "click", X: f[3], Y: f[4]})
	time.Sleep(300 * time.Millisecond)
	n0, _ := b.Act(ctx, id, "eval", chromeext.ActArgs{Expr: `document.getElementById("b").dataset.n || "0"`})
	t.Logf("click through the pane's mapping: %s", n0.Text)
	if n0.Text != "1" {
		t.Error("the pane's click missed the button")
	}

}
