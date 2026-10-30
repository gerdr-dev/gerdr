package work

import (
	"fmt"
	"strings"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/PuerkitoBio/goquery"
	"github.com/andybalholm/cascadia"
	"github.com/dop251/goja"
	"golang.org/x/net/html"
)

// html: ctx.html(text) parses HTML. .find(css) → [{ tag, text, attrs }] (text with whitespace collapsed), .text() the
// visible text of body (no script / style / noscript / svg), .markdown() the page as Markdown.
func (r *runner) html(text string) *goja.Object {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(text))
	if err != nil {
		r.throw(err)
	}
	o := r.vm.NewObject()
	_ = o.Set("find", func(sel string) []any {
		// goquery silently matches nothing on a bad selector: say so instead
		if _, err := cascadia.ParseGroup(sel); err != nil {
			r.throw(fmt.Errorf("invalid selector %q: %v", sel, err))
		}
		out := []any{}
		doc.Find(sel).Each(func(_ int, s *goquery.Selection) {
			attrs := map[string]any{}
			tag := ""
			if n := s.Get(0); n != nil {
				tag = n.Data
				for _, a := range n.Attr {
					attrs[a.Key] = a.Val
				}
			}
			out = append(out, map[string]any{"tag": tag, "text": visibleText(s), "attrs": attrs})
		})
		return out
	})
	_ = o.Set("text", func() string { return visibleText(doc.Find("body")) })
	_ = o.Set("markdown", func() string {
		md, err := htmltomarkdown.ConvertString(text)
		if err != nil {
			r.throw(fmt.Errorf("converting to Markdown failed: %w", err))
		}
		return strings.TrimSpace(md)
	})
	return o
}

// visibleText is an element's text, skipping script / style / noscript / svg / template, pieces joined by a space
// (goquery's Text() glues adjacent paragraphs together).
func visibleText(s *goquery.Selection) string {
	var parts []string
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		switch {
		case n.Type == html.TextNode:
			if t := strings.Join(strings.Fields(n.Data), " "); t != "" {
				parts = append(parts, t)
			}
			return
		case n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style" || n.Data == "noscript" || n.Data == "svg" || n.Data == "template"):
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	for _, n := range s.Nodes {
		walk(n)
	}
	return strings.Join(parts, " ")
}
