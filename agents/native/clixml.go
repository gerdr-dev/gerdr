package native

import (
	"bytes"
	"html"
	"io"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// Windows PowerShell 5.1 started with -EncodedCommand and its stderr on a pipe writes the error, warning, verbose
// and progress streams there as serialized objects ("#< CLIXML" and an <Objs> document) instead of text: the model
// read a page of XML where one error line should have been. psPreamble turns progress off (the most common source:
// "Preparing modules for first use.", Invoke-WebRequest's bar); clixmlWriter turns what is left back into the lines a
// console would show. PowerShell 7 writes plain text, which passes through untouched.

const clixmlMark = "#< CLIXML"

// clixmlWriter sits in front of a command's stderr: plain text passes through, each CLIXML block is written as text.
type clixmlWriter struct {
	mu    sync.Mutex
	w     io.Writer
	pend  []byte
	inXML bool
	// skipNL: a block was just written; the line break after its </Objs> (perhaps in the next write) is dropped
	skipNL bool
}

// clixmlMax: a block that never ends (a truncated document) is written as it came once it gets this big.
const clixmlMax = 1 << 20

func (c *clixmlWriter) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pend = append(c.pend, p...)
	for {
		if c.skipNL && len(c.pend) > 0 {
			if c.pend[0] == '\r' {
				c.pend = c.pend[1:]
			}
			if len(c.pend) > 0 {
				if c.pend[0] == '\n' {
					c.pend = c.pend[1:]
				}
				c.skipNL = false
			}
		}
		if !c.inXML {
			i := bytes.Index(c.pend, []byte(clixmlMark))
			if i < 0 {
				// keep a tail that could be the start of a marker split across writes
				k := markPrefix(c.pend)
				if err := c.out(c.pend[:len(c.pend)-k]); err != nil {
					return len(p), err
				}
				c.pend = append(c.pend[:0], c.pend[len(c.pend)-k:]...)
				return len(p), nil
			}
			if err := c.out(c.pend[:i]); err != nil {
				return len(p), err
			}
			c.pend = c.pend[i+len(clixmlMark):]
			c.inXML = true
		}
		end := bytes.Index(c.pend, []byte("</Objs>"))
		if end < 0 {
			if len(c.pend) > clixmlMax {
				err := c.out(append([]byte(clixmlMark), c.pend...))
				c.pend, c.inXML = c.pend[:0], false
				return len(p), err
			}
			return len(p), nil
		}
		end += len("</Objs>")
		if err := c.out([]byte(clixmlText(string(c.pend[:end])))); err != nil {
			return len(p), err
		}
		c.pend = c.pend[end:]
		c.inXML, c.skipNL = false, true
	}
}

// flush writes what is still held back (an unfinished block as it came). Call it once the command has exited.
func (c *clixmlWriter) flush() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.inXML {
		_ = c.out([]byte(clixmlMark))
	}
	_ = c.out(c.pend)
	c.pend, c.inXML = nil, false
}

func (c *clixmlWriter) out(b []byte) error {
	if len(b) == 0 {
		return nil
	}
	_, err := c.w.Write(b)
	return err
}

// markPrefix is the length of the longest tail of b that begins clixmlMark.
func markPrefix(b []byte) int {
	for k := min(len(b), len(clixmlMark)-1); k > 0; k-- {
		if bytes.HasSuffix(b, []byte(clixmlMark[:k])) {
			return k
		}
	}
	return 0
}

var (
	clixmlString = regexp.MustCompile(`(?s)<S S="([^"]*)">(.*?)</S>`)
	clixmlEscape = regexp.MustCompile(`_x([0-9A-Fa-f]{4})_`)
)

// clixmlText is the text of a CLIXML document's stream strings, as a console shows them. Progress records are
// <Obj S="progress"> (not <S>) and drop out.
func clixmlText(doc string) string {
	var sb strings.Builder
	for _, m := range clixmlString.FindAllStringSubmatch(doc, -1) {
		s := clixmlEscape.ReplaceAllStringFunc(html.UnescapeString(m[2]), func(e string) string {
			n, _ := strconv.ParseUint(e[2:6], 16, 16)
			return string(rune(n))
		})
		kind := strings.ToLower(m[1])
		switch kind {
		case "warning":
			s = "WARNING: " + s
		case "verbose":
			s = "VERBOSE: " + s
		case "debug":
			s = "DEBUG: " + s
		}
		sb.WriteString(s)
		if !strings.HasSuffix(s, "\n") && kind != "error" {
			// an error record's lines already end in _x000D__x000A_; the other streams' don't
			sb.WriteString("\r\n")
		}
	}
	return sb.String()
}
