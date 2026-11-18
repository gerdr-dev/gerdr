package server

import (
	"encoding/json"
	"image"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/gorilla/websocket"

	"github.com/gerdr-dev/gerdr/internal/screen"
)

// screenUpgrader: like /ws's, but without permessage-deflate: the frames are JPEG, which doesn't compress, and
// deflating them costs CPU on the latency path.
var screenUpgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 64 << 10,
	CheckOrigin: func(r *http.Request) bool {
		o := r.Header.Get("Origin")
		if o == "" {
			return true
		}
		u, err := url.Parse(o)
		return err == nil && u.Host == r.Host
	},
}

// After an input the screen is captured at these offsets: most UIs redraw within a frame or two (the 40 ms one
// shows a click's result), menus and pages take longer, an app launching longer still. Then every idleEvery.
var screenBurst = []time.Duration{40, 150, 300, 600, 1000, 1600, 2500}

const screenIdleEvery = time.Second

// pictureSource is what the stream shows and where its input goes: this computer's display (the remote screen) or a
// browser tab (the Browser pane, chromeext.View).
type pictureSource interface {
	Grab(maxWidth int) (*image.RGBA, error)
	Cursor() (fx, fy float64, ok bool)
	Do(in screen.Input) error
	Off() string              // non-empty: it can't be shown any more, and why
	Changed() <-chan struct{} // fires when there is something new to capture; nil = capture on the schedule only
	Page() *screen.Page       // a browser tab's address bar; nil for the display
	Size(w, h int)            // the viewer's box in CSS px (a browser tab lays its page out at that size)
	Selection() string        // the selected text, for the viewer's copy
}

// desktopSource: this computer's main display (internal/screen).
type desktopSource struct{ s *Server }

func (d desktopSource) Grab(w int) (*image.RGBA, error)  { return screen.Grab(w) }
func (d desktopSource) Cursor() (float64, float64, bool) { return screen.Cursor() }
func (d desktopSource) Do(in screen.Input) error         { return screen.Do(in) }
func (d desktopSource) Changed() <-chan struct{}         { return nil }
func (d desktopSource) Page() *screen.Page               { return nil }
func (d desktopSource) Size(int, int)                    {}
func (d desktopSource) Selection() string                { return "" }
func (d desktopSource) Off() string {
	if !d.s.screenOn() {
		return "remote screen is off (Settings → Remote access)"
	}
	return ""
}

// screenStream: GET /api/screen/stream?w=&q= (WebSocket). The server pushes the picture as diffs against what this
// viewer already has (internal/screen/diff.go): one binary message per capture that changed something, nothing
// while the screen is still. The page sends JSON:
//
//	{t:"input", seq, input}  run it, then capture on the screenBurst schedule; frames carry ack = seq
//	{t:"cfg", w, q, vw, vh}  new size / quality (vw / vh: the viewer's box, for a browser tab): the next frame is a full one
//	{t:"full"}               resend the whole picture (the refresh button)
//	{t:"copy"}               a browser tab: send the selected text back (header.text)
//
// Input never reaches the log: typed text is often a password.
func (s *Server) screenStream(w http.ResponseWriter, r *http.Request) {
	if !s.screenOn() {
		httpError(w, http.StatusForbidden, "remote screen is off (Settings → Remote access)")
		return
	}
	conn, err := screenUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	streamPictures(conn, r, desktopSource{s}, false)
}

// rawSource: a source that already has each picture as a JPEG (a browser tab's screencast, chromeext.View.Raw).
type rawSource interface {
	Raw(maxWidth int) (jpg []byte, w, h int, seq int64, err error)
}

