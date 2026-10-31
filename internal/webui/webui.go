// Package webui 内嵌一份界面源码（make 时从仓库的 ui/ 拷进 ui/），启动时装到 / 合进 <GERDR_DIR>/ui。
//
// 界面没有打包环节（internal/uisite）：装出来的就是源码，用户或 agent 直接改它，保存即生效。
// 所以升级 gerdr 时不能闷头覆盖 —— 用 git 三方合并（gitmerge.go，照 shuttle 的模板升级）：人改过的保留，
// gerdr 改的合进来。机器上没有 git 时退回老办法：记下每个文件的哈希（.gerdr-ui.json），一个都没改过才换新版，
// 改过就留着、新版放在旁边（<目录>.new）。
package webui

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

//go:embed all:ui
var snapshot embed.FS

const manifestName = ".gerdr-ui.json"

type manifest struct {
	Version string            `json:"version"`
	Files   map[string]string `json:"files"` // 路径 → sha256
}

// skipFile：不算界面源码的（和 Makefile 里 `make ui` 的 rsync 排除项一致，两边算出的版本号才对得上）。
func skipFile(rel string) bool {
	parts := strings.Split(rel, "/")
	for _, part := range parts {
		if part == "node_modules" || part == ".git" {
			return true
		}
	}
	// A workspace package's build output (npm only; gerdr serves its src/)
	if parts[0] == "packages" && slices.Contains(parts[1:len(parts)-1], "dist") {
		return true
	}
	// .gitignore 不算：界面目录的那份是 gerdr 自己写的（gitmerge.go）
	return rel == ".gitkeep" || rel == ".gitignore" || rel == manifestName || strings.HasSuffix(rel, ".tsbuildinfo") || strings.HasSuffix(rel, ".DS_Store")
}

func snapFiles() (map[string][]byte, error) {
	out := map[string][]byte{}
	err := fs.WalkDir(snapshot, "ui", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel := p[len("ui/"):]
		if skipFile(rel) {
			return nil
		}
		b, err := snapshot.ReadFile(p)
		out[rel] = b
		return err
	})
	return out, err
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// Has：这个 gerdr 有没有内嵌界面（直接 go build 没跑 make 时没有，那就只能用 GERDR_UI）。
func Has() bool {
	f, _ := snapFiles()
	return len(f) > 0
}

// Install 把内嵌的界面装到 / 合进 dst（见包注释）。返回一句给人看的说明（空 = 什么都没做）。
func Install(dst string) (string, error) {
	files, err := snapFiles()
	if err != nil || len(files) == 0 {
		return "", err
	}
	if hasGit() {
		res, err := upgradeGit(dst, files, "随 Gerdr 发布", false)
		if err != nil {
			return "", err
		}
		switch res.Status {
		case "installed":
			return "界面已安装到 " + dst, nil
		case "merged":
			return fmt.Sprintf("新版界面已合入 %s（改到 %d 个文件）", dst, len(res.Files)), nil
		case "conflict":
			return res.Note + "（冲突文件：" + strings.Join(res.Conflicts, ", ") + "）", nil
		}
		return "", nil
	}
	return legacyInstall(dst, files)
}

func legacyInstall(dst string, files map[string][]byte) (string, error) {
	next := manifestOf(files)

	var cur manifest
	raw, err := os.ReadFile(filepath.Join(dst, manifestName))
	switch {
	case err == nil:
		_ = json.Unmarshal(raw, &cur)
		if cur.Version == next.Version {
			return "", nil
		}
		for rel, want := range cur.Files {
			b, err := os.ReadFile(filepath.Join(dst, rel))
			if err != nil || sum(b) != want {
				// 改过了：留着，新版放旁边。
				alt := dst + ".new"
				_ = os.RemoveAll(alt)
				if err := write(alt, files, next); err != nil {
					return "", err
				}
				return fmt.Sprintf("界面目录 %s 有本地修改（比如 %s），没有覆盖；新版界面放在 %s", dst, rel, alt), nil
			}
		}
		for rel := range cur.Files {
			_ = os.Remove(filepath.Join(dst, rel))
		}
	case os.IsNotExist(err):
		if entries, _ := os.ReadDir(dst); len(entries) > 0 {
			return fmt.Sprintf("界面目录 %s 已经有东西（不是 Gerdr 装的），直接用它", dst), nil
		}
	default:
		return "", err
	}
	if err := write(dst, files, next); err != nil {
		return "", err
	}
	return "界面已安装到 " + dst, nil
}

func manifestOf(files map[string][]byte) manifest {
	keys := make([]string, 0, len(files))
	for k := range files {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	m := manifest{Files: map[string]string{}}
	h := sha256.New()
	for _, k := range keys {
		s := sum(files[k])
		m.Files[k] = s
		h.Write([]byte(k + "\x00" + s + "\n"))
	}
	m.Version = hex.EncodeToString(h.Sum(nil))[:16]
	return m
}

// Modified：dst 里哪些文件和上次装进去时不一样了（人或 agent 改过的）。
func Modified(dst string) ([]string, error) {
	raw, err := os.ReadFile(filepath.Join(dst, manifestName))
	if err != nil {
		return nil, err
	}
	var cur manifest
	if err := json.Unmarshal(raw, &cur); err != nil {
		return nil, err
	}
	var out []string
	for rel, want := range cur.Files {
		b, err := os.ReadFile(filepath.Join(dst, rel))
		if err != nil || sum(b) != want {
			out = append(out, rel)
		}
	}
	sort.Strings(out)
	return out, nil
}

// ReadDir 读一个界面源码目录（开发时就是仓库的 ui/），排除项和内嵌那份一样。
func ReadDir(src string) (map[string][]byte, error) {
	files := map[string][]byte{}
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel != "." && (d.Name() == "node_modules" || d.Name() == ".git") {
				return filepath.SkipDir
			}
			return nil
		}
		if skipFile(rel) {
			return nil
		}
		b, err := os.ReadFile(p)
		files[rel] = b
		return err
	})
	if err == nil {
		if _, ok := files["package.json"]; !ok {
			return nil, fmt.Errorf("%s 不像界面目录（没有 package.json）", src)
		}
	}
	return files, err
}

// SyncFrom 把一份界面源码（开发时的 ui/）当成新版合进 dst：和 App 升级走同一条 git 合并，
// 开着的窗口热更新，人在 dst 里的改动保留。冲突时不合（照旧用现在的），结果里列出冲突文件。
func SyncFrom(src, dst string) (*Result, error) {
	if !hasGit() {
		return nil, fmt.Errorf("这台机器上没有 git")
	}
	files, err := ReadDir(src)
	if err != nil {
		return nil, err
	}
	return upgradeGit(dst, files, "从 "+src+" 同步", false)
}

func write(dst string, files map[string][]byte, m manifest) error {
	for rel, b := range files {
		p := filepath.Join(dst, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, b, 0o644); err != nil {
			return err
		}
	}
	mb, _ := json.MarshalIndent(m, "", "  ")
	return os.WriteFile(filepath.Join(dst, manifestName), mb, 0o644)
}
