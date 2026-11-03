package screen

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
	"unsafe"

	"github.com/ebitengine/purego"
)

type cgPoint struct{ X, Y float64 }
type cgSize struct{ W, H float64 }
type cgRect struct {
	Origin cgPoint
	Size   cgSize
}

// CoreGraphics, bound once on first use. A nil function after load means the symbol was missing (an old macOS).
var (
	loadOnce sync.Once
	loadErr  error

	cgMainDisplayID      func() uint32
	cgDisplayBounds      func(uint32) cgRect
	cgEventCreateMouse   func(src uintptr, typ uint32, pos cgPoint, button uint32) uintptr
	cgEventCreateKey     func(src uintptr, key uint16, down bool) uintptr
	cgEventSetUnicode    func(ev uintptr, n uint64, s *uint16)
	cgEventSetFlags      func(ev uintptr, flags uint64)
	cgEventSetIntField   func(ev uintptr, field uint32, v int64)
	cgEventCreateScroll2 func(src uintptr, units uint32, count uint32, w1, w2, w3 int32) uintptr
	cgEventPost          func(tap uint32, ev uintptr)
	cgPreflightCapture   func() bool
	cgRequestCapture     func() bool
	cgPreflightPost      func() bool
	cgRequestPost        func() bool
	cfRelease            func(uintptr)
	cgEventCreate        func(src uintptr) uintptr
	cgEventGetLocation   func(ev uintptr) cgPoint

	// Grabbing pixels in-process (~20 ms for a 4K screen against screencapture's ~130 ms plus a file round trip).
	// CGDisplayCreateImage is deprecated in favour of ScreenCaptureKit (an async Objective-C API, out of reach
	// without cgo) but still exported on macOS 26; when it goes, cgDisplayCreateImage stays nil and Grab falls
	// back to screencapture.
	cgDisplayCreateImage   func(display uint32) uintptr
	cgImageGetWidth        func(uintptr) uint64
	cgImageGetHeight       func(uintptr) uint64
	cgImageGetBytesPerRow  func(uintptr) uint64
	cgImageGetBitsPerPixel func(uintptr) uint64
	cgImageGetBitsPerComp  func(uintptr) uint64
	cgImageGetBitmapInfo   func(uintptr) uint32
	cgImageGetDataProvider func(uintptr) uintptr
	cgDataProviderCopyData func(uintptr) uintptr
	cfDataGetBytePtr       func(uintptr) unsafe.Pointer
	cfDataGetLength        func(uintptr) int64
)

func load() error {
	loadOnce.Do(func() {
		cg, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			loadErr = err
			return
		}
		cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			loadErr = err
			return
		}
		// RegisterLibFunc panics on a missing symbol; turn that into an error (CGEventCreateScrollWheelEvent2 is
		// macOS 13+, the preflight / request pairs 10.15+).
		defer func() {
			if r := recover(); r != nil {
				loadErr = fmt.Errorf("CoreGraphics: %v", r)
			}
		}()
		purego.RegisterLibFunc(&cgMainDisplayID, cg, "CGMainDisplayID")
		purego.RegisterLibFunc(&cgDisplayBounds, cg, "CGDisplayBounds")
		purego.RegisterLibFunc(&cgEventCreateMouse, cg, "CGEventCreateMouseEvent")
		purego.RegisterLibFunc(&cgEventCreateKey, cg, "CGEventCreateKeyboardEvent")
		purego.RegisterLibFunc(&cgEventSetUnicode, cg, "CGEventKeyboardSetUnicodeString")
		purego.RegisterLibFunc(&cgEventSetFlags, cg, "CGEventSetFlags")
		purego.RegisterLibFunc(&cgEventSetIntField, cg, "CGEventSetIntegerValueField")
		// Not CGEventCreateScrollWheelEvent: it is variadic, and Apple's arm64 ABI puts variadic arguments on the stack,
		// which purego's plain call doesn't.
		purego.RegisterLibFunc(&cgEventCreateScroll2, cg, "CGEventCreateScrollWheelEvent2")
		purego.RegisterLibFunc(&cgEventPost, cg, "CGEventPost")
		purego.RegisterLibFunc(&cgPreflightCapture, cg, "CGPreflightScreenCaptureAccess")
		purego.RegisterLibFunc(&cgRequestCapture, cg, "CGRequestScreenCaptureAccess")
		purego.RegisterLibFunc(&cgPreflightPost, cg, "CGPreflightPostEventAccess")
		purego.RegisterLibFunc(&cgRequestPost, cg, "CGRequestPostEventAccess")
		purego.RegisterLibFunc(&cfRelease, cf, "CFRelease")
		purego.RegisterLibFunc(&cgEventCreate, cg, "CGEventCreate")
		purego.RegisterLibFunc(&cgEventGetLocation, cg, "CGEventGetLocation")
		if _, err := purego.Dlsym(cg, "CGDisplayCreateImage"); err == nil {
			purego.RegisterLibFunc(&cgDisplayCreateImage, cg, "CGDisplayCreateImage")
			purego.RegisterLibFunc(&cgImageGetWidth, cg, "CGImageGetWidth")
			purego.RegisterLibFunc(&cgImageGetHeight, cg, "CGImageGetHeight")
			purego.RegisterLibFunc(&cgImageGetBytesPerRow, cg, "CGImageGetBytesPerRow")
			purego.RegisterLibFunc(&cgImageGetBitsPerPixel, cg, "CGImageGetBitsPerPixel")
			purego.RegisterLibFunc(&cgImageGetBitsPerComp, cg, "CGImageGetBitsPerComponent")
			purego.RegisterLibFunc(&cgImageGetBitmapInfo, cg, "CGImageGetBitmapInfo")
			purego.RegisterLibFunc(&cgImageGetDataProvider, cg, "CGImageGetDataProvider")
			purego.RegisterLibFunc(&cgDataProviderCopyData, cg, "CGDataProviderCopyData")
			purego.RegisterLibFunc(&cfDataGetBytePtr, cf, "CFDataGetBytePtr")
			purego.RegisterLibFunc(&cfDataGetLength, cf, "CFDataGetLength")
		}
	})
	return loadErr
}

