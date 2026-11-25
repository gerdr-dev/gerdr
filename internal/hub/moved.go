package hub

import (
	"path/filepath"
	"strings"
)

// MoveDir rewrites every session's record after a folder Gerdr owns moved (a panel's directory renamed: from → to):
// its cwd, worktree, skill dirs, system prompt file and artifacts folder under from now point under to, and a tag
// with one of the old values (tags.panel's old key) takes the new one. Unlike SetCwd the agent isn't detached and a
// running turn isn't refused: the caller leaves from as a symlink to to, so an agent already running in the old
// path keeps working, and the next spawn takes the new paths (Claude's transcript is found by id and adopted into
// the new folder's project dir, agents/claude adoptTranscript). Returns how many sessions changed.
func (h *Hub) MoveDir(from, to string, tags map[string][2]string) int {
	from, to = filepath.Clean(from), filepath.Clean(to)
	move := func(p string) string {
		if p == from {
			return to
		}
		if strings.HasPrefix(p, from+string(filepath.Separator)) {
			return to + p[len(from):]
		}
		return p
	}
	h.mu.Lock()
	ss := make([]*Session, 0, len(h.sessions))
	for _, s := range h.sessions {
		ss = append(ss, s)
	}
	h.mu.Unlock()
	var changed []*Session
	for _, s := range ss {
		s.mu.Lock()
		r := &s.rec
		before := r.Cwd + "\x00" + r.Workdir + "\x00" + r.SystemFile + "\x00" + r.ArtifactsDir + "\x00" + strings.Join(r.SkillDirs, "\x00")
		r.Cwd, r.Workdir, r.SystemFile, r.ArtifactsDir = move(r.Cwd), move(r.Workdir), move(r.SystemFile), move(r.ArtifactsDir)
		if len(r.SkillDirs) > 0 {
			dirs := make([]string, len(r.SkillDirs))
			for i, d := range r.SkillDirs {
				dirs[i] = move(d)
			}
			r.SkillDirs = dirs
		}
		moved := before != r.Cwd+"\x00"+r.Workdir+"\x00"+r.SystemFile+"\x00"+r.ArtifactsDir+"\x00"+strings.Join(r.SkillDirs, "\x00")
		for k, ch := range tags {
			if v, ok := r.Tags[k]; ok && v == ch[0] {
				t := make(map[string]string, len(r.Tags))
				for kk, vv := range r.Tags {
					t[kk] = vv
				}
				t[k] = ch[1]
				r.Tags = t
				moved = true
			}
		}
		s.mu.Unlock()
		if moved {
			changed = append(changed, s)
		}
	}
	if len(changed) > 0 {
		h.saveIndex()
		for _, s := range changed {
			h.broadcastSession(s)
		}
	}
	return len(changed)
}
