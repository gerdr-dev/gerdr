package native

import (
	"net/url"
	"strings"
	"testing"
)

func TestHTMLToText(t *testing.T) {
	base, _ := url.Parse("https://example.com/docs/")
	got := htmlToText(`<html><head><title>T</title><script>evil()</script></head><body>
<nav>menu</nav><h2>Intro</h2><p>Hello <b>world</b>, see <a href="../x">the docs</a>.</p>
<ul><li>one</li><li>two <code>x()</code></li></ul><pre>a
  b</pre></body></html>`, base)
	for _, want := range []string{"Title: T", "## Intro", "Hello world, see [the docs](https://example.com/x).", "- one", "- two `x()`", "```\na\n  b\n```"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	for _, bad := range []string{"evil", "menu"} {
		if strings.Contains(got, bad) {
			t.Errorf("should drop %q:\n%s", bad, got)
		}
	}
}
