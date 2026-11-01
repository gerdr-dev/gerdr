package term

import "testing"

func TestRender(t *testing.T) {
	cases := []struct {
		name, in, want string
		n              int
		cols           int
	}{
		{"plain", "a\r\nb\r\n", "a\nb", 0, 0},
		{"colors dropped", "\x1b[31merror\x1b[0m code E404\r\n", "error code E404", 0, 0},
		{"progress overwritten", "10%\r20%\r\x1b[Kdone\r\n", "done", 0, 0},
		{"erase rest of line", "abcdef\r\x1b[3C\x1b[K\r\n", "abc", 0, 0},
		{"backspace", "abc\b\bX\r\n", "aXc", 0, 0},
		{"osc title skipped", "\x1b]0;user@host\x07$ ls\r\n", "$ ls", 0, 0},
		{"zsh prompt_sp", "\x1b[7m%\x1b[27m   \r \r\x1b[K$ npm publish\r\n", "$ npm publish", 0, 0},
		{"alternate screen dropped", "$ vim\r\n\x1b[?1049hjunk\r\nmore\x1b[?1049l$ \r\n", "$ vim\n$", 0, 0},
		{"still in alternate screen", "$ less x\r\n\x1b[?1049hpage", "$ less x", 0, 0},
		{"utf8", "发布成功\r\n", "发布成功", 0, 0},
		{"last n lines", "1\r\n2\r\n3\r\n4\r\n", "3\n4", 2, 0},
		{"cursor up redraw", "a\r\nb\r\n\x1b[1A\x1b[2KB\r\n", "a\nB", 0, 0},
		{"wrapped line joined", "abcdefgh\r\nz\r\n", "abcdefgh\nz", 0, 4},
		{"exact width no blank row", "abcd\r\nz\r\n", "abcd\nz", 0, 4},
		// zsh redrawing a wrapped command line: back up a row, rewrite both rows
		{"redraw over wrap", "$ abcdef\r\x1b[1A$ ABCDEF\r\n", "$ ABCDEF", 0, 4},
	}
	for _, c := range cases {
		if got := render([]byte(c.in), c.cols, c.n); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}
