package hub

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/coder/acp-go-sdk"
)

// Telling a long-lived agent its skills changed (Record.SkillUpdates).
//
// A skill file edited on disk is live (the session's roots are links), but an agent that read it on day one keeps
// what it read then and has no reason to look again: a panel's task runs for one or two weeks, its rules change in
// between, and it went on with the old ones (user report). Claude Code notices a file it Read changing, but only
// while that file is in its read-file cache, which compaction empties; codex and native have nothing like it. So the
// next message says which files changed, the way `!` output rides along (shell.go): one block in front, English for
// the agent, and a tag the chat draws as a one-line notice (ui transcript.ts, SKILLS_CHANGED).

// skillsPrefix starts the block. The UI, titles and "put it back in the composer" recognise it by this and skip it.
const skillsPrefix = "<gerdr-skills-changed>"

const (
	skillScanMax  = 2000 // entries looked at across all roots: a root that is really a big tree isn't walked whole
	skillScanDeep = 8    // directory depth, which also ends a symlink loop
	skillListMax  = 20   // files named in the block; the rest are counted
)

func isSkillsBlock(raw json.RawMessage) bool {
	var b struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	return json.Unmarshal(raw, &b) == nil && b.Type == "text" && strings.HasPrefix(b.Text, skillsPrefix)
}

// injectedText: a text block Gerdr put in front of a message (`!` output, the earlier conversation, changed skills),
// not something anyone said: titles, folds and "what the person said" skip it.
func injectedText(t string) bool {
	return strings.HasPrefix(t, shellPrefix) || strings.HasPrefix(t, handoffPrefix) || strings.HasPrefix(t, skillsPrefix)
}

type skillFile struct{ rel, path string }

// changedSkillFiles: the files under roots modified after since (ms), sorted. Links are followed (a panel hands its
// skills over as a tree of links, user/.skills) and the target's time counts. Hidden entries are skipped.
func changedSkillFiles(roots []string, since int64) []skillFile {
	var out []skillFile
	n := 0
	var walk func(root, dir string, depth int)
	walk = func(root, dir string, depth int) {
		if depth > skillScanDeep {
			return
		}
		ents, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range ents {
			if strings.HasPrefix(e.Name(), ".") {
				continue
			}
			if n++; n > skillScanMax {
				return
			}
			p := filepath.Join(dir, e.Name())
			st, err := os.Stat(p)
			if err != nil {
				continue
			}
			if st.IsDir() {
				walk(root, p, depth+1)
			} else if st.ModTime().UnixMilli() > since {
				rel, _ := filepath.Rel(root, p)
				out = append(out, skillFile{rel: filepath.ToSlash(rel), path: p})
			}
		}
	}
	for _, r := range roots {
		walk(r, r, 0)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].rel < out[j].rel })
	return out
}

// skillsText is the block: one "- <name>: <path>" line per file (the chat shows the names).
func skillsText(files []skillFile) string {
	var b strings.Builder
	b.WriteString(skillsPrefix + "\n")
	b.WriteString("These skill files changed since the last message. What you read of them before is out of date: read each again before you next act on it.\n")
	for i, f := range files {
		if i == skillListMax {
			fmt.Fprintf(&b, "- … and %d more\n", len(files)-skillListMax)
			break
		}
		fmt.Fprintf(&b, "- %s: %s\n", f.rel, f.path)
	}
	b.WriteString("</gerdr-skills-changed>")
	return b.String()
}

// skillsChanged: the block naming the session's skill files changed since the last check, nil when nothing changed
// or SkillUpdates is off. Moves the mark to now either way, so a change is told once.
func (s *Session) skillsChanged() []acp.ContentBlock {
	s.mu.Lock()
	rec := s.rec
	s.mu.Unlock()
	if !rec.SkillUpdates {
		return nil
	}
	at := now() // before the walk: a file changed while walking is news next time too
	var files []skillFile
	if rec.SkillsSeen > 0 {
		files = changedSkillFiles(s.h.skillRoots(rec), rec.SkillsSeen)
	}
	s.mu.Lock()
	s.rec.SkillsSeen = at
	s.mu.Unlock()
	s.h.saveIndex()
	if len(files) == 0 {
		return nil
	}
	return []acp.ContentBlock{acp.TextBlock(skillsText(files))}
}
