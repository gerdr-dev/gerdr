// Package desktopimport lists the Code-tab conversations of the Claude Desktop App on this computer, for Gerdr's
// "Import from Claude Desktop".
//
// The Desktop App's Code tab runs Claude Code underneath. For each conversation it keeps a small JSON file at
// <config dir>/Claude/claude-code-sessions/<account>/<org>/local_<uuid>.json (title, cwd, timestamps) whose
// cliSessionId is an ordinary Claude Code session: its transcript is ~/.claude/projects/<cwd>/<id>.jsonl, so Gerdr
// takes it over with a plain resume (hub CreateOptions.Import), history and all.
//
// The Desktop's chat conversations (the claude.ai kind) are not here: they live on Anthropic's servers and the App
// only caches them, so there is nothing local to import.
//
// The file format is the Desktop App's internal detail, read leniently: a field it drops just leaves ours empty.
// Read only: nothing in the Desktop App's folder is changed.
package desktopimport

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type Session struct {
	ID        string    `json:"id"`        // Claude Code's session id (cliSessionId): what `claude --resume` takes
	Title     string    `json:"title"`     // the Desktop App's title for it
	Cwd       string    `json:"cwd"`       // where it ran
	Scratch   bool      `json:"scratch"`   // cwd is one of the Desktop App's own scratch folders, not a project
	Archived  bool      `json:"archived"`  // archived in the Desktop App
	Model     string    `json:"model"`     // e.g. claude-opus-5-5
	UpdatedAt time.Time `json:"updatedAt"` // last activity
	// Missing: why it can't be taken over ("" = it can): no transcript, or its folder is gone.
	Missing string `json:"missing,omitempty"`
}

// Dir is where the Desktop App keeps its Code sessions: ~/Library/Application Support/Claude on macOS, %APPDATA%\Claude
// on Windows (os.UserConfigDir is each of those).
func Dir() string {
	d, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(d, "Claude")
}

var idRe = regexp.MustCompile(`^[0-9a-fA-F-]{8,64}$`)

// Scan lists the Desktop App's Code sessions, newest first. projects is Claude Code's projects folder (where the
// transcripts are); "" = $CLAUDE_CONFIG_DIR/projects or ~/.claude/projects. No Desktop App = an empty list.
func Scan(dir, projects string) ([]Session, error) {
	if projects == "" {
		projects = ProjectsDir()
	}
	files, _ := filepath.Glob(filepath.Join(dir, "claude-code-sessions", "*", "*", "local_*.json"))
	scratch := filepath.Join(dir, "scratch-workspaces") + string(filepath.Separator)
	seen := map[string]bool{}
	out := []Session{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var r struct {
			CliSessionID   string `json:"cliSessionId"`
			Cwd            string `json:"cwd"`
			Title          string `json:"title"`
			Model          string `json:"model"`
			IsArchived     bool   `json:"isArchived"`
			CreatedAt      int64  `json:"createdAt"` // ms
			LastActivityAt int64  `json:"lastActivityAt"`
		}
		if json.Unmarshal(b, &r) != nil || !idRe.MatchString(r.CliSessionID) || seen[r.CliSessionID] {
			continue
		}
		seen[r.CliSessionID] = true
		at := r.LastActivityAt
		if at == 0 {
			at = r.CreatedAt
		}
		s := Session{
			ID:       r.CliSessionID,
			Title:    strings.TrimSpace(r.Title),
			Cwd:      r.Cwd,
			Scratch:  strings.HasPrefix(r.Cwd, scratch),
			Archived: r.IsArchived,
			Model:    r.Model,
		}
		if at > 0 {
			s.UpdatedAt = time.UnixMilli(at)
		}
		if m, _ := filepath.Glob(filepath.Join(projects, "*", s.ID+".jsonl")); len(m) == 0 {
			s.Missing = "transcript"
		} else if st, err := os.Stat(s.Cwd); err != nil || !st.IsDir() {
			// claude --resume runs in the session's folder: one deleted since can't be resumed there
			s.Missing = "cwd"
		}
		out = append(out, s)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out, nil
}

// ProjectsDir is where Claude Code (and so the Desktop App's Code tab) writes transcripts.
func ProjectsDir() string {
	d := os.Getenv("CLAUDE_CONFIG_DIR")
	if d == "" {
		home, _ := os.UserHomeDir()
		d = filepath.Join(home, ".claude")
	}
	return filepath.Join(d, "projects")
}
