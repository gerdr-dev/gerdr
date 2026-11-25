// Package worktree moves an agent session into a git worktree of its repository and back: the enter_worktree /
// exit_worktree tools of Gerdr's own agent and of Codex (Claude Code has its own EnterWorktree / ExitWorktree, which
// these follow). The adapter then works there and reports it (acpx.WorkdirKey), so Gerdr shows and resumes the
// session in the worktree.
//
// Worktrees go inside the repository, <repo>/.gerdr/worktrees/<name>, kept out of git status through the repository's
// own info/exclude (no .gitignore change to commit). Inside rather than beside it because Codex's sandbox only lets a
// turn write under the directory the turn started in: a worktree entered mid-turn next to the repository refuses every
// write until the next turn (measured, codex 0.166). Claude Code keeps its own in .claude/worktrees for the same reason.
package worktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/gerdr-dev/gerdr/sysenv"
)

// Sub is where worktrees go, relative to the main checkout.
const Sub = ".gerdr/worktrees"

// Entered is where a session works after Enter.
type Entered struct {
	Dir    string // where to work: the same folder of the worktree as the session's folder is of the repository
	Root   string // the worktree's top folder
	Branch string
	Base   string // what the new branch started from ("" when the branch or the worktree was already there)
	Reused bool   // the worktree existed: entered, not made
}

var nameRe = regexp.MustCompile(`^[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)*$`)

// ValidName: the worktree's folder and its branch, so letters, digits, . _ - and / between parts, at most 64.
func ValidName(name string) error {
	if name == "" || len(name) > 64 || !nameRe.MatchString(name) || strings.Contains(name, "..") || strings.HasSuffix(name, ".lock") {
		return fmt.Errorf("worktree name %q: use letters, digits, '.', '_', '-' and '/' between parts, at most 64 characters", name)
	}
	return nil
}

func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = sysenv.ChildEnv("GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	s := strings.TrimSpace(string(out))
	if err != nil {
		if s == "" {
			s = err.Error()
		}
		return s, fmt.Errorf("git %s: %s", strings.Join(args, " "), s)
	}
	return s, nil
}

// mainRoot: the main checkout of the repository dir is in (also when dir is inside one of its worktrees, so worktrees
// never nest), and dir's folder relative to the checkout it is in.
func mainRoot(ctx context.Context, dir string) (root, sub string, err error) {
	top, err := git(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", "", fmt.Errorf("%s is not in a git repository", dir)
	}
	common, err := git(ctx, dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", "", err
	}
	if filepath.Base(common) != ".git" {
		return "", "", fmt.Errorf("%s: a bare repository has no checkout to make a worktree beside", dir)
	}
	root = filepath.Dir(common)
	if d, err := filepath.EvalSymlinks(dir); err == nil {
		dir = d
	}
	if t, err := filepath.EvalSymlinks(top); err == nil {
		top = t
	}
	sub, err = filepath.Rel(top, dir)
	if err != nil || strings.HasPrefix(sub, "..") {
		sub = "."
	}
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	return root, sub, nil
}

// Enter makes the worktree name of the repository dir is in (or enters it when it is there already), on a new branch
// called name from the remote's default branch, fetched first (like Claude Code's default), else from HEAD.
func Enter(ctx context.Context, dir, name string) (Entered, error) {
	if err := ValidName(name); err != nil {
		return Entered{}, err
	}
	root, sub, err := mainRoot(ctx, dir)
	if err != nil {
		return Entered{}, err
	}
	wt := filepath.Join(root, filepath.FromSlash(Sub), filepath.FromSlash(name))
	e := Entered{Root: wt, Branch: name, Dir: filepath.Join(wt, sub)}
	if st, err := os.Stat(filepath.Join(wt, ".git")); err == nil && !st.IsDir() {
		e.Reused = true
		if b, err := git(ctx, wt, "branch", "--show-current"); err == nil {
			e.Branch = b
		}
		return e.inside(), nil
	}
	if err := exclude(ctx, root); err != nil {
		return Entered{}, err
	}
	args := []string{"worktree", "add"}
	if _, err := git(ctx, root, "rev-parse", "--verify", "--quiet", "refs/heads/"+name); err == nil {
		args = append(args, wt, name) // the branch is there already (a worktree removed, its branch kept): check it out
	} else {
		e.Base = base(ctx, root)
		args = append(args, "-b", name, wt, e.Base)
	}
	if _, err := git(ctx, root, args...); err != nil {
		return Entered{}, err
	}
	return e.inside(), nil
}