// CoreGraphics constants (CGEventTypes.h).
const (
	tapHID = 0 // kCGHIDEventTap

	evLeftDown  = 1
	evLeftUp    = 2
	evRightDown = 3
	evRightUp   = 4
	evMoved     = 5

	btnLeft  = 0
	btnRight = 1

	fieldClickState = 1 // kCGMouseEventClickState: 2 on the second click of a double click
	scrollUnitLine  = 1

	flagShift = 0x20000
	flagCtrl  = 0x40000
	flagOpt   = 0x80000
	flagCmd   = 0x100000
)

// mu: one action at a time, so a double click's events and a typed string's keys don't interleave with another's.
var mu sync.Mutex

func GetStatus() Status {
	if err := load(); err != nil {
		return Status{}
	}
	b := cgDisplayBounds(cgMainDisplayID())
	return Status{Supported: true, Screen: cgPreflightCapture(), Input: cgPreflightPost(), Width: b.Size.W, Height: b.Size.H}
}

// RequestAccess asks macOS for the two grants this needs: each call shows the system prompt the first time and puts
// gerdr in the list in System Settings; the pane of whatever is still missing is opened, since after the first
// prompt macOS only lets the user switch it on there. Local only (the prompts show on this computer's screen).
func RequestAccess() Status {
	if load() != nil {
		return Status{}
	}
	if !cgPreflightCapture() {
		cgRequestCapture()
	}
	if !cgPreflightPost() {
		cgRequestPost()
	}
	st := GetStatus()
	if !st.Screen {
		_ = exec.Command("/usr/bin/open", "x-apple.systempreferences:com.apple.preference.security?Privacy_ScreenCapture").Run()
	} else if !st.Input {
		_ = exec.Command("/usr/bin/open", "x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility").Run()
	}
	return st
}

var (
	wakeMu   sync.Mutex
	lastWake time.Time
)

// wake declares user activity so a display that went to sleep lights up: otherwise the screenshot is black and
// the user can't tell why. While someone watches, this also keeps it from dimming. At most every 20s (it starts
// a process).
func wake() {
	wakeMu.Lock()
	defer wakeMu.Unlock()
	if time.Since(lastWake) < 20*time.Second {
		return
	}
	lastWake = time.Now()
	c := exec.Command("/usr/bin/caffeinate", "-u", "-t", "2")
	if c.Start() == nil {
		go func() { _ = c.Wait() }()
	}
}

// Grab captures the main display shrunk to at most maxWidth pixels wide (0 = full size).
func Grab(maxWidth int) (*image.RGBA, error) {
	if err := load(); err != nil {
		return nil, err
	}
	if !cgPreflightCapture() {
		return nil, errors.New("Screen Recording isn't granted to Gerdr")
	}
	wake()
	if m, ok := grabCG(maxWidth); ok {
		return m, nil
	}
	return grabScreencapture(maxWidth)
}

// CGBitmapInfo bits (CGImage.h)
const (
	alphaMask      = 0x1f
	alphaLast      = 3 // kCGImageAlphaNoneSkipLast / PremultipliedLast / Last: 1, 3, 5
	byteOrderMask  = 0x7000
	byteOrder32Lit = 2 << 12
)

