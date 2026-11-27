// Package repomem is the person's memory per repository: what agents learned from them about one repository and
// that isn't (or mustn't be) in it — whether it ships through an MR or straight to the branch, who merges, who
// approves a release, a pitfall they were told about. Every session gets the notes of the repository it works in
// (agentwire.SessionPrompt), whichever agent runs it and whoever started it: a panel's worker, a helper session it
// opened in another repository, a conversation in Code.
//
// Why here and not in a panel: the Superagent panel kept these per repository in its own files and put them into its
// workers' prompts. A helper session the worker opened in another repository (gerdr session new) never got them, nor
// did anything opened in Code, and a worker in one repository couldn't see another's (2026-11-27, the user: "这个记忆
// 能力是不是不应该由面板提供……而是直接由本体提供"). Keyed by the repository's path and shared by every Gerdr
// project: who merges is a fact about the repository, not about which project opened it.
//
// Layout: <GERDR_DIR>/repo-memory/<folder name>-<hash of the path>.md, a frontmatter naming the repository, then the
// notes as plain "- " lines the person can read and edit as they are.
package repomem

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Dir is where the notes live under a Gerdr data directory.
func Dir(root string) string { return filepath.Join(root, "repo-memory") }

// Root is the repository a directory belongs to: the top of its git work tree, and for a linked worktree (a panel's
// task worktree, `git worktree add`) the main one's, so a task in a worktree gets its repository's notes. A
// directory outside git (a working directory that is just a folder) is its own.
func Root(dir string) string {
	dir = filepath.Clean(dir)
	for d := dir; ; {
		g := filepath.Join(d, ".git")
		if st, err := os.Stat(g); err == nil {
			if st.IsDir() {
				return d
			}
			if main := worktreeMain(g); main != "" {
				return main
			}
			return d // a submodule: its own repository
		}
		up := filepath.Dir(d)
		if up == d {
			return dir
		}
		d = up
	}
}

// worktreeMain: a linked worktree's .git file says "gitdir: <main>/.git/worktrees/<name>", whose commondir file
// points at <main>/.git. "" when it isn't one.
func worktreeMain(gitFile string) string {
	b, err := os.ReadFile(gitFile)
	if err != nil {
		return ""
	}
	gd := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(b)), "gitdir:"))
	if gd == "" {
		return ""
	}
	if !filepath.IsAbs(gd) {
		gd = filepath.Join(filepath.Dir(gitFile), gd)
	}
	c, err := os.ReadFile(filepath.Join(gd, "commondir"))
	if err != nil {
		return ""
	}
	common := strings.TrimSpace(string(c))
	if !filepath.IsAbs(common) {
		common = filepath.Join(gd, common)
	}
	common = filepath.Clean(common)
	if filepath.Base(common) != ".git" {
		return ""
	}
	return filepath.Dir(common)
}

// Path is the notes file of a repository (an absolute path), whether or not it exists.
func Path(root, repo string) string {
	repo = filepath.Clean(repo)
	sum := sha256.Sum256([]byte(repo))
	name := safe(filepath.Base(repo))
	if name == "" {
		name = "repo"
	}
	return filepath.Join(Dir(root), name+"-"+hex.EncodeToString(sum[:])[:8]+".md")
}

func safe(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == ':' || r < 0x20 {
			return '_'
		}
		return r
	}, strings.TrimLeft(s, "."))
}

// Notes of one repository.
type Notes struct {
	Repo string `json:"repo"`
	Path string `json:"path"`
	Text string `json:"text"` // the "- " lines, without the frontmatter; "" = none yet
}

// Read returns a repository's notes (Text "" when there are none).
func Read(root, repo string) Notes {
	repo = filepath.Clean(repo)
	p := Path(root, repo)
	n := Notes{Repo: repo, Path: p}
	if b, err := os.ReadFile(p); err == nil {
		_, n.Text = split(string(b))
	}
	return n
}

// Write replaces a repository's notes; an empty text removes the file.
func Write(root, repo, text string) (Notes, error) {
	if !filepath.IsAbs(repo) {
		return Notes{}, fmt.Errorf("repository must be an absolute path: %q", repo)
	}
	repo = filepath.Clean(repo)
	p := Path(root, repo)
	text = strings.TrimSpace(text)
	if text == "" {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return Notes{}, err
		}
		return Notes{Repo: repo, Path: p}, nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return Notes{}, err
	}
	body := "---\nrepo: " + repo + "\n---\n" + text + "\n"
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, []byte(body), 0o600); err != nil {
		return Notes{}, err
	}
	if err := os.Rename(tmp, p); err != nil {
		return Notes{}, err
	}
	return Notes{Repo: repo, Path: p, Text: text}, nil
}

// Add appends one "- " line, unless the same fact (ignoring a trailing "（…）" note) is already there. → whether added.
func Add(root, repo, line string) (Notes, bool, error) {
	line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "- "))
	if line == "" {
		return Notes{}, false, errors.New("nothing to add")
	}
	cur := Read(root, repo)
	for _, l := range strings.Split(cur.Text, "\n") {
		if bare(l) == bare(line) {
			return cur, false, nil
		}
	}
	text := strings.TrimSpace(cur.Text)
	if text != "" {
		text += "\n"
	}
	n, err := Write(root, repo, text+"- "+line)
	return n, err == nil, err
}

// bare is a line without its "- " and a trailing "（date, who）".
func bare(l string) string {
	l = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(l), "- "))
	if i := strings.LastIndex(l, "（"); i > 0 && strings.HasSuffix(l, "）") {
		l = strings.TrimSpace(l[:i])
	}
	return l
}

// List is every repository with notes, by path.
func List(root string) []Notes {
	ents, _ := os.ReadDir(Dir(root))
	var out []Notes
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		p := filepath.Join(Dir(root), e.Name())
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		repo, text := split(string(b))
		if repo == "" || text == "" {
			continue
		}
		out = append(out, Notes{Repo: repo, Path: p, Text: text})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Repo < out[j].Repo })
	return out
}

// split: the frontmatter's repo, and the rest.
func split(s string) (repo, text string) {
	s = strings.TrimPrefix(s, "\uFEFF")
	if strings.HasPrefix(s, "---\n") {
		if end := strings.Index(s[4:], "\n---"); end >= 0 {
			for _, l := range strings.Split(s[4:4+end], "\n") {
				if k, v, ok := strings.Cut(l, ":"); ok && strings.TrimSpace(k) == "repo" {
					repo = strings.TrimSpace(v)
				}
			}
			s = s[4+end+4:]
		}
	}
	return repo, strings.TrimSpace(s)
}
