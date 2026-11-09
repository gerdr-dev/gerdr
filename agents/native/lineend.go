package native

import "strings"

// Files keep their line endings and UTF-8 BOM through edit / write (ported from pi's edit tool). On Windows many
// files are CRLF (Git for Windows checks out with core.autocrlf=true): the model writes old_string with "\n", which
// never matched, and a rewritten file came back LF-only.

const utf8BOM = "\ufeff"

// splitBOM splits a leading UTF-8 BOM off.
func splitBOM(s string) (bom, rest string) {
	if strings.HasPrefix(s, utf8BOM) {
		return utf8BOM, s[len(utf8BOM):]
	}
	return "", s
}

// isCRLF: the file's first line break is CRLF (pi's detectLineEnding).
func isCRLF(s string) bool {
	i := strings.IndexByte(s, '\n')
	return i > 0 && s[i-1] == '\r'
}

func toLF(s string) string { return strings.ReplaceAll(s, "\r\n", "\n") }

// restore puts the BOM and line endings back on LF text.
func restore(bom, lf string, crlf bool) string {
	if crlf {
		lf = strings.ReplaceAll(lf, "\n", "\r\n")
	}
	return bom + lf
}

// likeOld gives new content for an existing file the old file's BOM and line endings, when the new content
// doesn't choose its own (no BOM / no CRLF in it).
func likeOld(old, content string) string {
	bom, rest := splitBOM(old)
	_, c := splitBOM(content)
	crlf := isCRLF(rest) && !strings.Contains(c, "\r\n")
	return restore(bom, c, crlf)
}
