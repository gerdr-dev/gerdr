package hub

import (
	"encoding/json"
	"log/slog"
	"testing"

	acp "github.com/coder/acp-go-sdk"
)

// A turn that keeps the mode it started under (agy, codex unless full-access) reports it while the picker shows the
// new one; claude and native apply a change at once.
func TestTurnMode(t *testing.T) {
	h := &Hub{opt: Options{Dir: t.TempDir()}, sessions: map[string]*Session{}, subs: map[*Sub]struct{}{}, log: slog.Default()}
	h.defs = []AgentDef{{ID: "agy", Command: []string{"agy", "acp"}}}
	for _, c := range []struct {
		agent, to, want string
	}{
		{"agy", "auto", "default"},
		{"claude", "auto", ""},
		{"native", "auto", ""},
		{"codex", "auto", "default"},
		{"codex", "full-access", ""}, // the adapter accepts every ask from then on
	} {
		s := &Session{h: h, loaded: true, rec: Record{ID: "s-" + c.agent, Agent: c.agent},
			modes: &acp.SessionModeState{CurrentModeId: "default", AvailableModes: []acp.SessionMode{{Id: "default"}, {Id: acp.SessionModeId(c.to)}}}}
		s.running = true
		s.turnMode = s.turnModeLocked()
		if m := s.turnInfoLocked().Mode; m != "" {
			t.Fatalf("%s: nothing changed yet, got turn mode %q", c.agent, m)
		}
		s.modes.CurrentModeId = acp.SessionModeId(c.to) // a person switched mid-turn
		if m := s.turnInfoLocked().Mode; m != c.want {
			t.Fatalf("%s → %s: turn mode %q, want %q", c.agent, c.to, m, c.want)
		}
		s.modes.CurrentModeId = "default" // and back: nothing to show
		if m := s.turnInfoLocked().Mode; m != "" {
			t.Fatalf("%s: switched back, got %q", c.agent, m)
		}
	}
}

// Switching to a mode that never asks answers the permissions on screen with "allow once"; a plan's "ready to code?"
// stays for the person.
func TestAllowOpenAsks(t *testing.T) {
	h := &Hub{opt: Options{Dir: t.TempDir()}, sessions: map[string]*Session{}, subs: map[*Sub]struct{}{}, log: slog.Default()}
	s := newSession(h, Record{ID: "s", Agent: "codex"})
	ask := func(kind acp.ToolKind) *pending {
		req, _ := json.Marshal(acp.RequestPermissionRequest{ToolCall: acp.ToolCallUpdate{Kind: acp.Ptr(kind)}, Options: []acp.PermissionOption{
			{OptionId: "allow_once", Kind: acp.PermissionOptionKindAllowOnce},
			{OptionId: "reject_once", Kind: acp.PermissionOptionKindRejectOnce},
		}})
		p := &pending{kind: "permission", ch: make(chan json.RawMessage, 1), req: req}
		s.pending[string(kind)] = p
		return p
	}
	cmd, plan := ask(acp.ToolKindExecute), ask(acp.ToolKindSwitchMode)
	if modeAllowsAll("codex", "auto") || !modeAllowsAll("codex", "full-access") || !modeAllowsAll("claude", "bypassPermissions") {
		t.Fatal("modeAllowsAll")
	}
	s.allowOpenAsks()
	select {
	case r := <-cmd.ch:
		if string(r) != `{"optionId":"allow_once"}` {
			t.Fatalf("answer %s", r)
		}
	default:
		t.Fatal("the command's ask stayed open")
	}
	if len(plan.ch) != 0 || s.pending[string(acp.ToolKindSwitchMode)] == nil {
		t.Fatal("the plan's ask was answered")
	}
}
