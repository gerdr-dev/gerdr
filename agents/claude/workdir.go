package claude

import (
	"context"
	"encoding/json"
	"path/filepath"

	"github.com/coder/acp-go-sdk"
	"github.com/gerdr-dev/gerdr/acpx"
)

// workdirFromTool: claude moves itself with EnterWorktree / ExitWorktree, and stream-json has no other sign of it
// until the next process's init. Their structured results (measured, 2.1.317):
//
//	EnterWorktree {"worktreePath", "worktreeBranch", "message"}
//	ExitWorktree  {"action", "originalCwd", "worktreePath", "worktreeBranch", "restoredCwd", "originalCwdMissing", "message"}
func (s *session) workdirFromTool(ctx context.Context, name string, structured json.RawMessage) {
	if name != "EnterWorktree" && name != "ExitWorktree" || len(structured) == 0 {
		return
	}
	var r struct {
		WorktreePath string `json:"worktreePath"`
		RestoredCwd  string `json:"restoredCwd"`
	}
	if json.Unmarshal(structured, &r) != nil {
		return
	}
	if name == "EnterWorktree" {
		s.noteWorkdir(ctx, r.WorktreePath)
	} else {
		s.noteWorkdir(ctx, r.RestoredCwd)
	}
}

// noteWorkdir tells the hub where claude works now (acpx.WorkdirKey) when that changed. Not while replaying a
// transcript (withEmit): an old EnterWorktree in the history says nothing about where it is now.
func (s *session) noteWorkdir(ctx context.Context, dir string) {
	if dir == "" || s.replaying {
		return
	}
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real // init reports /private/tmp/… for a session started in /tmp/…; the hub's cwd is resolved too
	}
	s.mu.Lock()
	same := dir == s.workdir
	s.workdir = dir
	s.mu.Unlock()
	if !same {
		s.update(ctx, acp.SessionUpdate{SessionInfoUpdate: &acp.SessionSessionInfoUpdate{Meta: acpx.Meta(map[string]any{acpx.WorkdirKey: dir})}})
	}
}
