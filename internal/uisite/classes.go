package uisite

import (
	"regexp"
	"sort"
	"strings"
)

// Tailwind's browser build only generates a class once it shows up in the DOM, and its stylesheet rewrite lands a
// frame or more after the commit that mounted the element (docs/dev/TAILWIND.md). So every local source file's
// candidate classes are extracted here, cached with its compile result (per path + content, so an edit re-extracts
// only that file), and the union over the module graph is handed to the browser build as @source inline(...): it
// compiles them all on page load, and nothing mounted later waits for its CSS.
//
// The extraction is deliberately loose, like Tailwind's own file scanner: every whitespace-separated word inside a
// string literal that could be a class. Words that aren't classes cost nothing (Tailwind drops them), measured on
// gerdr's UI: ~2800 candidates, page-load compile 36→50ms on desktop, 145→190ms at 4x CPU throttling, generated
// CSS 51→154KB. What it can't see still compiles late: classes built at runtime ("w-" + n) and classes inside
// third-party packages (Streamdown) — those stay listed by hand in src/index.css.
//
// The result is cached on disk with the compile result: changing the extraction rules means bumping
// compileCacheVersion (modules.go), or files keep the classes the old rules found.

// stringLitRE matches one string literal. Leftmost match wins, so a "..." holding a ' (content-['x']) stays whole.
// Single-line for quotes: an apostrophe in a comment then swallows at most the rest of its line.
var stringLitRE = regexp.MustCompile("\"([^\"\\\\\\n]*)\"|'([^'\\\\\\n]*)'|`([^`\\\\]*)`")

// classTokenRE: the characters a class can have. No quotes, backslashes or braces: inside @source inline("...") a
// quote ends the string and braces are brace expansion. No non-ASCII.
var classTokenRE = regexp.MustCompile(`^[-!@*\[a-zA-Z][-_a-zA-Z0-9:./\[\]()%#!&>*=@',+~^<]*$`)

const maxClassLen = 200

// extractClasses returns the sorted, unique candidate classes in a source file.
func extractClasses(src []byte) []string {
	seen := map[string]bool{}
	var scan func(src []byte)
	scan = func(src []byte) {
		for _, m := range stringLitRE.FindAllSubmatch(src, -1) {
			s := m[1]
			if s == nil {
				s = m[2]
			}
			if s == nil {
				s = m[3]
				// A template literal's ${...} often holds strings of its own: `p-2 ${on ? "bg-accent" : ""}`
				scan(s)
			}
			for _, w := range strings.Fields(string(s)) {
				if len(w) <= maxClassLen && classTokenRE.MatchString(w) && balanced(w) {
					seen[w] = true
				}
			}
		}
	}
	scan(src)
	out := make([]string, 0, len(seen))
	for w := range seen {
		out = append(out, w)
	}
	sort.Strings(out)
	return out
}

// balanced: every ( and [ is closed in order and quotes come in pairs. Measured with 4.1.11: one word with an
// unclosed bracket ("min(42rem," from a JS expression in a template literal) or a lone quote (the apostrophe in
// "Can't" from UI text) silently drops every class after it in the same @source inline.
func balanced(w string) bool {
	if strings.Count(w, "'")%2 != 0 {
		return false
	}
	var stack []byte
	for i := 0; i < len(w); i++ {
		switch c := w[i]; c {
		case '(', '[':
			stack = append(stack, c)
		case ')', ']':
			open := byte('(')
			if c == ']' {
				open = '['
			}
			if len(stack) == 0 || stack[len(stack)-1] != open {
				return false
			}
			stack = stack[:len(stack)-1]
		}
	}
	return len(stack) == 0
}

// classesPerInline bounds each @source inline line, so a word that breaks one anyway (balanced misses some trap)
// costs only its own line, not every class in the UI.
const classesPerInline = 200

// sourceInlineCSS turns the classes of every module in the graph into @source inline lines ("" when there are none).
// Sorted, so the CSS — and with it the build id and the hot update — changes only when the set of classes does.
func sourceInlineCSS(g *graph) string {
	seen := map[string]bool{}
	for _, m := range g.mods {
		if m == nil || m.Error != "" {
			continue
		}
		for _, c := range m.Classes {
			seen[c] = true
		}
	}
	if len(seen) == 0 {
		return ""
	}
	all := make([]string, 0, len(seen))
	for c := range seen {
		all = append(all, c)
	}
	sort.Strings(all)
	var s strings.Builder
	s.WriteString("\n/* Classes found in the UI's source files, compiled up front (internal/uisite/classes.go) */\n")
	for i := 0; i < len(all); i += classesPerInline {
		s.WriteString(`@source inline("` + strings.Join(all[i:min(i+classesPerInline, len(all))], " ") + "\");\n")
	}
	return s.String()
}
