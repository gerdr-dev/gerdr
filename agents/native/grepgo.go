package native

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// goGrep is the grep tool when neither rg nor grep is on the machine (Windows without Git): Go regexp (RE2, close
// to ripgrep's syntax), skips .git / node_modules, binary files and files over 4 MB. No .gitignore, no context lines.
func goGrep(pat, dir, glob, mode string, ignoreCase bool) (string, error) {
	if ignoreCase {
		pat = "(?i)" + pat
	}
	re, err := regexp.Compile(pat)
	if err != nil {
		return "", fmt.Errorf("bad pattern: %v", err)
	}
	var out strings.Builder
	search := func(p string) {
		if out.Len() > outMax*2 {
			return
		}
		st, err := os.Stat(p)
		if err != nil || st.Size() > 4<<20 {
			return
		}
		b, err := os.ReadFile(p)
		if err != nil || bytes.IndexByte(b[:min(len(b), 8000)], 0) >= 0 {
			return
		}
		n := 0
		sc := bufio.NewScanner(bytes.NewReader(b))
		sc.Buffer(make([]byte, 64<<10), 4<<20)
		for line := 1; sc.Scan(); line++ {
			if !re.Match(sc.Bytes()) {
				continue
			}
			n++
			if mode == "files" {
				break
			}
			if mode == "content" {
				l := sc.Text()
				if len(l) > 400 {
					l = l[:400] + "…"
				}
				fmt.Fprintf(&out, "%s:%d:%s\n", p, line, l)
			}
		}
		switch {
		case n == 0:
		case mode == "files":
			out.WriteString(p + "\n")
		case mode == "count":
			fmt.Fprintf(&out, "%s:%d\n", p, n)
		}
	}
	if st, err := os.Stat(dir); err == nil && !st.IsDir() {
		search(dir)
		return out.String(), nil
	}
	err = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != dir && (d.Name() == ".git" || d.Name() == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if r, _ := filepath.Rel(dir, p); glob == "" || globMatch(glob, filepath.ToSlash(r)) {
			search(p)
		}
		return nil
	})
	return out.String(), err
}
