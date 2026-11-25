package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/coder/acp-go-sdk"

	"github.com/gerdr-dev/gerdr/acpx"
	"github.com/gerdr-dev/gerdr/worktree"
)

// Codex has no tool of its own that moves a thread to another folder (Claude Code has EnterWorktree), so the adapter
// gives it two as app-server dynamic tools (experimental API, thread/start only; a resumed thread keeps them, measured
// 0.166) and does the move itself:
//
//   - the worktree is made inside the repository (package worktree): the sandbox of the running turn only writes under
//     the folder the turn started in, so a worktree beside the repository would refuse every write until the next turn;
//   - the rest of this turn: commands run where the model says (their workdir); the next turn/start carries cwd, which
//     moves the thread's commands, its sandbox root and the cwd saved with the thread (a resume without cwd lands there);
//   - Gerdr is told (acpx.WorkdirKey) and passes it back on resume, since its own resume carries the session's home
//     folder, which would move the thread back.
//
// Threads started before these tools existed don't have them: thread/resume can't add tools.

const (
	toolEnterWorktree = "enter_worktree"
	toolExitWorktree  = "exit_worktree"
)

func worktreeTools() []map[string]any {
	return []map[string]any{
		{
			"type": "function",
			"name": toolEnterWorktree,
			"description": "Create an isolated git worktree of this repository on a new branch and move this session into it. " +
				"Use it ONLY when the user, the project's instructions (AGENTS.md) or your instructions explicitly say to work in a worktree; " +
				"to just make or switch a branch, use git. The branch starts from origin's default branch (fetched), else HEAD. " +
				"With the name of a worktree made before, enters that one again.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{"type": "string", "description": "The worktree's folder and its branch name, saying what the work is, e.g. fix/order-timeout. Letters, digits, . _ - and / between parts, at most 64 characters."},
				},
				"required": []string{"name"},
			},
		},
		{
			"type": "function",
			"name": toolExitWorktree,
			"description": "Leave the worktree entered with enter_worktree and go back to the repository's own folder. Only when the user asks. " +
				"action keep leaves the worktree and its branch on disk; remove deletes both, and refuses while there are uncommitted changes or unpushed commits unless discard_changes is true (confirm that with the user first).",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action":          map[string]any{"type": "string", "enum": []string{"keep", "remove"}},
					"discard_changes": map[string]any{"type": "boolean"},
				},
				"required": []string{"action"},
			},
		},
	}
}

type toolCallParams struct {
	ThreadID  string          `json:"threadId"`
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments"`
}

func toolText(ok bool, text string) map[string]any {
	return map[string]any{"success": ok, "contentItems": []map[string]any{{"type": "inputText", "text": text}}}
}

// dynamicTool answers item/tool/call.
func (a *Agent) dynamicTool(ctx context.Context, params json.RawMessage) (any, error) {
	var p toolCallParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	a.mu.Lock()
	s := a.sessions[p.ThreadID]
	a.mu.Unlock()
	if s == nil || s.sub != nil {
		return toolText(false, "This tool only works in the main conversation."), nil
	}
	var args struct {
		Name    string `json:"name"`
		Action  string `json:"action"`
		Discard bool   `json:"discard_changes"`
	}
	_ = json.Unmarshal(p.Arguments, &args)
	switch p.Tool {
	case toolEnterWorktree:
		return a.enterWorktree(ctx, s, args.Name), nil
	case toolExitWorktree:
		return a.exitWorktree(ctx, s, args.Action, args.Discard), nil
	}
	return toolText(false, "Unknown tool "+p.Tool), nil
}

func (a *Agent) enterWorktree(ctx context.Context, s *session, name string) any {
	s.mu.Lock()
	cwd := s.cwd
	s.mu.Unlock()
	e, err := worktree.Enter(ctx, cwd, name)
	if err != nil {
		return toolText(false, err.Error())
	}
	a.moveTo(ctx, s, e.Dir)
	how := fmt.Sprintf("Made worktree %s on new branch %s (from %s).", e.Root, e.Branch, e.Base)
	if e.Reused {
		how = fmt.Sprintf("Entered the existing worktree %s (branch %s).", e.Root, e.Branch)
	}
	return toolText(true, how+fmt.Sprintf(" Work there from now on: for the rest of this turn give every command workdir %s and use paths in it; "+
		"from the next turn it is the session's folder. Files git doesn't track (.env, dependencies such as node_modules) are not in it: "+
		"copy or install what you need from %s.", e.Dir, worktree.Home(e.Dir)))
}

func (a *Agent) exitWorktree(ctx context.Context, s *session, action string, discard bool) any {
	s.mu.Lock()
	cwd := s.cwd
	s.mu.Unlock()
	root, home := worktree.RootOf(cwd), worktree.Home(cwd)
	if root == "" {
		return toolText(false, "Not in a worktree made by enter_worktree: nothing to leave.")
	}
	if action == "remove" {
		if err := worktree.Remove(ctx, root, discard); err != nil {
			if errors.Is(err, worktree.ErrUnsaved) {
				return toolText(false, err.Error()+"\nNothing was removed. Ask the user before calling again with discard_changes true, or use action keep.")
			}
			return toolText(false, err.Error())
		}
	}
	a.moveTo(ctx, s, home)
	if action == "remove" {
		return toolText(true, "Removed "+root+" and its branch. Back in "+home+": use it as the workdir for the rest of this turn.")
	}
	return toolText(true, "Back in "+home+" (the worktree "+root+" stays). Use it as the workdir for the rest of this turn.")
}

// moveTo makes dir the thread's folder from its next turn (turn/start carries cwd) and tells Gerdr.
func (a *Agent) moveTo(ctx context.Context, s *session, dir string) {
	s.mu.Lock()
	s.cwd = dir
	s.mu.Unlock()
	a.noteWorkdir(ctx, s)
}

// noteWorkdir tells Gerdr where the thread works now: after a move, and after a resume (Gerdr's record may be older).
func (a *Agent) noteWorkdir(ctx context.Context, s *session) {
	s.mu.Lock()
	dir := s.cwd
	s.mu.Unlock()
	if dir == "" {
		return
	}
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		dir = r
	}
	a.send(ctx, s.id, acp.SessionUpdate{SessionInfoUpdate: &acp.SessionSessionInfoUpdate{Meta: acpx.Meta(map[string]any{acpx.WorkdirKey: dir})}})
}

// resumeCwd: where to resume a thread. Gerdr passes the session's home folder as cwd and where it last worked in
// _meta (acpx.WorkdirKey); the worktree wins while it is still there.
func resumeCwd(cwd string, meta map[string]any) string {
	if v, ok := acpx.GetMeta(meta, acpx.WorkdirKey); ok {
		if d, _ := v.(string); d != "" {
			if st, err := os.Stat(d); err == nil && st.IsDir() {
				return d
			}
		}
	}
	return cwd
}