// streamPictures runs one viewer's stream. raw: send a rawSource's JPEGs as they are, whole, instead of diffing
// tiles: for a viewer on this computer, where bandwidth is free and decoding / re-encoding was most of the latency
// (docs/dev/BROWSER-PANE.md).
func streamPictures(conn *websocket.Conn, r *http.Request, src pictureSource, raw bool) {
	type cfg struct{ w, q int }
	parse := func(w, q int) cfg {
		c := cfg{1600, 60}
		if w > 0 && w <= 4000 {
			c.w = w
		}
		if q > 0 && q <= 100 {
			c.q = q
		}
		return c
	}
	kicks := make(chan int64, 8)    // an input finished (its seq)
	cfgs := make(chan cfg, 4)       // new size / quality
	fulls := make(chan struct{}, 1) // resend everything
	errs := make(chan string, 4)    // an input failed: told to the page
	texts := make(chan string, 2)   // a selection asked for with copy
	done := make(chan struct{})

	const readWait = 70 * time.Second
	_ = conn.SetReadDeadline(time.Now().Add(readWait))
	conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(readWait)) })
	go func() {
		defer close(done)
		defer conn.Close()
		conn.SetReadLimit(64 << 10)
		for {
			_, b, err := conn.ReadMessage()
			if err != nil {
				return
			}
			_ = conn.SetReadDeadline(time.Now().Add(readWait))
			var m struct {
				T     string       `json:"t"`
				Seq   int64        `json:"seq"`
				Input screen.Input `json:"input"`
				W     int          `json:"w"`
				Q     int          `json:"q"`
				VW    int          `json:"vw"`
				VH    int          `json:"vh"`
			}
			if json.Unmarshal(b, &m) != nil {
				continue
			}
			switch m.T {
			case "input":
				// Here, in order: a typed string must not overtake the click that focused its field
				if src.Off() != "" {
					return
				}
				if err := src.Do(m.Input); err != nil {
					select {
					case errs <- err.Error():
					default:
					}
				}
				select {
				case kicks <- m.Seq:
				default:
				}
			case "cfg":
				if m.VW > 0 && m.VH > 0 {
					src.Size(m.VW, m.VH)
				}
				select {
				case cfgs <- parse(m.W, m.Q):
				default:
				}
			case "full":
				select {
				case fulls <- struct{}{}:
				default:
				}
			case "copy":
				select {
				case texts <- src.Selection():
				default:
				}
			}
		}
	}()

	qw, _ := strconv.Atoi(r.URL.Query().Get("w"))
	qq, _ := strconv.Atoi(r.URL.Query().Get("q"))
	c := parse(qw, qq)
	var d screen.Differ
	var seq, ack, sentAck int64
	var lastCur *[2]float64
	lastSend := time.Now()
	burstAt, burstIdx := time.Time{}, len(screenBurst)
	timer := time.NewTimer(0) // the first capture: right away
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()

	send := func(b []byte) bool {
		_ = conn.SetWriteDeadline(time.Now().Add(15 * time.Second))
		if conn.WriteMessage(websocket.BinaryMessage, b) != nil {
			return false
		}
		lastSend = time.Now()
		return true
	}
	var lastPage screen.Page
	rs, _ := src.(rawSource)
	var rawSeq int64 = -1
	var rawW, rawH int
	captureRaw := func() bool {
		jpg, w, hh, fseq, err := rs.Raw(c.w)
		h := screen.Header{Ack: ack}
		if p := src.Page(); p != nil && *p != lastPage {
			lastPage, h.Page = *p, p
		}
		if err != nil {
			h.Err = err.Error()
			return send(screen.Message(h, nil))
		}
		fresh := fseq != rawSeq
		if !fresh && ack == sentAck && h.Page == nil && time.Since(lastSend) < 10*time.Second {
			return true
		}
		seq++
		h.Seq, h.W, h.H = seq, w, hh
		// full only when the size changes: the page then resizes its canvas, which clears it
		h.Full = w != rawW || hh != rawH
		var payload []byte
		if fresh || h.Full {
			h.Rects, payload = []screen.Rect{{0, 0, w, hh, len(jpg)}}, jpg
		}
		rawSeq, rawW, rawH, sentAck = fseq, w, hh, ack
		return send(screen.Message(h, payload))
	}
	capture := func() bool {
		if off := src.Off(); off != "" {
			send(screen.Message(screen.Header{Err: off}, nil))
			return false
		}
		if raw && rs != nil {
			return captureRaw()
		}
		m, err := src.Grab(c.w)
		h := screen.Header{Ack: ack}
		if p := src.Page(); p != nil && *p != lastPage {
			lastPage, h.Page = *p, p
		}
		if err != nil {
			h.Err = err.Error()
			return send(screen.Message(h, nil))
		}
		if fx, fy, ok := src.Cursor(); ok {
			h.Cur = &[2]float64{fx, fy}
		}
		rects, payload, full, err := d.Diff(m, c.q)
		if err != nil {
			h.Err = err.Error()
			return send(screen.Message(h, nil))
		}
		moved := (h.Cur == nil) != (lastCur == nil) || (h.Cur != nil && *h.Cur != *lastCur)
		// Nothing new to draw or acknowledge: stay quiet, except a keepalive now and then
		if len(rects) == 0 && ack == sentAck && !moved && h.Page == nil && time.Since(lastSend) < 10*time.Second {
			return true
		}
		seq++
		h.Seq, h.W, h.H, h.Full, h.Rects = seq, m.Bounds().Dx(), m.Bounds().Dy(), full, rects
		lastCur, sentAck = h.Cur, ack
		return send(screen.Message(h, payload))
	}
	next := func() time.Duration {
		if burstIdx < len(screenBurst) {
			d := time.Until(burstAt.Add(screenBurst[burstIdx] * time.Millisecond))
			burstIdx++
			return max(0, d)
		}
		return screenIdleEvery
	}

	for {
		select {
		case <-done:
			return
		case a := <-kicks:
			ack = a
			burstAt, burstIdx = time.Now(), 0
			timer.Reset(next())
		case nc := <-cfgs:
			c = nc
			d.Reset()
			rawW, rawH = 0, 0
			timer.Reset(0)
		case <-fulls:
			d.Reset()
			rawW, rawH = 0, 0
			timer.Reset(0)
		case e := <-errs:
			if !send(screen.Message(screen.Header{Ack: ack, InErr: e}, nil)) {
				return
			}
		case <-timer.C:
			if !capture() {
				return
			}
			timer.Reset(next())
		case <-src.Changed():
			if !capture() {
				return
			}
		case txt := <-texts:
			if !send(screen.Message(screen.Header{Ack: ack, Text: txt}, nil)) {
				return
			}
		case <-ping.C:
			_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if conn.WriteMessage(websocket.PingMessage, nil) != nil {
				return
			}
		}
	}
}