func grabCG(maxWidth int) (*image.RGBA, bool) {
	if cgDisplayCreateImage == nil {
		return nil, false
	}
	img := cgDisplayCreateImage(cgMainDisplayID())
	if img == 0 {
		return nil, false
	}
	defer cfRelease(img)
	if cgImageGetBitsPerPixel(img) != 32 || cgImageGetBitsPerComp(img) != 8 {
		return nil, false // HDR (16-bit / float) buffers: let screencapture convert them
	}
	data := cgDataProviderCopyData(cgImageGetDataProvider(img))
	if data == 0 {
		return nil, false
	}
	defer cfRelease(data)
	w, h, stride := int(cgImageGetWidth(img)), int(cgImageGetHeight(img)), int(cgImageGetBytesPerRow(img))
	n := int(cfDataGetLength(data))
	if w == 0 || h == 0 || n < stride*(h-1)+w*4 {
		return nil, false
	}
	// Read in place: shrink copies out what it needs before data is released
	src := raw{pix: unsafe.Slice((*byte)(cfDataGetBytePtr(data)), n), w: w, h: h, stride: stride}
	info := cgImageGetBitmapInfo(img)
	alpha := info & alphaMask
	last := alpha == 1 || alpha == 3 || alpha == 5
	little := info&byteOrderMask == byteOrder32Lit
	switch {
	case little && !last: // B G R A in memory: the usual screen buffer
		src.r, src.g, src.b = 2, 1, 0
	case little && last: // A B G R
		src.r, src.g, src.b = 3, 2, 1
	case !little && !last: // A R G B
		src.r, src.g, src.b = 1, 2, 3
	default: // R G B A
		src.r, src.g, src.b = 0, 1, 2
	}
	return shrink(src, maxWidth), true
}

func grabScreencapture(maxWidth int) (*image.RGBA, error) {
	f, err := os.CreateTemp("", "gerdr-screen-*.png")
	if err != nil {
		return nil, err
	}
	p := f.Name()
	f.Close()
	defer os.Remove(p)
	// -x no sound, -m main display only
	if out, err := exec.Command("/usr/sbin/screencapture", "-x", "-m", "-t", "png", p).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("screencapture: %v %s", err, strings.TrimSpace(string(out)))
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	m, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("screencapture wrote no image: %v", err)
	}
	return shrink(fromImage(m), maxWidth), nil
}

// Cursor is where the pointer is, as fractions of the main display; ok is false when it is on another display.
func Cursor() (fx, fy float64, ok bool) {
	if load() != nil {
		return 0, 0, false
	}
	ev := cgEventCreate(0)
	if ev == 0 {
		return 0, 0, false
	}
	p := cgEventGetLocation(ev)
	cfRelease(ev)
	b := cgDisplayBounds(cgMainDisplayID())
	if b.Size.W <= 0 || b.Size.H <= 0 {
		return 0, 0, false
	}
	fx, fy = (p.X-b.Origin.X)/b.Size.W, (p.Y-b.Origin.Y)/b.Size.H
	return fx, fy, fx >= 0 && fx <= 1 && fy >= 0 && fy <= 1
}

func ready() error {
	if err := load(); err != nil {
		return err
	}
	if !cgPreflightPost() {
		return errors.New("Accessibility isn't granted to Gerdr (input would be dropped)")
	}
	return nil
}

func post(ev uintptr) {
	if ev == 0 {
		return
	}
	cgEventPost(tapHID, ev)
	cfRelease(ev)
}

func point(fx, fy float64) cgPoint {
	b := cgDisplayBounds(cgMainDisplayID())
	return cgPoint{b.Origin.X + fx*b.Size.W, b.Origin.Y + fy*b.Size.H}
}

func click(fx, fy float64, right bool, count int) error {
	if err := ready(); err != nil {
		return err
	}
	mu.Lock()
	defer mu.Unlock()
	pos := point(fx, fy)
	down, up, btn := uint32(evLeftDown), uint32(evLeftUp), uint32(btnLeft)
	if right {
		down, up, btn = evRightDown, evRightUp, btnRight
	}
	// Move there first: hover-dependent UIs (menus, toolbars that appear on hover) see the pointer arrive.
	post(cgEventCreateMouse(0, evMoved, pos, btn))
	time.Sleep(30 * time.Millisecond)
	for i := 1; i <= count; i++ {
		for _, t := range []uint32{down, up} {
			ev := cgEventCreateMouse(0, t, pos, btn)
			if ev != 0 {
				cgEventSetIntField(ev, fieldClickState, int64(i))
			}
			post(ev)
			time.Sleep(20 * time.Millisecond)
		}
	}
	return nil
}

