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

// A project's Work is a whole app (pages, local functions, skills, schedules), so it can come from somewhere: a git
// repository someone published (Install), or the built-in blank one (Reset). Either way the Work directory there now is
// moved aside, whole, to trash/work/<project>-<time>, not deleted: its data/ and anything the user made stay
// recoverable. Secrets aren't in it (secrets.json), so a Work installed over another still finds them by name.

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

// Install replaces the project's Work with a clone of url. The old one goes to the trash (its path is returned).
func (r *Runner) Install(ctx context.Context, project, url string) (trash string, err error) {
	u, err := repoURL(url)
	if err != nil {
		return "", err
	}
	return r.replace(project, func(dir string) error {
		cmd := exec.CommandContext(ctx, "git", "clone", "--", u, dir)
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0") // a private repo fails instead of waiting for a password nobody sees
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git clone %s: %s", u, strings.TrimSpace(string(out)))
		}
		if _, err := os.Stat(filepath.Join(dir, "ui", "package.json")); err != nil {
			return fmt.Errorf("%s isn't a Gerdr Work: it has no ui/package.json", u)
		}
		return nil
	})
}

// Reset replaces the project's Work with the built-in blank one.
func (r *Runner) Reset(project string) (trash string, err error) {
	return r.replace(project, func(string) error { return nil })
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

// replace moves the Work directory to the trash, lets fill make the new one, and installs the built-in files it
// lacks (a repository's own versions of them are kept: they count as edited). A failed fill puts the old one back.
func (r *Runner) replace(project string, fill func(dir string) error) (string, error) {
	dir, err := r.Dir(project)
	if err != nil {
		return "", err
	}
	if project == "" {
		project = DefaultProject
	}
	var trash string
	if _, err := os.Stat(dir); err == nil {
		base := filepath.Join(r.Root, "trash", "work")
		if err := os.MkdirAll(base, 0o755); err != nil {
			return "", err
		}
		trash = filepath.Join(base, project+"-"+time.Now().Format("20060102-150405"))
		for i := 2; ; i++ {
			if _, err := os.Stat(trash); os.IsNotExist(err) {
				break
			}
			trash = filepath.Join(base, fmt.Sprintf("%s-%s-%d", project, time.Now().Format("20060102-150405"), i))
		}
		if err := os.Rename(dir, trash); err != nil {
			return "", err
		}
	}
	r.mu.Lock()
	delete(r.installed, dir)
	r.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return "", err
	}
	if err := fill(dir); err != nil {
		_ = os.RemoveAll(dir)
		if trash != "" {
			_ = os.Rename(trash, dir)
		}
		return "", err
	}
	if _, err := r.Ensure(project); err != nil {
		return trash, err
	}
	return trash, nil
}

func git(dir string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	return strings.TrimSpace(string(out)), err
}
