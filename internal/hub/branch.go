package hub

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// gitBranch：cwd 所在仓库当前的分支（pane 顶部显示）。不 fork git —— 会话摘要每次广播都要它，几十个会话一刷就是几十个进程；
// 直接读 .git/HEAD：「ref: refs/heads/<分支>」，detached 时是一个提交号（给前 7 位）。worktree / 子模块里 .git 是个文件
// （「gitdir: <路径>」），跟过去读那边的 HEAD。不是仓库返回空。结果按 cwd 缓存几秒，切了分支几秒内跟上。
var branchCache sync.Map // cwd → branchEntry

type branchEntry struct {
	b  string
	at time.Time
}

func gitBranch(cwd string) string {
	if cwd == "" {
		return ""
	}
	if e, ok := branchCache.Load(cwd); ok && time.Since(e.(branchEntry).at) < 3*time.Second {
		return e.(branchEntry).b
	}
	b := readBranch(cwd)
	branchCache.Store(cwd, branchEntry{b, time.Now()})
	return b
}

func readBranch(dir string) string {
	for d := dir; ; {
		g := filepath.Join(d, ".git")
		if st, err := os.Stat(g); err == nil {
			if !st.IsDir() {
				b, err := os.ReadFile(g)
				if err != nil {
					return ""
				}
				p, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir:")
				if !ok {
					return ""
				}
				g = strings.TrimSpace(p)
				if !filepath.IsAbs(g) {
					g = filepath.Join(d, g)
				}
			}
			head, err := os.ReadFile(filepath.Join(g, "HEAD"))
			if err != nil {
				return ""
			}
			h := strings.TrimSpace(string(head))
			if r, ok := strings.CutPrefix(h, "ref: refs/heads/"); ok {
				return r
			}
			if len(h) >= 7 {
				return h[:7]
			}
			return ""
		}
		parent := filepath.Dir(d)
		if parent == d {
			return ""
		}
		d = parent
	}
}
