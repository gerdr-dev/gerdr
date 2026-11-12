package agentwire

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gerdr-dev/gerdr/acpx"
	"github.com/gerdr-dev/gerdr/internal/ext"
)

// projectIndexMax caps the memory index put into the prompt, like Claude Code's own MEMORY.md (it loads the first
// ~200 lines): the index is one line per entry, and a runaway one shouldn't eat the context of every session.
const projectIndexMax = 25 << 10

// ProjectPrompt is the text appended to a session's system prompt about its Gerdr project's memory
// (<GERDR_DIR>/projects/<id>/memory, ext.ProjectMemoryDir): where it is, what belongs in it, and its index as it
// is now. "" without a project (or em nil: the stand-alone adapters). The index goes in whole and the entries are
// read on demand, the way Claude Code's auto memory and native's memory work. Read when the agent starts (claude:
// the process, codex: the thread, native: every model call), so an entry another session adds shows up in the
// next one; an index change doesn't restart a running session (it would cost a cold prompt cache for little).
func ProjectPrompt(em *ext.Manager) func(sessionID, cwd string, meta map[string]any) string {
	return func(_, _ string, meta map[string]any) string {
		if em == nil {
			return ""
		}
		p, _ := acpx.GetMeta(meta, ProjectKey)
		project, _ := p.(string)
		dir, err := em.ProjectMemoryDir(project)
		if err != nil {
			return ""
		}
		return projectMemoryText(project, dir)
	}
}

// SystemFileKey is the _meta key of a session's system file (hub Record.SystemFile).
const SystemFileKey = "systemFile"

// systemFileMax caps what a system file puts into every prompt.
const systemFileMax = 200 << 10

// SessionPrompt is everything gerdr appends to a session's system prompt: the project's memory, then the text of the
// session's system file if it has one (a panel's instructions for the agent it runs: kept out of the chat, and
// changed by rewriting the file — the next process / thread / call reads the new text).
func SessionPrompt(em *ext.Manager) func(sessionID, cwd string, meta map[string]any) string {
	project := ProjectPrompt(em)
	return func(id, cwd string, meta map[string]any) string {
		parts := []string{}
		if p := strings.TrimSpace(project(id, cwd, meta)); p != "" {
			parts = append(parts, p)
		}
		if t := systemFileText(meta); t != "" {
			parts = append(parts, t)
		}
		return strings.Join(parts, "\n\n")
	}
}

func systemFileText(meta map[string]any) string {
	v, _ := acpx.GetMeta(meta, SystemFileKey)
	path, _ := v.(string)
	if path == "" {
		return ""
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	t := strings.TrimSpace(string(b))
	if len(t) > systemFileMax {
		t = t[:systemFileMax] + "\n… (truncated)"
	}
	return t
}

func projectMemoryText(project, dir string) string {
	idx, _ := os.ReadFile(filepath.Join(dir, "MEMORY.md"))
	index := strings.TrimSpace(string(idx))
	if len(index) > projectIndexMax {
		index = index[:projectIndexMax] + "\n… (index truncated; read MEMORY.md for the rest, and shorten it)"
	}
	if index == "" {
		index = "(empty)"
	}
	return fmt.Sprintf(`# Project memory (Gerdr project %q)

This conversation belongs to the Gerdr project %q, which can span several repositories. The project has a memory shared by all its conversations, whatever agent runs them, kept in %s:
- One fact per .md file, with frontmatter (name: short-kebab-slug, description: one line used to judge relevance, type: user | feedback | project | reference) and the fact as the body; for feedback / project, add **Why:** and **How to apply:** lines.
- MEMORY.md is the index, one line per file: "- [Title](file.md) — hook". Update it whenever you add, rename or delete a file. Its current content is below; read an entry's file when its line looks relevant.
- Save here what holds across the project's repositories and conversations: how the team works, where things are, decisions and why, the person's preferences for this project. Facts about a single repository belong in that repository (its CLAUDE.md / AGENTS.md) or your own memory; don't duplicate them here. Don't save what the code or git history already records.
- Before saving, check for an entry that already covers it and update that one instead. Delete entries that turn out to be wrong. The person also edits these files in Gerdr, so read a file again before changing it.

Current MEMORY.md:
%s`, project, project, dir, index)
}
