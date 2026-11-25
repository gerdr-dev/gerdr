package server

import (
	"encoding/json"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIconError(t *testing.T) {
	ok := [][2]string{{"", ""}, {"rocket", "teal"}, {"sprite:cat", ""}, {"text:项目", "#e11d48"}, {"text:abc", ""}, {"data:image/png;base64,AAAA", ""}}
	for _, c := range ok {
		if err := IconError(c[0], c[1]); err != nil {
			t.Errorf("%q %q refused: %v", c[0], c[1], err)
		}
	}
	bad := [][2]string{{"text:", ""}, {"text:abcd", ""}, {"data:text/html;base64,AAAA", ""}, {"data:image/png;base64," + strings.Repeat("A", maxIconBytes), ""}, {"a b", ""}, {"rocket", "#fff"}, {"rocket", "url(x)"}}
	for _, c := range bad {
		if IconError(c[0], c[1]) == nil {
			t.Errorf("%.40q %q accepted", c[0], c[1])
		}
	}
}

func TestIconFromImage(t *testing.T) {
	p := filepath.Join(t.TempDir(), "wide.png")
	f, _ := os.Create(p)
	_ = png.Encode(f, image.NewRGBA(image.Rect(0, 0, 400, 200)))
	f.Close()
	ic, err := IconFromImage(p)
	if err != nil || IconError(ic, "") != nil {
		t.Fatalf("icon %.40q: %v / %v", ic, err, IconError(ic, ""))
	}
	if _, err := IconFromImage(filepath.Join(t.TempDir(), "none.png")); err == nil {
		t.Error("read a missing file")
	}
}

func TestProjectIcon(t *testing.T) {
	raw, res := apply(t, opsLayout, call("project.icon", LayoutParams{ID: "default", Icon: "text:我", Color: "teal"}))
	b, _ := json.Marshal(res)
	if !strings.Contains(string(b), `"icon":"text:我"`) || !strings.Contains(string(b), `"color":"teal"`) {
		t.Fatalf("result: %s", b)
	}
	// Back to its initials: both keys go
	raw2, _ := apply(t, raw, call("project.icon", LayoutParams{ID: "default"}))
	if strings.Contains(raw2, `"icon"`) || strings.Contains(raw2, `"teal"`) {
		t.Errorf("icon kept: %s", raw2)
	}
	if _, _, err := ApplyLayout(json.RawMessage(opsLayout), opsSessions, "A", "default", "project.icon", LayoutParams{ID: "nope", Icon: "rocket"}); err == nil {
		t.Error("set an icon on a missing project")
	}
	if _, _, err := ApplyLayout(json.RawMessage(opsLayout), opsSessions, "A", "default", "project.icon", LayoutParams{ID: "default", Icon: "text:toolong"}); err == nil {
		t.Error("took a bad icon")
	}
}
