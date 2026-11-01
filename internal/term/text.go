package term

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// Text is the terminal's recent output as plain text, the last n lines (n <= 0: all that's kept), for `gerdr pane read`:
// an agent asks the user to run something in a terminal (an interactive login, a publish that needs a browser
// confirmation) and then has to see what came out.
//
// The ring holds raw PTY bytes, not a screen (see the package comment), so this replays them into lines with just
// enough of a terminal to undo what line-oriented programs do: \r and erase-line (progress bars, spinners, zsh
// redrawing its prompt), backspace, cursor left/right/up/down, and wrapping at the terminal's width (zsh redraws a
// long command line counting on it). Wrapped rows are joined back into one line. Absolute cursor moves aren't
// followed, so a full-screen program's output would come out garbled — but those run in the alternate screen,
// which is dropped here, the way a terminal shows the shell again once they quit. The width is today's: output
// from before a resize may wrap a little differently than it did on screen.
func (t *Term) Text(n int) string {
	t.mu.Lock()
	raw := append([]byte(nil), t.ring...)
	t.mu.Unlock()
	return render(raw, t.width(), n)
}

type screen struct {
	lines    [][]rune
	wrapped  map[int]bool // row continues the one above (auto-wrap), joined back on output
	row, col int
	cols     int  // 0 = don't wrap
	pending  bool // at the right edge: the next printable character wraps (xterm's deferred wrap)
}

func (s *screen) line() []rune {
	for s.row >= len(s.lines) {
		s.lines = append(s.lines, nil)
	}
	return s.lines[s.row]
}

func (s *screen) put(r rune) {
	if s.pending {
		s.pending = false
		s.row++
		s.col = 0
		if s.wrapped == nil {
			s.wrapped = map[int]bool{}
		}
		s.wrapped[s.row] = true
		if s.row < len(s.lines) {
			s.lines[s.row] = nil // a stale row from before a redraw
		}
	}
	l := s.line()
	for len(l) < s.col {
		l = append(l, ' ')
	}
	if s.col < len(l) {
		l[s.col] = r
	} else {
		l = append(l, r)
	}
	s.lines[s.row] = l
	s.col++
	if s.cols > 0 && s.col >= s.cols {
		s.col = s.cols - 1
		s.pending = true
	}
}

// eraseLine: mode 0 = cursor to end, 1 = start to cursor, 2 = whole line.
func (s *screen) eraseLine(mode int) {
	l := s.line()
	switch mode {
	case 0:
		if s.col < len(l) {
			s.lines[s.row] = l[:s.col]
		}
	case 1:
		for i := 0; i <= s.col && i < len(l); i++ {
			l[i] = ' '
		}
	case 2:
		s.lines[s.row] = nil
	}
}

func render(b []byte, cols, n int) string {
	s := &screen{cols: cols}
	var saved *screen // the main screen while a full-screen program has the alternate one
	for i := 0; i < len(b); {
		c := b[i]
		if c < 0x20 || c == 0x7f {
			s.pending = false // any control or cursor move cancels a deferred wrap
		}
		switch {
		case c == 0x1b:
			i = s.escape(b, i, &saved)
			continue
		case c == '\n':
			s.row++
			s.col = 0 // the PTY sends \r\n anyway; a bare \n from a raw-mode program means the same here
		case c == '\r':
			s.col = 0
		case c == '\b':
			if s.col > 0 {
				s.col--
			}
		case c == '\t':
			s.col = (s.col/8 + 1) * 8
		case c < 0x20 || c == 0x7f:
			// bell and other controls: nothing to show
		default:
			r, size := utf8.DecodeRune(b[i:])
			s.put(r)
			i += size
			continue
		}
		i++
	}
	if saved != nil {
		s = saved
	}
	out := make([]string, 0, len(s.lines))
	for i, l := range s.lines {
		if s.wrapped[i] && len(out) > 0 {
			out[len(out)-1] += strings.TrimRight(string(l), " ")
			continue
		}
		out = append(out, strings.TrimRight(string(l), " "))
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	if n > 0 && len(out) > n {
		out = out[len(out)-n:]
	}
	return strings.Join(out, "\n")
}

// escape handles the sequence starting at b[i] (ESC) and returns the index after it.
func (s *screen) escape(b []byte, i int, saved **screen) int {
	if i+1 >= len(b) {
		return len(b)
	}
	switch b[i+1] {
	case '[': // CSI: parameters, then one final byte in @..~
		j := i + 2
		for j < len(b) && (b[j] < 0x40 || b[j] > 0x7e) {
			j++
		}
		if j >= len(b) {
			return len(b)
		}
		s.csi(string(b[i+2:j]), b[j], saved)
		return j + 1
	case ']', 'P', '_', '^': // OSC / DCS / APC / PM: up to BEL or ESC \
		for j := i + 2; j < len(b); j++ {
			if b[j] == 0x07 {
				return j + 1
			}
			if b[j] == 0x1b && j+1 < len(b) && b[j+1] == '\\' {
				return j + 2
			}
		}
		return len(b)
	case '(', ')', '*', '+', '#', '%': // charset selection and the like: one more byte
		return min(i+3, len(b))
	}
	return i + 2
}

func (s *screen) csi(params string, final byte, saved **screen) {
	private := strings.HasPrefix(params, "?")
	params = strings.TrimLeft(params, "?<=>")
	num := func(def int) int {
		p, _, _ := strings.Cut(params, ";")
		if v, err := strconv.Atoi(p); err == nil && v > 0 {
			return v
		}
		return def
	}
	if private {
		// Alternate screen on / off (vim, less, claude): keep the main one aside and bring it back after.
		switch params {
		case "1049", "1047", "47":
			if final == 'h' && *saved == nil {
				main := *s
				*saved = &main
				*s = screen{cols: s.cols}
			} else if final == 'l' && *saved != nil {
				*s = **saved
				*saved = nil
			}
		}
		return
	}
	switch final {
	case 'K':
		s.eraseLine(num(0))
	case 'J':
		if num(0) == 0 { // erase below: zsh clears what's under a redrawn prompt this way
			s.eraseLine(0)
			if s.row+1 < len(s.lines) {
				s.lines = s.lines[:s.row+1]
			}
		}
	case 'C':
		s.col += num(1)
	case 'D':
		s.col = max(0, s.col-num(1))
	case 'G':
		s.col = num(1) - 1
	case 'A':
		s.row = max(0, s.row-num(1))
	case 'B':
		s.row += num(1)
	case 'E':
		s.row += num(1)
		s.col = 0
	case 'F':
		s.row = max(0, s.row-num(1))
		s.col = 0
	}
}
