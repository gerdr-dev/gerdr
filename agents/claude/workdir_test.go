package claude

import (
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/coder/acp-go-sdk"
	"github.com/gerdr-dev/gerdr/acpx"
)

// Claude moving into a worktree and back (lines shaped like claude 2.1.317's stream-json): every move reaches the hub
// as acpx.WorkdirKey, a repeat doesn't, and a replayed transcript never does.
func TestWorkdirFollowsWorktree(t *testing.T) {
	a := &Agent{opts: Options{Env: []string{"CLAUDE_CONFIG_DIR=" + t.TempDir()}}, sessions: map[string]*session{}, log: slog.New(slog.DiscardHandler)}
	s := a.newSession("11111111-2222-4333-8444-555555555555", "/nonexistent/repo", nil)
	var got []string
	s.emitHook = func(u acp.SessionUpdate) {
		if u.SessionInfoUpdate != nil {
			if d, ok := u.SessionInfoUpdate.Meta[acpx.MetaKey].(map[string]any)[acpx.WorkdirKey].(string); ok {
				got = append(got, d)
			}
		}
	}
	line := func(js string) {
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(js), &m); err != nil {
			t.Fatal(err)
		}
		s.onLine(nil, m, []byte(js))
	}
	const wt = "/nonexistent/repo/.claude/worktrees/probe"
	line(`{"type":"system","subtype":"init","cwd":"/nonexistent/repo","session_id":"11111111-2222-4333-8444-555555555555"}`)
	line(`{"type":"assistant","message":{"id":"m1","content":[{"type":"tool_use","id":"t1","name":"EnterWorktree","input":{"name":"probe"}}]}}`)
	line(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"Created worktree"}]},"tool_use_result":{"worktreePath":"` + wt + `","worktreeBranch":"worktree-probe"}}`)
	line(`{"type":"system","subtype":"init","cwd":"` + wt + `","session_id":"11111111-2222-4333-8444-555555555555"}`) // a respawn: same place, no report
	line(`{"type":"assistant","message":{"id":"m2","content":[{"type":"tool_use","id":"t2","name":"ExitWorktree","input":{"action":"keep"}}]}}`)
	line(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t2","content":"Exited"}]},"tool_use_result":{"action":"keep","originalCwd":"/nonexistent/repo","worktreePath":"` + wt + `","restoredCwd":"/nonexistent/repo"}}`)
	want := []string{"/nonexistent/repo", wt, "/nonexistent/repo"}
	if len(got) != len(want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %q, want %q", got, want)
		}
	}

	got = nil
	s.withEmit(func(acp.SessionUpdate) {}, func() {
		s.noteWorkdir(nil, "/nonexistent/elsewhere")
	})
	if len(got) != 0 || s.workdir != "/nonexistent/repo" {
		t.Fatalf("a replay moved the workdir: %q, %q", got, s.workdir)
	}
}
