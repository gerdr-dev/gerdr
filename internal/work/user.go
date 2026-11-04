package work

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// UserDir is a Work directory's per-person folder: what one person's use of a panel produces and decides, as
// opposed to the panel itself (its code and defaults), which a team shares through git. Everything personal goes in
// it, so a panel never needs an ignore rule per kind of file:
//
//	user/data/       what its functions keep (tasks, logs, caches)
//	user/triggers/   triggers this person turned on        ┐ the same path as a shared file
//	user/schedules/  schedules this person added            │ replaces it; others are added
//	user/skills/     their edits of the panel's skills      ┘ (overlay below)
//
// It ignores itself (user/.gitignore is "*", written by install), so a panel repository needs nothing for it and a
// `git add -A` there can't pick anyone's tasks, Slack ids or repository list up.
const UserDir = "user"

const userIgnore = "# This person's data and settings for this panel: never shared (Gerdr writes this file)\n*\n"

// ensureUserDir makes user/ with its .gitignore. Best effort: a panel works without it, it's only where personal
// files go.
func ensureUserDir(dir string) {
	p := filepath.Join(dir, UserDir, ".gitignore")
	if _, err := os.Stat(p); err == nil {
		return
	}
	if os.MkdirAll(filepath.Dir(p), 0o755) == nil {
		_ = os.WriteFile(p, []byte(userIgnore), 0o644)
	}
}

// overlayJSON lists sub/*.json and user/sub/*.json by id (the file name without .json), user's winning: the files a
// folder of definitions (triggers, schedules) has for this person. rel is what an error names.
func overlayJSON(dir, sub string) (map[string]struct{ rel, abs string }, error) {
	out := map[string]struct{ rel, abs string }{}
	for _, base := range []string{sub, UserDir + "/" + sub} {
		ents, err := os.ReadDir(filepath.Join(dir, filepath.FromSlash(base)))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, e := range ents {
			id, ok := strings.CutSuffix(e.Name(), ".json")
			if e.IsDir() || !ok {
				continue
			}
			out[id] = struct{ rel, abs string }{base + "/" + e.Name(), filepath.Join(dir, filepath.FromSlash(base), e.Name())}
		}
	}
	return out, nil
}

// overlayIDs is overlayJSON's ids in order.
func overlayIDs(m map[string]struct{ rel, abs string }) []string {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// overlaySig fingerprints sub/ and user/sub/ together: re-read when either changed.
func overlaySig(dir, sub string) string {
	a, b := dirSig(filepath.Join(dir, sub)), dirSig(filepath.Join(dir, UserDir, sub))
	if a == "" && b == "" {
		return ""
	}
	return a + "|" + b
}

// SkillsRoot is the skills folder a session the Work starts gets: skills/ itself while this person changed none of
// it, else user/.skills, a tree of links to every file of skills/ and user/skills/ by relative path, user's
// winning. Links, not copies: a skill edited while a session runs is what it reads next. Rebuilt on every call
// (sessions starting / resuming) by fixing only what differs, so a session reading it meanwhile isn't disturbed.
func SkillsRoot(dir string) string {
	shared := filepath.Join(dir, SkillsDir)
	mine := filepath.Join(dir, UserDir, SkillsDir)
	want := map[string]string{} // rel → the file it links to
	collect := func(root string) {
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || strings.HasPrefix(d.Name(), ".") {
				return nil
			}
			rel, _ := filepath.Rel(root, p)
			want[rel] = p
			return nil
		})
	}
	collect(mine)
	if len(want) == 0 {
		return shared
	}
	// Shared first, then this person's over it
	want = map[string]string{}
	collect(shared)
	collect(mine)
	out := filepath.Join(dir, UserDir, ".skills")
	if err := syncLinks(out, want); err != nil {
		return shared
	}
	return out
}

// syncLinks makes root a tree of symlinks: rel → target, nothing else.
func syncLinks(root string, want map[string]string) error {
	have := map[string]bool{}
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		if t, ok := want[rel]; ok {
			if cur, err := os.Readlink(p); err == nil && cur == t {
				have[rel] = true
				return nil
			}
		}
		_ = os.Remove(p)
		return nil
	})
	for rel, t := range want {
		if have[rel] {
			continue
		}
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.Symlink(t, p); err != nil {
			return fmt.Errorf("skills link %s: %w", rel, err)
		}
	}
	// Folders left empty by a removed skill
	var dirs []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() && p != root {
			dirs = append(dirs, p)
		}
		return nil
	})
	for i := len(dirs) - 1; i >= 0; i-- {
		_ = os.Remove(dirs[i]) // only succeeds when empty
	}
	return nil
}