// inside: Dir falls back to the worktree's top when the session's folder isn't in it (not committed yet).
func (e Entered) inside() Entered {
	if st, err := os.Stat(e.Dir); err != nil || !st.IsDir() {
		e.Dir = e.Root
	}
	return e
}

// base: origin's default branch, fetched (a few seconds at most: a slow network shouldn't hold the tool up), else HEAD.
func base(ctx context.Context, root string) string {
	ref, err := git(ctx, root, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	if err != nil || !strings.HasPrefix(ref, "origin/") {
		return "HEAD"
	}
	fctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	_, _ = git(fctx, root, "fetch", "--quiet", "origin", strings.TrimPrefix(ref, "origin/"))
	return ref
}

// exclude adds the worktrees' folder to the repository's info/exclude, once: without it the main checkout's git
// status lists them as an untracked folder.
func exclude(ctx context.Context, root string) error {
	p, err := git(ctx, root, "rev-parse", "--path-format=absolute", "--git-path", "info/exclude")
	if err != nil {
		return err
	}
	line := "/" + Sub + "/"
	b, _ := os.ReadFile(p)
	for _, l := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(l) == line {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if len(b) > 0 && !strings.HasSuffix(string(b), "\n") {
		line = "\n" + line
	}
	_, err = f.WriteString(line + "\n# (Gerdr's agent worktrees)\n")
	return err
}

// ErrUnsaved: Remove found work that only lives in the worktree.
var ErrUnsaved = errors.New("the worktree has work that isn't saved anywhere else")

// Remove deletes the worktree whose top folder is root, and its branch. Unless discard, it refuses (ErrUnsaved, with
// what it found) while there are uncommitted changes or commits on no remote branch.
func Remove(ctx context.Context, root string, discard bool) error {
	branch, _ := git(ctx, root, "branch", "--show-current")
	if !discard {
		var left []string
		if s, err := git(ctx, root, "status", "--porcelain"); err != nil {
			return err
		} else if s != "" {
			left = append(left, "uncommitted changes:\n"+s)
		}
		if branch != "" {
			if s, err := git(ctx, root, "log", "--oneline", branch, "--not", "--remotes"); err == nil && s != "" {
				left = append(left, "commits not pushed:\n"+s)
			}
		}
		if len(left) > 0 {
			return fmt.Errorf("%w\n%s", ErrUnsaved, strings.Join(left, "\n"))
		}
	}
	common, err := git(ctx, root, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return err
	}
	main := filepath.Dir(common)
	args := []string{"worktree", "remove", root}
	if discard {
		args = []string{"worktree", "remove", "--force", root}
	}
	if _, err := git(ctx, main, args...); err != nil {
		return err
	}
	if branch != "" {
		_, _ = git(ctx, main, "branch", "-D", branch) // its commits are on a remote, or the caller chose to discard them
	}
	return nil
}

// Home: the session's own folder for dir when dir is in one of these worktrees (the main checkout, same subfolder),
// else "". Lets an adapter that only knows where it works now find where to go back to.
func Home(dir string) string {
	top := RootOf(dir)
	if top == "" {
		return ""
	}
	main := top[:strings.Index(filepath.ToSlash(top), "/"+Sub+"/")]
	rel, err := filepath.Rel(top, dir)
	if err != nil || strings.HasPrefix(rel, "..") {
		rel = "."
	}
	return filepath.Join(main, rel)
}

// RootOf: the top folder of the worktree dir is in, when it is one of these, else "".
func RootOf(dir string) string {
	d := filepath.ToSlash(dir)
	i := strings.Index(d, "/"+Sub+"/")
	if i < 0 {
		return ""
	}
	root, parts := d[:i], strings.Split(d[i+len(Sub)+2:], "/")
	for n := 1; n <= len(parts); n++ {
		top := root + "/" + Sub + "/" + strings.Join(parts[:n], "/")
		if st, err := os.Stat(filepath.FromSlash(top + "/.git")); err == nil && !st.IsDir() {
			return filepath.FromSlash(top)
		}
	}
	return ""
}
