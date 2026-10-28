package webui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// 界面目录用 git 管（照 shuttle 的 internal/wsgit 模板升级）：
//
//   - 界面目录（默认 ~/.gerdr/ui）是一个 git 仓库，main 是正在用的那份（人和 agent 都在上面改）；
//   - upstream 分支的每个提交是 gerdr 发的一版界面（快照），main 从它分出去、一路合并它；
//   - 升级 = 把新的那版快照提交到 upstream，再在 main 上 git merge：人改过的地方保留，gerdr 改的地方合进来，
//     两边改了同一处才是冲突。
//
// 新版从哪来有两处，走同一条路：App 自带的那份（启动时，Install）和开发时仓库里的 ui/（gerdr ui sync）。
//
// 冲突时不把冲突标记留在目录里 —— 界面会当场编译失败、整个 App 用不了。先 abort，照旧用现在这份，
// `gerdr ui status` 里报出来；agent 跑 `gerdr ui merge` 才把冲突摆出来让它解决（gerdr-ui skill 里写了）。

const upstream = "upstream"

var verTrailerRE = regexp.MustCompile(`(?m)^界面版本: (\S+)$`)

func hasGit() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

func git(dir string, env []string, args ...string) (string, error) {
	// 提交身份写死成 gerdr：用户机器上可能没配 user.name，commit 会直接失败。
	full := append([]string{"-c", "user.name=gerdr", "-c", "user.email=gerdr@localhost", "-c", "core.quotepath=off", "-c", "commit.gpgsign=false"}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

// Result 是一次升级 / 同步的结果。
type Result struct {
	Status    string   `json:"status"` // installed / up_to_date / merged / conflict
	Version   string   `json:"version"`
	Files     []string `json:"files,omitempty"`     // 合并改到的文件
	Conflicts []string `json:"conflicts,omitempty"` // 冲突的文件（Status=conflict）
	Note      string   `json:"note,omitempty"`
}

const gitignore = "node_modules/\n.gerdr-ui.json\n.DS_Store\n*.tsbuildinfo\n"

// upgradeGit：把 files（一版界面）合进 dst。leave=true 时冲突留在目录里给 agent 解决，否则 abort。
func upgradeGit(dst string, files map[string][]byte, label string, leave bool) (*Result, error) {
	ver := manifestOf(files).Version
	res := &Result{Version: ver}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(dst, ".git")); err != nil {
		return initRepo(dst, files, ver, label)
	}
	if _, err := os.Stat(filepath.Join(dst, ".git", "MERGE_HEAD")); err == nil {
		res.Status = "conflict"
		res.Conflicts = conflicted(dst)
		res.Note = "上一次合并新版界面还有冲突没解决完"
		return res, nil
	}
	if tip, err := git(dst, nil, "log", "-1", "--format=%B", upstream); err == nil {
		if m := verTrailerRE.FindStringSubmatch(tip); m != nil && m[1] == ver {
			res.Status = "up_to_date"
			return res, nil
		}
	}
	if err := commitAll(dst, "合并新版界面之前的改动"); err != nil {
		return nil, err
	}
	if _, err := commitSnapshot(dst, files, ver, label); err != nil {
		return nil, err
	}
	return merge(dst, res, leave)
}

// merge 把 upstream 合进当前分支。
func merge(dst string, res *Result, leave bool) (*Result, error) {
	before, _ := git(dst, nil, "rev-parse", "HEAD")
	if _, err := git(dst, nil, "merge", "--no-ff", "--allow-unrelated-histories", "-m", "合入新版界面 "+res.Version, upstream); err != nil {
		res.Conflicts = conflicted(dst)
		if len(res.Conflicts) == 0 {
			git(dst, nil, "merge", "--abort")
			return nil, err
		}
		res.Status = "conflict"
		if !leave {
			git(dst, nil, "merge", "--abort")
			res.Note = "新版界面和本地改动有冲突，先没合：在 gerdr 里让 agent 运行 gerdr ui merge 解决"
		}
		return res, nil
	}
	out, _ := git(dst, nil, "diff", "--name-only", strings.TrimSpace(before), "HEAD")
	res.Files = strings.Fields(out)
	res.Status = "merged"
	if len(res.Files) == 0 {
		res.Status = "up_to_date"
	}
	return res, nil
}

// initRepo：第一次（或者从按哈希管的老版本换过来）。
func initRepo(dst string, files map[string][]byte, ver, label string) (*Result, error) {
	res := &Result{Version: ver, Status: "installed"}
	entries, _ := os.ReadDir(dst)
	empty := len(entries) == 0
	// 老版本装的、一个文件都没改过：直接换成新的（和全新装一样）
	if !empty {
		if mod, err := Modified(dst); err == nil && len(mod) == 0 {
			if raw, err := os.ReadFile(filepath.Join(dst, manifestName)); err == nil {
				var m manifest
				if json.Unmarshal(raw, &m) == nil {
					for rel := range m.Files {
						_ = os.Remove(filepath.Join(dst, rel))
					}
				}
			}
			empty = true
		}
	}
	if _, err := git(dst, nil, "init", "-q", "-b", "main"); err != nil {
		return nil, err
	}
	_ = os.WriteFile(filepath.Join(dst, ".gitignore"), []byte(gitignore), 0o644)
	if empty {
		if err := writeFiles(dst, files); err != nil {
			return nil, err
		}
		sha, err := commitSnapshot(dst, files, ver, label)
		if err != nil {
			return nil, err
		}
		if _, err := git(dst, nil, "reset", "-q", "--hard", sha); err != nil {
			return nil, err
		}
		return res, nil
	}
	// 有别人放的东西 / 改过的老版本：现状先提交成 main，新版做成 upstream（没有共同祖先，按无关历史合并）。
	if err := commitAll(dst, "现有界面（换成 git 管理之前）"); err != nil {
		return nil, err
	}
	if _, err := commitSnapshot(dst, files, ver, label); err != nil {
		return nil, err
	}
	return merge(dst, res, false)
}

func commitAll(dst, msg string) error {
	if _, err := git(dst, nil, "add", "-A"); err != nil {
		return err
	}
	if out, _ := git(dst, nil, "status", "--porcelain"); strings.TrimSpace(out) == "" {
		return nil
	}
	_, err := git(dst, nil, "commit", "-q", "-m", msg)
	return err
}

// commitSnapshot：files 写成一个 tree（临时目录 + 临时 index，不碰工作目录），接在 upstream 后面提交。
func commitSnapshot(dst string, files map[string][]byte, ver, label string) (string, error) {
	tmp, err := os.MkdirTemp("", "gerdr-ui-snap")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	if err := writeFiles(tmp, files); err != nil {
		return "", err
	}
	_ = os.WriteFile(filepath.Join(tmp, ".gitignore"), []byte(gitignore), 0o644)
	idx := filepath.Join(dst, ".git", "gerdr-upstream.index")
	_ = os.Remove(idx)
	defer os.Remove(idx)
	env := []string{"GIT_INDEX_FILE=" + idx}
	if _, err := git(dst, env, "--work-tree="+tmp, "add", "-A", "--force", "--", "."); err != nil {
		return "", err
	}
	tree, err := git(dst, env, "write-tree")
	if err != nil {
		return "", err
	}
	msg := "gerdr 界面"
	if label != "" {
		msg += "（" + label + "）"
	}
	msg += "\n\n界面版本: " + ver
	args := []string{"commit-tree", strings.TrimSpace(tree), "-m", msg}
	if parent, err := git(dst, nil, "rev-parse", "--verify", "-q", upstream); err == nil && strings.TrimSpace(parent) != "" {
		args = append(args, "-p", strings.TrimSpace(parent))
	}
	sha, err := git(dst, nil, args...)
	if err != nil {
		return "", err
	}
	sha = strings.TrimSpace(sha)
	_, err = git(dst, nil, "update-ref", "refs/heads/"+upstream, sha)
	return sha, err
}

func conflicted(dst string) []string {
	out, _ := git(dst, nil, "diff", "--name-only", "--diff-filter=U")
	return strings.Fields(out)
}

func writeFiles(dst string, files map[string][]byte) error {
	for rel, b := range files {
		p := filepath.Join(dst, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if old, err := os.ReadFile(p); err == nil && bytes.Equal(old, b) {
			continue // 没变的不碰：修改时间不动，热更新只推真改了的模块
		}
		if err := os.WriteFile(p, b, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// State：界面目录的 git 状态（gerdr ui status 用）。
type State struct {
	Git        bool     `json:"git"`
	Merging    bool     `json:"merging,omitempty"`
	Conflicts  []string `json:"conflicts,omitempty"`
	Pending    bool     `json:"pending_upstream,omitempty"` // upstream 有新版还没合进 main（上次合并有冲突、被跳过了）
	Uncommited int      `json:"uncommitted_files,omitempty"`
}

func Status(dst string) State {
	var st State
	if _, err := os.Stat(filepath.Join(dst, ".git")); err != nil || !hasGit() {
		return st
	}
	st.Git = true
	if _, err := os.Stat(filepath.Join(dst, ".git", "MERGE_HEAD")); err == nil {
		st.Merging, st.Conflicts = true, conflicted(dst)
	}
	if out, err := git(dst, nil, "rev-list", "--count", "HEAD.."+upstream); err == nil && strings.TrimSpace(out) != "0" {
		st.Pending = true
	}
	if out, err := git(dst, nil, "status", "--porcelain"); err == nil {
		for _, l := range strings.Split(out, "\n") {
			if strings.TrimSpace(l) != "" {
				st.Uncommited++
			}
		}
	}
	return st
}

// Merge：把 upstream 上还没合进来的新版界面合进来，冲突留在文件里（给 agent 解决：改好之后 git add + git commit）。
func Merge(dst string) (*Result, error) {
	if !hasGit() {
		return nil, fmt.Errorf("这台机器上没有 git")
	}
	if _, err := os.Stat(filepath.Join(dst, ".git", "MERGE_HEAD")); err == nil {
		return &Result{Status: "conflict", Conflicts: conflicted(dst), Note: "合并进行中：解决冲突后 git add -A && git commit"}, nil
	}
	if err := commitAll(dst, "合并新版界面之前的改动"); err != nil {
		return nil, err
	}
	ver := ""
	if tip, err := git(dst, nil, "log", "-1", "--format=%B", upstream); err == nil {
		if m := verTrailerRE.FindStringSubmatch(tip); m != nil {
			ver = m[1]
		}
	}
	return merge(dst, &Result{Version: ver}, true)
}
