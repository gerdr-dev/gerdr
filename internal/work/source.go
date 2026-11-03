package work

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
)

// A Work is a whole app (pages, local functions, skills, schedules), so a project can have more than its own: panels
// installed from a git repository someone published (Install), or blank ones to build (NewPanel). Adding never
// replaces anything; removing a panel moves it, data and all, to trash/panels/, never deletes it. Secrets aren't in
// a Work (secrets.json), so a panel finds them by name like any other.

// Source is where a project's Work came from, for the menu that updates it.
type Source struct {
	Git    bool   `json:"git"`
	Remote string `json:"remote,omitempty"`
	Branch string `json:"branch,omitempty"`
	// Dirty: changes not committed (the user's or their agent's edits); an update keeps them or refuses.
	Dirty bool `json:"dirty,omitempty"`
}

func (r *Runner) Source(project string) (Source, error) {
	dir, err := r.Dir(project)
	if err != nil {
		return Source{}, err
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return Source{}, nil
	}
	s := Source{Git: true}
	s.Remote, _ = git(dir, "remote", "get-url", "origin")
	s.Branch, _ = git(dir, "rev-parse", "--abbrev-ref", "HEAD")
	st, _ := git(dir, "status", "--porcelain")
	s.Dirty = st != ""
	return s, nil
}

// repoURL accepts what people copy from GitHub: https://github.com/o/r(.git), github.com/o/r, o/r, git@…:o/r.git.
var shortRepo = regexp.MustCompile(`^[\w.-]+/[\w.-]+$`)

func repoURL(s string) (string, error) {
	s = strings.TrimSpace(s)
	switch {
	case s == "":
		return "", errors.New("no repository given")
	case strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "git@") || strings.HasPrefix(s, "ssh://") || strings.HasPrefix(s, "file://"):
		return strings.TrimSuffix(s, "/"), nil
	case strings.HasPrefix(s, "github.com/") || strings.HasPrefix(s, "gitlab.com/"):
		return "https://" + strings.TrimSuffix(s, "/"), nil
	case shortRepo.MatchString(s):
		return "https://github.com/" + s, nil
	}
	return "", fmt.Errorf("%q doesn't look like a git repository address (https://github.com/owner/repo)", s)
}

// Install adds a panel to the project: a clone of url, named name (or after the repository). Its key is returned.
func (r *Runner) Install(ctx context.Context, project, url, name string) (string, error) {
	u, err := repoURL(url)
	if err != nil {
		return "", err
	}
	if name == "" {
		name = nameFromURL(u)
	}
	return r.addPanel(project, name, func(dir string) error {
		cmd := exec.CommandContext(ctx, "git", "clone", "--", u, dir)
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0") // a private repo fails instead of waiting for a password nobody sees
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git clone %s: %s", u, strings.TrimSpace(string(out)))
		}
		if _, err := os.Stat(filepath.Join(dir, "ui", "package.json")); err != nil {
			return fmt.Errorf("%s isn't a Gerdr panel: it has no ui/package.json", u)
		}
		return nil
	})
}

// NewPanel adds a blank panel (the built-in Work) to the project, for its agent to build.
func (r *Runner) NewPanel(project, name string) (string, error) {
	return r.addPanel(project, name, func(dir string) error { return os.MkdirAll(dir, 0o755) })
}

// RemovePanel moves a panel to trash/panels/<project>-<name>-<time> (a project's own Work can't be removed this way).
func (r *Runner) RemovePanel(key string) (string, error) {
	project, panel := SplitKey(key)
	if panel == "" {
		return "", errors.New("a project's own Work isn't a panel to remove")
	}
	dir, err := r.Dir(key)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(dir); err != nil {
		return "", fmt.Errorf("no panel %q in %s", panel, project)
	}
	base := filepath.Join(r.Root, "trash", PanelsDir)
	if err := os.MkdirAll(base, 0o755); err != nil {
		return "", err
	}
	trash := filepath.Join(base, project+"-"+panel+"-"+time.Now().Format("20060102-150405"))
	if err := os.Rename(dir, trash); err != nil {
		return "", err
	}
	r.mu.Lock()
	delete(r.installed, dir)
	r.mu.Unlock()
	return trash, nil
}

var badName = regexp.MustCompile(`[/\\.\s]+`)

// nameFromURL: the repository's name ("https://github.com/o/task-board.git" → "task-board").
func nameFromURL(u string) string {
	u = strings.TrimSuffix(strings.TrimSuffix(u, "/"), ".git")
	if i := strings.LastIndexAny(u, "/:"); i >= 0 {
		u = u[i+1:]
	}
	u = strings.Trim(badName.ReplaceAllString(u, "-"), "-")
	if u == "" {
		u = "panel"
	}
	return u
}

// addPanel makes <panels>/<name> (another name if it's taken: name-2…) with fill, then adds the built-in files it
// lacks (a repository's own versions of them count as edited and stay). A failed fill leaves nothing behind.
func (r *Runner) addPanel(project, name string, fill func(dir string) error) (string, error) {
	if project == "" {
		project = DefaultProject
	}
	name = strings.TrimSpace(name)
	if !validProject(name) {
		return "", fmt.Errorf("bad panel name %q: no / \\ . or control characters", name)
	}
	key := PanelKey(project, name)
	dir, err := r.Dir(key)
	if err != nil {
		return "", err
	}
	for i := 2; ; i++ {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			break
		}
		key = PanelKey(project, fmt.Sprintf("%s-%d", name, i))
		if dir, err = r.Dir(key); err != nil {
			return "", err
		}
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return "", err
	}
	if err := fill(dir); err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	r.mu.Lock()
	delete(r.installed, dir)
	r.mu.Unlock()
	if _, err := r.Ensure(key); err != nil {
		return key, err
	}
	return key, nil
}

// Update pulls the project's Work repository (fast-forward only: local edits are never merged over behind the user's back).
func (r *Runner) Update(ctx context.Context, project string) (string, error) {
	dir, err := r.Dir(project)
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "pull", "--ff-only")
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git pull: %s", strings.TrimSpace(string(out)))
	}
	r.mu.Lock()
	delete(r.installed, dir) // built-in files it doesn't have come back
	r.mu.Unlock()
	if _, err := r.Ensure(project); err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func git(dir string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	return strings.TrimSpace(string(out)), err
}
