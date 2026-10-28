package webui

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// 急救面板（/rescue）的「回到之前的一版」。界面被 agent 改坏、而且坏到没法在主界面里叫 agent 修的时候，
// 先退回一个能用的版本让人用上，再慢慢修。
//
// 只往前提交、不改写历史：回退之前把现在的样子提交一次（坏的那版也留在历史里，想找回改动随时找得回），
// 回退本身也是一个新提交。所以这里没有不可撤销的操作。

// Commit：界面目录历史里的一版。
type Commit struct {
	Sha      string `json:"sha"`
	Time     int64  `json:"time"` // unix 秒
	Subject  string `json:"subject"`
	Upstream bool   `json:"upstream,omitempty"` // gerdr 发的原版（upstream 分支的最新一个）
	Current  bool   `json:"current,omitempty"`  // 现在用的就是这一版（HEAD，且没有没提交的改动）
}

var shaRE = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

// History：main 上最近 n 个提交，外加 upstream 的最新一个（原版，放最后）。
func History(dst string, n int) ([]Commit, error) {
	if _, err := os.Stat(filepath.Join(dst, ".git")); err != nil || !hasGit() {
		return nil, fmt.Errorf("界面目录不是 git 仓库（或者这台机器上没有 git），没有历史可回")
	}
	out, err := git(dst, nil, "log", "-n", strconv.Itoa(n), "--first-parent", "--format=%H%x1f%ct%x1f%s", "HEAD")
	if err != nil {
		return nil, err
	}
	list := parseLog(out)
	if len(list) > 0 && Status(dst).Uncommited == 0 {
		list[0].Current = true
	}
	if up, err := git(dst, nil, "log", "-1", "--format=%H%x1f%ct%x1f%s", upstream); err == nil {
		if u := parseLog(up); len(u) == 1 {
			// 原版就在上面的列表里（没改过 / 刚装上）：标一下，不另列一条
			found := false
			for i := range list {
				if list[i].Sha == u[0].Sha {
					list[i].Upstream, found = true, true
				}
			}
			if !found {
				u[0].Upstream = true
				list = append(list, u[0])
			}
		}
	}
	return list, nil
}

func parseLog(out string) []Commit {
	var list []Commit
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Split(l, "\x1f")
		if len(f) != 3 {
			continue
		}
		t, _ := strconv.ParseInt(f[1], 10, 64)
		list = append(list, Commit{Sha: f[0], Time: t, Subject: f[2]})
	}
	return list
}

// Restore：把界面目录的内容换成 sha 那一版（多出来的文件删掉、少了的补回来），提交成一个新版本。
// 返回回退之前存档的那个提交（坏的那版），没有改动可存时为空。
func Restore(dst, sha string) (saved string, err error) {
	if !shaRE.MatchString(sha) {
		return "", fmt.Errorf("版本号不对：%q", sha)
	}
	if _, err := os.Stat(filepath.Join(dst, ".git", "MERGE_HEAD")); err == nil {
		// 合并到一半（有冲突标记）：先收掉，不然 commit 会把冲突一起提交
		if _, err := git(dst, nil, "merge", "--abort"); err != nil {
			return "", err
		}
	}
	if _, err := git(dst, nil, "cat-file", "-e", sha+"^{commit}"); err != nil {
		return "", fmt.Errorf("没有这个版本：%s", sha)
	}
	before, _ := git(dst, nil, "rev-parse", "HEAD")
	if err := commitAll(dst, "急救：回退之前的样子（存档）"); err != nil {
		return "", err
	}
	after, _ := git(dst, nil, "rev-parse", "HEAD")
	if strings.TrimSpace(after) != strings.TrimSpace(before) {
		saved = strings.TrimSpace(after)
	}
	// 按目标那一版重建索引和工作目录：它没有的文件也删掉（只动跟踪的文件，.gitignore 里的 node_modules 不碰）
	if _, err := git(dst, nil, "read-tree", "-u", "--reset", sha); err != nil {
		return saved, err
	}
	short := sha
	if len(short) > 8 {
		short = short[:8]
	}
	return saved, commitAll(dst, "急救：回到 "+short+" 那一版")
}
