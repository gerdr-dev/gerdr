package native

import (
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/coder/acp-go-sdk"
	"golang.org/x/net/html"
)

// web_fetch：抓一个网页给模型看。HTML 转成 Markdown 风格的纯文本（标题、链接、列表、代码块留着，脚本 / 样式 / 导航扔掉），
// 原样的 HTML 太费 token。不做「用小模型总结」（Claude Code 的 WebFetch 那样）：多一次请求、多一个出错点，
// 模型自己读文本就行；太长用 start 分页。
//
// 联网 = 数据可能被带出去（URL 里塞东西），所以不算只读：按域名审批（permission.go 的 fetch:<host>）。

var webFetchTool = &tool{
	name: "web_fetch",
	desc: "Fetch a URL (http/https) and return its content as text. HTML pages are converted to Markdown-like text; JSON and plain text are returned as is. " +
		"Long pages are cut at max_length characters — fetch again with start to read further.",
	params: obj(map[string]any{
		"url":        prop("string", "The URL to fetch"),
		"start":      prop("integer", "Character offset to start from (for long pages, default 0)"),
		"max_length": prop("integer", "Maximum characters to return (default 20000)"),
	}, "url"),
	show: func(args map[string]any, cwd string) (string, acp.ToolKind, []acp.ToolCallLocation) {
		return "Fetch " + oneLine(str(args, "url"), 160), acp.ToolKindFetch, nil
	},
	run: func(c *call, args map[string]any) result {
		u, err := url.Parse(str(args, "url"))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return errResult("invalid URL: must be http(s)")
		}
		req, err := http.NewRequestWithContext(c.ctx, "GET", u.String(), nil)
		if err != nil {
			return errResult("%v", err)
		}
		// 有的站对没有浏览器 UA 的请求直接 403
		req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130 Safari/537.36 gerdr")
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/json,text/plain;q=0.9,*/*;q=0.5")
		cl := &http.Client{Timeout: 30 * time.Second}
		res, err := cl.Do(req)
		if err != nil {
			return errResult("%v", err)
		}
		defer res.Body.Close()
		body, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
		if err != nil {
			return errResult("%v", err)
		}
		ct, _, _ := mime.ParseMediaType(res.Header.Get("Content-Type"))
		var text string
		switch {
		case ct == "text/html" || ct == "application/xhtml+xml" || (ct == "" && strings.Contains(strings.ToLower(string(body[:min(len(body), 512)])), "<html")):
			text = htmlToText(string(body), res.Request.URL)
		case strings.HasPrefix(ct, "text/") || strings.Contains(ct, "json") || strings.Contains(ct, "xml") || strings.Contains(ct, "javascript"):
			text = string(body)
		default:
			return errResult("unsupported content type %q (%d bytes)", ct, len(body))
		}
		head := fmt.Sprintf("URL: %s\nStatus: %d\n", res.Request.URL, res.StatusCode)
		start := max(num(args, "start"), 0)
		maxLen := num(args, "max_length")
		if maxLen <= 0 {
			maxLen = 20000
		}
		r := []rune(text)
		total := len(r)
		if start >= total && total > 0 {
			return errResult("start %d is past the end (%d characters)", start, total)
		}
		end := min(start+maxLen, total)
		out := string(r[start:end])
		if end < total {
			out += fmt.Sprintf("\n\n[%d more characters; fetch again with start=%d]", total-end, end)
		}
		return result{text: head + "\n" + out, err: res.StatusCode >= 400,
			display: []acp.ToolCallContent{acp.ToolContent(acp.TextBlock(head + "\n" + string(r[start:min(start+4000, total)])))}}
	},
}

var blankLines = regexp.MustCompile(`\n{3,}`)

// htmlToText：HTML → Markdown 风格的纯文本。只认最常见的结构，够模型读懂就行。
func htmlToText(src string, base *url.URL) string {
	doc, err := html.Parse(strings.NewReader(src))
	if err != nil {
		return src
	}
	b := &strings.Builder{}
	skip := map[string]bool{"script": true, "style": true, "noscript": true, "svg": true, "nav": true, "footer": true, "iframe": true, "template": true, "head": true, "form": true}
	var walk func(n *html.Node, pre bool, list string, depth int)
	// space：前一段文字以空白结尾（或这段以空白开头）时，等下一个可见的东西出来再补一个空格 ——
	// 「Hello <b>world</b>」的空格在前一个文字节点的末尾，直接丢掉就成了 Helloworld
	space := false
	flushSpace := func() {
		if space && b.Len() > 0 {
			if last := b.String()[b.Len()-1]; last != '\n' && last != ' ' {
				b.WriteByte(' ')
			}
		}
		space = false
	}
	text := func(n *html.Node, pre bool) {
		if pre {
			b.WriteString(n.Data)
			return
		}
		s := strings.Join(strings.Fields(n.Data), " ")
		if s == "" {
			if n.Data != "" {
				space = true
			}
			return
		}
		if strings.IndexAny(n.Data[:1], " \t\n\r") == 0 {
			space = true
		}
		flushSpace()
		b.WriteString(s)
		last := n.Data[len(n.Data)-1]
		space = last == ' ' || last == '\t' || last == '\n' || last == '\r'
	}
	block := func() {
		space = false
		if b.Len() > 0 && !strings.HasSuffix(b.String(), "\n\n") {
			if strings.HasSuffix(b.String(), "\n") {
				b.WriteByte('\n')
			} else {
				b.WriteString("\n\n")
			}
		}
	}
	walk = func(n *html.Node, pre bool, list string, depth int) {
		if n.Type == html.TextNode {
			text(n, pre)
			return
		}
		if n.Type == html.ElementNode {
			tag := n.Data
			if skip[tag] {
				return
			}
			attr := func(k string) string {
				for _, a := range n.Attr {
					if a.Key == k {
						return a.Val
					}
				}
				return ""
			}
			switch tag {
			case "h1", "h2", "h3", "h4", "h5", "h6":
				block()
				b.WriteString(strings.Repeat("#", int(tag[1]-'0')) + " ")
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					walk(c, false, list, depth)
				}
				block()
				return
			case "p", "div", "section", "article", "main", "header", "table", "blockquote", "figure":
				block()
			case "br":
				b.WriteByte('\n')
				return
			case "tr":
				b.WriteByte('\n')
			case "td", "th":
				b.WriteString(" | ")
			case "pre":
				block()
				b.WriteString("```\n")
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					walk(c, true, list, depth)
				}
				b.WriteString("\n```")
				block()
				return
			case "code":
				if !pre {
					flushSpace()
					b.WriteByte('`')
					for c := n.FirstChild; c != nil; c = c.NextSibling {
						walk(c, false, list, depth)
					}
					b.WriteByte('`')
					return
				}
			case "ul", "ol":
				block()
				i := 0
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					if c.Type == html.ElementNode && c.Data == "li" {
						i++
						marker := "- "
						if tag == "ol" {
							marker = fmt.Sprintf("%d. ", i)
						}
						if !strings.HasSuffix(b.String(), "\n") && b.Len() > 0 {
							b.WriteByte('\n')
						}
						b.WriteString(strings.Repeat("  ", depth) + marker)
						for cc := c.FirstChild; cc != nil; cc = cc.NextSibling {
							walk(cc, false, tag, depth+1)
						}
					}
				}
				block()
				return
			case "a":
				href := attr("href")
				saved, savedSpace := b, space
				b, space = &strings.Builder{}, false
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					walk(c, pre, list, depth)
				}
				label := strings.TrimSpace(b.String())
				trail := space
				b, space = saved, savedSpace
				flushSpace()
				if href != "" && !strings.HasPrefix(href, "#") && !strings.HasPrefix(href, "javascript:") && label != "" {
					if u, err := base.Parse(href); err == nil {
						href = u.String()
					}
					b.WriteString("[" + label + "](" + href + ")")
				} else {
					b.WriteString(label)
				}
				space = trail
				return
			case "img":
				if alt := attr("alt"); alt != "" {
					flushSpace()
					b.WriteString("[image: " + alt + "]")
				}
				return
			case "title":
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, pre, list, depth)
		}
	}
	walk(doc, false, "", 0)
	out := blankLines.ReplaceAllString(b.String(), "\n\n")
	// 页面标题放最前（head 整个跳过了）
	if t := findTitle(doc); t != "" {
		out = "Title: " + t + "\n\n" + out
	}
	return strings.TrimSpace(out)
}

func findTitle(n *html.Node) string {
	if n.Type == html.ElementNode && n.Data == "title" && n.FirstChild != nil {
		return strings.TrimSpace(n.FirstChild.Data)
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if t := findTitle(c); t != "" {
			return t
		}
	}
	return ""
}
