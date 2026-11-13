package hub

import (
	"context"
	"errors"

	"github.com/gerdr-dev/gerdr/internal/i18n"
)

// SetCwd points a session at another folder (`gerdr session cwd`): the usual case is a project folder that was moved
// or renamed, which leaves every conversation in it pointing at a path that no longer exists. Only Gerdr's record and
// the agent side change; the conversation itself carries on. The agent session is detached (like Shutdown, without
// telling anyone it ended) so the next turn re-attaches in the new folder: Claude's adapter moves the transcript to
// the new folder's project dir on spawn (agents/claude adoptTranscript), Codex is unloaded by CloseSession and its
// thread/resume takes the new cwd. Refused while a turn runs: the agent is working in the old folder.
func (s *Session) SetCwd(ctx context.Context, dir string) error {
	n, err := normCwd(dir)
	if err != nil {
		return err
	}
	s.mu.Lock()
	running, same := s.running, s.rec.Cwd == n
	s.mu.Unlock()
	if running {
		return errors.New(i18n.S("这个会话还在跑：先停下这一轮，再换目录", "This session is still running: stop the current turn before changing its folder"))
	}
	if same {
		return nil
	}
	s.attachMu.Lock()
	s.mu.Lock()
	s.shut = true // as Shutdown: a process exit from here on is ours
	s.mu.Unlock()
	s.close(ctx)
	s.mu.Lock()
	s.attached = false
	s.rec.Cwd = n
	s.rec.Workdir = "" // a worktree it had entered belongs to the old folder
	s.mu.Unlock()
	s.attachMu.Unlock()
	s.setStatus()
	s.h.saveIndex()
	s.h.broadcastSession(s)
	return nil
}

// NormDir: a folder as sessions store it (absolute, ~ expanded, symlinks resolved), or an error if it isn't one.
func NormDir(p string) (string, error) { return normCwd(p) }
