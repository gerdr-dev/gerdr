package native

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/coder/acp-go-sdk"

	"github.com/gerdr-dev/gerdr/acpx"
	"github.com/gerdr-dev/gerdr/worktree"
)

// enter_worktree / exit_worktree: the session moves itself into a git worktree of its repository and back, like
// Claude Code's EnterWorktree / ExitWorktree (package worktree has the git side). The session's folder stays its home
// (meta.Cwd: memory, how Gerdr groups it); where it works now is meta.Workdir, saved with the session, so a resume
// lands there again. Every tool reads the folder per call, so the move takes effect on the next call; the system
// prompt follows from the next turn. Gerdr is told through acpx.WorkdirKey.

var enterWorktreeTool = &tool{
	name: "enter_worktree",
	desc: "Create an isolated git worktree of this repository on a new branch and move this session into it. " +
		"Use it ONLY when the user, the project's instructions (AGENTS.md / CLAUDE.md) or your memory explicitly say to work in a worktree; " +
		"to just make or switch a branch, use git. The branch starts from origin's default branch (fetched), else HEAD. " +
		"With the name of a worktree made before, enters that one again.",
	params: obj(map[string]any{
		"name": prop("string", "The worktree's folder and its branch name, saying what the work is, e.g. fix/order-timeout. Letters, digits, . _ - and / between parts, at most 64 characters."),
	}, "name"),
	show: func(args map[string]any, cwd string) (string, acp.ToolKind, []acp.ToolCallLocation) {
		return "Enter worktree " + str(args, "name"), acp.ToolKindOther, nil
	},
	run: func(c *call, args map[string]any) result {
		e, err := worktree.Enter(c.ctx, c.cwd(), str(args, "name"))
		if err != nil {
			return errResult("%s", err.Error())
		}
		c.r.s.moveTo(e.Dir)
		how := fmt.Sprintf("Made worktree %s on new branch %s (from %s).", e.Root, e.Branch, e.Base)
		if e.Reused {
			how = fmt.Sprintf("Entered the existing worktree %s (branch %s).", e.Root, e.Branch)
		}
		return result{text: how + fmt.Sprintf(" You now work in %s: relative paths and commands are there. "+
			"Files git doesn't track (.env, dependencies such as node_modules) are not in it: copy or install what you need from %s.",
			e.Dir, worktree.Home(e.Dir))}
	},
}

var exitWorktreeTool = &tool{
	name: "exit_worktree",
	desc: "Leave the worktree entered with enter_worktree and go back to the repository's own folder. Only when the user asks. " +
		"action keep leaves the worktree and its branch on disk; remove deletes both, and refuses while there are uncommitted " +
		"changes or unpushed commits unless discard_changes is true (confirm that with the user first).",
	params: obj(map[string]any{
		"action":          map[string]any{"type": "string", "enum": []string{"keep", "remove"}},
		"discard_changes": prop("boolean", "With remove: delete even uncommitted changes and unpushed commits"),
	}, "action"),
	show: func(args map[string]any, cwd string) (string, acp.ToolKind, []acp.ToolCallLocation) {
		if str(args, "action") == "remove" {
			return "Leave and remove worktree", acp.ToolKindDelete, nil
		}
		return "Leave worktree", acp.ToolKindOther, nil
	},
	run: func(c *call, args map[string]any) result {
		s := c.r.s
		s.mu.Lock()
		wd := s.meta.Workdir
		s.mu.Unlock()
		root := worktree.RootOf(wd)
		if wd == "" || root == "" {
			return errResult("Not in a worktree made by enter_worktree: nothing to leave.")
		}
		home := worktree.Home(wd)
		if str(args, "action") == "remove" {
			if err := worktree.Remove(c.ctx, root, boolean(args, "discard_changes")); err != nil {
				if errors.Is(err, worktree.ErrUnsaved) {
					return errResult("%s\nNothing was removed. Ask the user before calling again with discard_changes true, or use action keep.", err.Error())
				}
				return errResult("%s", err.Error())
			}
			s.moveTo("")
			return result{text: "Removed " + root + " and its branch. Back in " + home + "."}
		}
		s.moveTo("")
		return result{text: "Back in " + home + " (the worktree " + root + " stays)."}
	},
}

// moveTo: dir is where the session works from now on ("" = its home folder), saved and told to Gerdr.
func (s *session) moveTo(dir string) {
	s.mu.Lock()
	if dir == s.meta.Cwd {
		dir = ""
	}
	s.meta.Workdir = dir
	s.mu.Unlock()
	s.saveMeta()
	s.noteWorkdir()
}

// noteWorkdir tells Gerdr where the session works (acpx.WorkdirKey; its home folder means it is back).
func (s *session) noteWorkdir() {
	d := s.cwd()
	if r, err := filepath.EvalSymlinks(d); err == nil {
		d = r
	}
	s.send(acp.SessionUpdate{SessionInfoUpdate: &acp.SessionSessionInfoUpdate{Meta: acpx.Meta(map[string]any{acpx.WorkdirKey: d})}})
}

// workdirGone: the saved worktree was removed outside the session (by hand, or Gerdr cleaning up): back home.
func workdirGone(d string) bool {
	if d == "" {
		return false
	}
	st, err := os.Stat(d)
	return err != nil || !st.IsDir()
}