func scroll(fx, fy float64, dy int) error {
	if err := ready(); err != nil {
		return err
	}
	mu.Lock()
	defer mu.Unlock()
	// The wheel scrolls whatever is under the pointer
	post(cgEventCreateMouse(0, evMoved, point(fx, fy), btnLeft))
	time.Sleep(20 * time.Millisecond)
	post(cgEventCreateScroll2(0, scrollUnitLine, 1, int32(max(-50, min(50, dy))), 0, 0))
	return nil
}

func typeText(s string) error {
	if err := ready(); err != nil {
		return err
	}
	if len(s) > 4000 {
		return errors.New("text too long")
	}
	mu.Lock()
	defer mu.Unlock()
	for _, r := range s {
		if r == '\n' {
			key(keyCodes["enter"], 0)
			continue
		}
		if r == '\r' {
			continue
		}
		// One character per key event: some apps read only the first unit of a longer string. The key code stays 0
		// (the event carries the character), flags 0 so a Shift held on the Mac doesn't change it.
		u := utf16.Encode([]rune{r})
		for _, down := range []bool{true, false} {
			ev := cgEventCreateKey(0, 0, down)
			if ev == 0 {
				continue
			}
			cgEventSetUnicode(ev, uint64(len(u)), &u[0])
			cgEventSetFlags(ev, 0)
			post(ev)
		}
		time.Sleep(8 * time.Millisecond)
	}
	return nil
}

// key presses one key with the modifier flags set on it.
func key(code uint16, flags uint64) {
	for _, down := range []bool{true, false} {
		ev := cgEventCreateKey(0, code, down)
		if ev == 0 {
			continue
		}
		cgEventSetFlags(ev, flags)
		post(ev)
		time.Sleep(15 * time.Millisecond)
	}
}

var modKeys = []struct {
	name string
	code uint16
	flag uint64
}{{"cmd", 55, flagCmd}, {"shift", 56, flagShift}, {"opt", 58, flagOpt}, {"ctrl", 59, flagCtrl}}

func pressKey(name string, mods []string) error {
	code, ok := keyCodes[strings.ToLower(name)]
	if !ok {
		return errors.New("unknown key: " + name)
	}
	if err := ready(); err != nil {
		return err
	}
	mu.Lock()
	defer mu.Unlock()
	// Modifier keys go down and up as keys of their own around the key, not only as flags on it: some shortcuts
	// (⌘Space for Spotlight, ⌘Tab) are read by the system from the modifier key events.
	var flags uint64
	var held []uint16
	for _, m := range modKeys {
		for _, want := range mods {
			if strings.EqualFold(want, m.name) {
				flags |= m.flag
				if ev := cgEventCreateKey(0, m.code, true); ev != 0 {
					cgEventSetFlags(ev, flags)
					post(ev)
				}
				held = append(held, m.code)
			}
		}
	}
	time.Sleep(15 * time.Millisecond)
	key(code, flags)
	for i := len(held) - 1; i >= 0; i-- {
		flags &^= modKeys[indexOfMod(held[i])].flag
		if ev := cgEventCreateKey(0, held[i], false); ev != 0 {
			cgEventSetFlags(ev, flags)
			post(ev)
		}
	}
	return nil
}

func indexOfMod(code uint16) int {
	for i, m := range modKeys {
		if m.code == code {
			return i
		}
	}
	return 0
}

// keyCodes: macOS virtual key codes (ANSI layout positions, Events.h). Letters and digits are for shortcuts
// (⌘A, ⌘W…); plain text goes through typeText, which doesn't depend on the keyboard layout.
var keyCodes = map[string]uint16{
	"enter": 36, "tab": 48, "space": 49, "backspace": 51, "esc": 53, "delete": 117,
	"left": 123, "right": 124, "down": 125, "up": 126, "home": 115, "end": 119, "pageup": 116, "pagedown": 121,
	"a": 0, "s": 1, "d": 2, "f": 3, "h": 4, "g": 5, "z": 6, "x": 7, "c": 8, "v": 9, "b": 11, "q": 12, "w": 13,
	"e": 14, "r": 15, "y": 16, "t": 17, "1": 18, "2": 19, "3": 20, "4": 21, "6": 22, "5": 23, "=": 24, "9": 25,
	"7": 26, "-": 27, "8": 28, "0": 29, "]": 30, "o": 31, "u": 32, "[": 33, "i": 34, "p": 35, "l": 37, "j": 38,
	"'": 39, "k": 40, ";": 41, "\\": 42, ",": 43, "/": 44, "n": 45, "m": 46, ".": 47, "`": 50,
}
