// Package snapshot 给工作目录打快照、按快照恢复 —— 「回退」里文件那一半。
//
// 为什么自己做而不用 agent 自带的：claude 的 file checkpoint 不跟踪 Bash 改的文件
// （sed / rm / git checkout 改的都退不回来），codex 压根没有文件回退。两家统一走这里。
//
// 做法是一个**影子 git 仓库**：GIT_DIR 在 ~/.gerdr/snapshots/<cwd 的哈希>.git，
// work tree 指向用户的目录。不碰用户自己的 .git、不动他的 index，用户目录里的
// .gitignore 照样生效（node_modules 这类不会被打进去）。每次发一条新消息之前 `add -A` +
// `commit`，记下 commit 哈希；回退时先给「现在」也打一个（回退本身可以反悔），再
// `read-tree` + `checkout-index` 把文件换回去、删掉快照之后才出现的文件。
package snapshot

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Store struct {
	dir string
	mu  sync.Map // cwd → *sync.Mutex
}

func New(dir string) *Store { return &Store{dir: dir} }

// ErrSkipped：这个目录不打快照（家目录、根目录这种太大的）。
var ErrSkipped = errors.New("snapshot skipped")

func (s *Store) lock(cwd string) func() {
	v, _ := s.mu.LoadOrStore(cwd, &sync.Mutex{})
	m := v.(*sync.Mutex)
	m.Lock()
	return m.Unlock
}

func (s *Store) gitDir(cwd string) string {
	h := sha256.Sum256([]byte(cwd))
	return filepath.Join(s.dir, hex.EncodeToString(h[:8])+".git")
}

func skip(cwd string) bool {
	home, _ := os.UserHomeDir()
	c := filepath.Clean(cwd)
	return c == "/" || c == filepath.Clean(home) || c == filepath.Dir(filepath.Clean(home))
}

func (s *Store) git(ctx context.Context, cwd string, args ...string) (string, error) {
	gd := s.gitDir(cwd)
	full := append([]string{
		"--git-dir=" + gd, "--work-tree=" + cwd,
		"-c", "core.autocrlf=false", "-c", "core.quotepath=false", "-c", "color.ui=false",
		"-c", "user.name=gerdr", "-c", "user.email=gerdr@localhost", "-c", "commit.gpgsign=false",
	}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Dir = cwd
	// 不继承 GIT_DIR / GIT_INDEX_FILE 之类（gerdr 自己可能就是在一个 git hook 里被起的）。
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_") {
			env = append(env, kv)
		}
	}
	cmd.Env = env
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %v: %s", args[0], err, strings.TrimSpace(errb.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

func (s *Store) ensure(ctx context.Context, cwd string) error {
	gd := s.gitDir(cwd)
	if _, err := os.Stat(filepath.Join(gd, "HEAD")); err == nil {
		return nil
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	if _, err := s.git(ctx, cwd, "init", "-q"); err != nil {
		return err
	}
	// 用户的 .gitignore 在 work tree 里，自动生效；这里再兜几个常见的大目录（没 .gitignore 的项目）。
	ex := "node_modules/\n.venv/\nvenv/\n__pycache__/\ntarget/\ndist/\nbuild/\n.next/\n.DS_Store\n*.log\n"
	_ = os.MkdirAll(filepath.Join(gd, "info"), 0o700)
	_ = os.WriteFile(filepath.Join(gd, "info", "exclude"), []byte(ex), 0o600)
	_ = os.WriteFile(filepath.Join(gd, "gerdr-cwd"), []byte(cwd), 0o600)
	return nil
}

// Take 打一个快照，返回 commit 哈希。大目录（超过 timeout）放弃，不拖住发消息。
func (s *Store) Take(cwd, label string) (string, error) {
	if skip(cwd) {
		return "", ErrSkipped
	}
	if st, err := os.Stat(cwd); err != nil || !st.IsDir() {
		return "", ErrSkipped
	}
	defer s.lock(cwd)()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := s.ensure(ctx, cwd); err != nil {
		return "", err
	}
	if _, err := s.git(ctx, cwd, "add", "-A", "--ignore-errors", "."); err != nil {
		// 个别读不了的文件（权限）不该让整个快照失败：--ignore-errors 已经跳过它们，
		// 这里只在 git 整个跑不起来时才报。
		if ctx.Err() != nil {
			return "", fmt.Errorf("snapshot timed out (directory too large?)")
		}
	}
	if _, err := s.git(ctx, cwd, "commit", "-q", "--allow-empty", "--no-verify", "-m", label); err != nil {
		return "", err
	}
	return s.git(ctx, cwd, "rev-parse", "HEAD")
}

// Restore 把 cwd 恢复成 commit 那一刻：快照里有的文件改回去，快照之后才出现的（且没被
// .gitignore 挡掉的）删掉。返回恢复前打的那个安全快照（拿它可以再恢复回来）。
func (s *Store) Restore(cwd, commit string) (string, error) {
	if commit == "" {
		return "", errors.New("no snapshot")
	}
	safety, err := s.Take(cwd, "before restore to "+commit)
	if err != nil {
		return "", err
	}
	defer s.lock(cwd)()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	// 现在有、目标里没有的文件：删。
	gone, err := s.git(ctx, cwd, "diff", "--name-only", "--no-renames", "--diff-filter=A", commit, safety)
	if err != nil {
		return "", err
	}
	if _, err := s.git(ctx, cwd, "read-tree", commit); err != nil {
		return "", err
	}
	if _, err := s.git(ctx, cwd, "checkout-index", "-a", "-f"); err != nil {
		return "", err
	}
	for _, f := range strings.Split(gone, "\n") {
		if f == "" {
			continue
		}
		p := filepath.Join(cwd, f)
		if !strings.HasPrefix(p, filepath.Clean(cwd)+string(filepath.Separator)) {
			continue
		}
		_ = os.Remove(p)
		// 顺手删掉因此变空的目录。
		for d := filepath.Dir(p); d != filepath.Clean(cwd) && strings.HasPrefix(d, cwd); d = filepath.Dir(d) {
			if os.Remove(d) != nil {
				break
			}
		}
	}
	if _, err := s.git(ctx, cwd, "commit", "-q", "--allow-empty", "--no-verify", "-m", "restored to "+commit); err != nil {
		return "", err
	}
	return safety, nil
}

// Changed 报告 commit 之后工作目录里改了哪些文件（回退前给人看「会动到哪些文件」）。
func (s *Store) Changed(cwd, commit string) ([]string, error) {
	if commit == "" {
		return nil, nil
	}
	defer s.lock(cwd)()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := s.git(ctx, cwd, "add", "-A", "--ignore-errors", "."); err != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	out, err := s.git(ctx, cwd, "diff", "--cached", "--name-status", "--no-renames", commit)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, l := range strings.Split(out, "\n") {
		if l != "" {
			files = append(files, l)
		}
	}
	return files, nil
}
