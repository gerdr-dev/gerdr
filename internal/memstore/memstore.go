// Package memstore 是设置 →「记忆」那一页的后端：把三家 agent 的长期记忆列出来、读、改、删。
//
//   - Gerdr Agent（native）：<GERDR_DIR>/native/memory/{global,projects/<项目>}（internal/agents/native/memory.go）；
//   - Claude Code：~/.claude/projects/<cwd 变形>/memory/（它自己的自动记忆，格式和我们的一样：一条一个 .md + MEMORY.md 索引）；
//   - Codex：~/.codex/memories/（它从过去的会话自动总结出来的文件；原始数据在 memories_*.sqlite，那是它的内部格式，不碰）。
//
// 三家的记忆都是 agent 自己写的文件，这里只是给人一个看和改的地方，agent 下一轮读到的就是改过的。
// 安全：只认这几个目录里的 .md（Allowed），符号链接解开再判断 —— 这几个接口能写文件，不能被拿去读写别处。
package memstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const Index = "MEMORY.md"

type Entry struct {
	File        string `json:"file"` // 文件名（相对记忆目录）
	Path        string `json:"path"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Type        string `json:"type,omitempty"`
	Size        int64  `json:"size"`
	ModTime     int64  `json:"modTime"` // unix ms
}

type Store struct {
	ID    string `json:"id"`    // 记忆目录的路径（唯一）
	Agent string `json:"agent"` // native | claude | codex
	Scope string `json:"scope"` // global | project
	// Label：给人看的。项目的是项目路径（从目录名认不回来时就是目录名）
	Label   string  `json:"label"`
	Dir     string  `json:"dir"`
	Index   bool    `json:"index"` // 有没有 MEMORY.md
	Entries []Entry `json:"entries"`
	ModTime int64   `json:"modTime"` // 最近一次改动，排序用
}

// Roots 是三家记忆的根目录。
type Roots struct {
	Native string // <GERDR_DIR>/native/memory
	Claude string // ~/.claude/projects
	Codex  string // ~/.codex/memories
}

func DefaultRoots(gerdrDir string) Roots {
	home, _ := os.UserHomeDir()
	return Roots{
		Native: filepath.Join(gerdrDir, "native", "memory"),
		Claude: filepath.Join(home, ".claude", "projects"),
		Codex:  filepath.Join(home, ".codex", "memories"),
	}
}

// claudeSlug：Claude Code 的项目目录名 —— 每个非字母数字的字符换成 -（/Users/me/a.b → -Users-me-a-b）。
var claudeChars = regexp.MustCompile(`[^A-Za-z0-9]`)

func claudeSlug(p string) string { return claudeChars.ReplaceAllString(p, "-") }

// List 列出所有记忆。cwds 是已知的工作目录（gerdr 里各会话的），拿来把目录名认回项目路径；nativeSlug 是 native 的项目目录名规则。
// Claude 那边没有记忆文件的项目目录（绝大多数）不列；native 的全局目录和 Codex 即使是空的也列（让人知道在哪）。
func List(r Roots, cwds []string, nativeSlug func(cwd string) string) []Store {
	labels := map[string]string{} // 目录名 → 项目路径
	home, _ := os.UserHomeDir()
	for _, c := range append(cwds, home) {
		if c == "" {
			continue
		}
		for _, p := range []string{c, gitRoot(c)} {
			if p == "" {
				continue
			}
			labels["claude:"+claudeSlug(p)] = p
			if nativeSlug != nil {
				labels["native:"+nativeSlug(p)] = p
			}
		}
	}
	var out []Store
	if r.Native != "" {
		g := scan(Store{ID: filepath.Join(r.Native, "global"), Agent: "native", Scope: "global", Label: "全局（所有项目）", Dir: filepath.Join(r.Native, "global")})
		out = append(out, g)
		ents, _ := os.ReadDir(filepath.Join(r.Native, "projects"))
		for _, e := range ents {
			if !e.IsDir() {
				continue
			}
			dir := filepath.Join(r.Native, "projects", e.Name())
			label := labels["native:"+e.Name()]
			if label == "" {
				label = e.Name()
			}
			if s := scan(Store{ID: dir, Agent: "native", Scope: "project", Label: label, Dir: dir}); len(s.Entries) > 0 || s.Index {
				out = append(out, s)
			}
		}
	}
	if r.Claude != "" {
		ents, _ := os.ReadDir(r.Claude)
		for _, e := range ents {
			dir := filepath.Join(r.Claude, e.Name(), "memory")
			if st, err := os.Stat(dir); err != nil || !st.IsDir() {
				continue
			}
			label := labels["claude:"+e.Name()]
			if label == "" {
				label = claudeCwd(filepath.Join(r.Claude, e.Name()))
			}
			if label == "" {
				label = e.Name()
			}
			scope := "project"
			if home != "" && e.Name() == claudeSlug(home) {
				label = "~（在家目录里开的会话）"
			}
			if s := scan(Store{ID: dir, Agent: "claude", Scope: scope, Label: label, Dir: dir}); len(s.Entries) > 0 || s.Index {
				out = append(out, s)
			}
		}
	}
	if r.Codex != "" {
		if _, err := os.Stat(r.Codex); err == nil {
			out = append(out, scan(Store{ID: r.Codex, Agent: "codex", Scope: "global", Label: "全局（从过去的会话自动总结）", Dir: r.Codex}))
		}
	}
	order := map[string]int{"native": 0, "claude": 1, "codex": 2}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if order[a.Agent] != order[b.Agent] {
			return order[a.Agent] < order[b.Agent]
		}
		if a.Scope != b.Scope {
			return a.Scope == "global"
		}
		return a.ModTime > b.ModTime
	})
	return out
}

// scan 读一个记忆目录：每个 .md（除了 MEMORY.md）一条，按改动时间新的在前。Codex 的可能有子目录，一起扫。
func scan(s Store) Store {
	s.Entries = []Entry{}
	_ = filepath.WalkDir(s.Dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.EqualFold(filepath.Ext(p), ".md") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if mt := info.ModTime().UnixMilli(); mt > s.ModTime {
			s.ModTime = mt
		}
		rel, _ := filepath.Rel(s.Dir, p)
		if rel == Index {
			s.Index = true
			return nil
		}
		e := Entry{File: rel, Path: p, Size: info.Size(), ModTime: info.ModTime().UnixMilli()}
		if b, err := readHead(p, 4096); err == nil {
			e.Name, e.Description, e.Type = frontmatter(b)
		}
		if e.Name == "" {
			e.Name = strings.TrimSuffix(filepath.Base(rel), filepath.Ext(rel))
		}
		s.Entries = append(s.Entries, e)
		return nil
	})
	sort.Slice(s.Entries, func(i, j int) bool { return s.Entries[i].ModTime > s.Entries[j].ModTime })
	return s
}

func readHead(p string, n int) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	b := make([]byte, n)
	k, _ := f.Read(b)
	return string(b[:k]), nil
}

// frontmatter 读开头 --- 之间的 name / description / type（Claude 的新格式把 type 放在 metadata 下面，缩进的也认）。
func frontmatter(s string) (name, desc, typ string) {
	if !strings.HasPrefix(s, "---") {
		return
	}
	lines := strings.Split(s, "\n")
	for _, l := range lines[1:] {
		t := strings.TrimSpace(l)
		if t == "---" {
			break
		}
		k, v, ok := strings.Cut(t, ":")
		if !ok {
			continue
		}
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		switch strings.TrimSpace(k) {
		case "name":
			name = v
		case "description":
			desc = v
		case "type":
			typ = v
		}
	}
	return
}

func gitRoot(dir string) string {
	for d := dir; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		if filepath.Dir(d) == d {
			return ""
		}
	}
}

// ---- 读 / 写 / 删 ----

var ErrNotAllowed = errors.New("只能读写记忆目录里的 .md 文件")

// Allowed：p 是不是某家记忆目录里的 .md。符号链接解开再判断（链到别处的不算）；文件还不存在（新建）时看它所在的目录。
func Allowed(r Roots, p string) (string, error) {
	if !filepath.IsAbs(p) || !strings.EqualFold(filepath.Ext(p), ".md") {
		return "", ErrNotAllowed
	}
	p = filepath.Clean(p)
	real := resolve(p)
	// 原样的路径对原样的根、解开的路径对解开的根，两个都得在里面（macOS 的 /var、/tmp 本身就是链接，混着比会对不上）
	if !inRoots(r, p, false) || !inRoots(r, real, true) {
		return "", ErrNotAllowed
	}
	return p, nil
}

// resolve：解开符号链接。文件 / 目录还不存在（新建一条记忆）时，解开最深一层存在的祖先，再接上后面那段。
func resolve(p string) string {
	rest := ""
	for d := p; ; d = filepath.Dir(d) {
		if rd, err := filepath.EvalSymlinks(d); err == nil {
			return filepath.Join(rd, rest)
		}
		if filepath.Dir(d) == d {
			return p
		}
		rest = filepath.Join(filepath.Base(d), rest)
	}
}

func inRoots(r Roots, p string, resolve bool) bool {
	norm := func(root string) string {
		if resolve {
			if rr, err := filepath.EvalSymlinks(root); err == nil {
				return rr
			}
		}
		return filepath.Clean(root)
	}
	rel := func(root string) (string, bool) {
		if root == "" {
			return "", false
		}
		x, err := filepath.Rel(norm(root), p)
		return x, err == nil && x != "." && !strings.HasPrefix(x, "..")
	}
	if _, ok := rel(r.Native); ok {
		return true
	}
	if _, ok := rel(r.Codex); ok {
		return true
	}
	// Claude 只认 <项目>/memory/ 下面的：~/.claude/projects 里还有会话记录（.jsonl）等别的东西
	if x, ok := rel(r.Claude); ok {
		parts := strings.Split(filepath.ToSlash(x), "/")
		return len(parts) >= 3 && parts[1] == "memory"
	}
	return false
}

func Read(r Roots, p string) (string, error) {
	p, err := Allowed(r, p)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(p)
	return string(b), err
}

func Write(r Roots, p, content string) error {
	p, err := Allowed(r, p)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	// 先写临时文件再改名：agent 可能正好在读
	tmp := fmt.Sprintf("%s.tmp-%d", p, time.Now().UnixNano())
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// Delete 删掉一条记忆，并把同目录 MEMORY.md 里指向它的那几行一起删掉（不然索引里留着一条找不到文件的）。
func Delete(r Roots, p string) error {
	p, err := Allowed(r, p)
	if err != nil {
		return err
	}
	if filepath.Base(p) == Index {
		return errors.New("索引不能删，改它的内容就行")
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	idx := filepath.Join(filepath.Dir(p), Index)
	b, err := os.ReadFile(idx)
	if err != nil {
		return nil
	}
	name := filepath.Base(p)
	var keep []string
	changed := false
	for _, l := range strings.Split(string(b), "\n") {
		if strings.Contains(l, "("+name+")") || strings.Contains(l, "(./"+name+")") {
			changed = true
			continue
		}
		keep = append(keep, l)
	}
	if !changed {
		return nil
	}
	return Write(r, idx, strings.Join(keep, "\n"))
}

// claudeCwd：从 Claude 项目目录里的会话记录认回项目路径（目录名是 cwd 的有损变形，认不回来；记录里每行带着 cwd）。
// 只读一个 .jsonl 的开头几十 KB。
func claudeCwd(dir string) string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		head, err := readHead(filepath.Join(dir, e.Name()), 64<<10)
		if err != nil {
			continue
		}
		if m := cwdField.FindStringSubmatch(head); m != nil {
			var s string
			if json.Unmarshal([]byte(`"`+m[1]+`"`), &s) == nil && filepath.IsAbs(s) {
				return s
			}
		}
	}
	return ""
}

var cwdField = regexp.MustCompile(`"cwd"\s*:\s*"((?:[^"\\]|\\.)*)"`)
