package uisite

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Tailwind browser can't follow @import, so a stylesheet split into files (src/index.css importing
// "./animate.css") is stitched together here, before browserCSS drops the @imports it can't use. Only relative
// paths to .css files inside src/ are inlined; anything else (a package, a URL) is left for browserCSS to report.
var cssLocalImportRE = regexp.MustCompile(`(?m)^[ \t]*@import\s+["'](\.{1,2}/[^"']+\.css)["']\s*;[ \t]*\r?\n?`)

// inlineCSSImports replaces each local @import in file's css with that file's content, recursively. root bounds what
// may be read (src/); a missing file, one outside root, or an import cycle becomes a warning and is skipped.
func inlineCSSImports(root, file, css string) (out string, warns []string) {
	return inlineCSS(root, file, css, map[string]bool{file: true}, &warns), warns
}

func inlineCSS(root, file, css string, open map[string]bool, warns *[]string) string {
	rel := func(p string) string {
		if r, err := filepath.Rel(filepath.Dir(root), p); err == nil {
			return filepath.ToSlash(r)
		}
		return p
	}
	return cssLocalImportRE.ReplaceAllStringFunc(css, func(line string) string {
		spec := cssLocalImportRE.FindStringSubmatch(line)[1]
		p := filepath.Join(filepath.Dir(file), filepath.FromSlash(spec))
		if r, err := filepath.Rel(root, p); err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
			*warns = append(*warns, fmt.Sprintf("%s：@import %q 指到了 src/ 外面，已经跳过", rel(file), spec))
			return ""
		}
		if open[p] {
			*warns = append(*warns, fmt.Sprintf("%s：@import %q 绕回来了（循环引用），已经跳过", rel(file), spec))
			return ""
		}
		b, err := os.ReadFile(p)
		if err != nil {
			*warns = append(*warns, fmt.Sprintf("%s：@import %q 读不到（%v），已经跳过", rel(file), spec, err))
			return ""
		}
		open[p] = true
		body := inlineCSS(root, p, string(b), open, warns)
		delete(open, p)
		return "/* " + rel(p) + " */\n" + body + "\n"
	})
}
