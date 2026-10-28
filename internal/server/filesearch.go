package server

import (
	"io/fs"
	"net/http"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/zbysir/gerdr/internal/hub"
)

// 输入框里的 @ 文件引用（照 Claude Code）：会话目录下的文件模糊搜索。
// git 仓库用 git ls-files（快，.gitignore 里的自动不算，未跟踪的新文件也带上）；不是仓库就自己走目录，跳过常见的大目录，最多 2 万个。

var skipDirs = map[string]bool{"node_modules": true, ".git": true, "dist": true, "build": true, ".next": true, "target": true, "vendor": true, ".venv": true, "__pycache__": true}

func listFiles(dir string) []string {
	cmd := exec.Command("git", "-C", dir, "ls-files", "-co", "--exclude-standard")
	if out, err := cmd.Output(); err == nil {
		return strings.Split(strings.TrimSpace(string(out)), "\n")
	}
	var files []string
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || len(files) > 20000 {
			return filepath.SkipDir
		}
		if d.IsDir() {
			if p != dir && (skipDirs[d.Name()] || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	return files
}

// fuzzyScore：q 的字符按顺序都出现在 path 里才算；文件名里命中、连续命中、路径短的排前面。-1 = 不匹配。
func fuzzyScore(path, q string) int {
	if q == "" {
		return 1000 - len(path)
	}
	lp, lq := strings.ToLower(path), strings.ToLower(q)
	base := lp[strings.LastIndex(lp, "/")+1:]
	score := 0
	if strings.Contains(base, lq) {
		score += 500
		if strings.HasPrefix(base, lq) {
			score += 200
		}
	} else if strings.Contains(lp, lq) {
		score += 300
	}
	j, run := 0, 0
	for i := 0; i < len(lp) && j < len(lq); i++ {
		if lp[i] == lq[j] {
			j++
			run++
			score += run * 2
		} else {
			run = 0
		}
	}
	if j < len(lq) {
		return -1
	}
	return score - len(path)/4
}

func (s *Server) searchFiles(r *http.Request, sess *hub.Session) (any, error) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	files := listFiles(sess.Summary().Cwd)
	type hit struct {
		p string
		s int
	}
	var hits []hit
	for _, f := range files {
		if f == "" {
			continue
		}
		if sc := fuzzyScore(f, q); sc >= 0 {
			hits = append(hits, hit{f, sc})
		}
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].s > hits[j].s })
	out := []string{}
	for i := 0; i < len(hits) && i < 30; i++ {
		out = append(out, hits[i].p)
	}
	return map[string]any{"files": out}, nil
}
