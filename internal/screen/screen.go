// Package screen is Settings → Labs → Remote screen: look at this computer's main display and click / type on it
// from another device (a phone through the relay), e.g. to open an app and type its password. Not a remote desktop:
// one still frame at a time, refreshed after each action, which is all that needs.
//
// macOS only (screen_darwin.go). The screenshot is CGDisplayCreateImage (screencapture as a fallback), shrunk and
// diffed here (scale.go, diff.go); the input is CoreGraphics events posted from this process. CoreGraphics is
// called through purego (no cgo: the release builds cross-compile). Both need a privacy grant
// for the process that runs gerdr (Screen Recording, Accessibility). gerdr is ad-hoc signed, and TCC keys an
// ad-hoc binary's grant by its hash, so a new backend may need granting again: Status says so, the UI shows it.
package screen

import (
	"bytes"
	"errors"
	"image/jpeg"
)

// Status is what the UI needs to say why it can't work yet.
type Status struct {
	Supported bool `json:"supported"` // this OS has an implementation
	Screen    bool `json:"screen"`    // Screen Recording granted (without it screencapture fails or shows only the wallpaper)
	Input     bool `json:"input"`     // Accessibility granted (without it posted events are dropped silently)
	// The main display in points (what events are addressed in); the screenshot has the same aspect ratio.
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// Input is one action from the UI. X and Y are fractions of the main display (0..1), so the client never needs to
// know the display's points or the screenshot's scale.
type Input struct {
	Kind   string   `json:"kind"`   // click | scroll | text | key
	X      float64  `json:"x"`      // click, scroll
	Y      float64  `json:"y"`      // click, scroll
	Button string   `json:"button"` // click: left (default) | right
	Count  int      `json:"count"`  // click: 1 (default) or 2
	DY     int      `json:"dy"`     // scroll: lines, positive = content moves down (wheel up)
	Text   string   `json:"text"`   // text: typed as is (a newline presses Return)
	Key    string   `json:"key"`    // key: a name from keyCodes (enter, tab, esc, a, space…)
	Mods   []string `json:"mods"`   // key: cmd | shift | opt | ctrl
}

var ErrUnsupported = errors.New("remote screen only works on macOS")

// Do runs one input.
func Do(in Input) error {
	switch in.Kind {
	case "click":
		if in.Count < 1 {
			in.Count = 1
		}
		return click(clamp(in.X), clamp(in.Y), in.Button == "right", min(in.Count, 2))
	case "scroll":
		return scroll(clamp(in.X), clamp(in.Y), in.DY)
	case "text":
		return typeText(in.Text)
	case "key":
		return pressKey(in.Key, in.Mods)
	}
	return errors.New("unknown input kind: " + in.Kind)
}

func clamp(f float64) float64 {
	return max(0, min(1, f))
}

// Capture is one whole JPEG of the main display, no wider than maxWidth, at quality q (the plain HTTP endpoint;
// the viewer streams diffs instead).
func Capture(maxWidth, q int) ([]byte, error) {
	m, err := Grab(maxWidth)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, m, &jpeg.Options{Quality: max(1, min(100, q))}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
