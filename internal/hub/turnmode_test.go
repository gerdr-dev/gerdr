package hub

import (
	"encoding/json"
	"log/slog"
	"testing"

	acp "github.com/coder/acp-go-sdk"
)

// An external agent's turn keeps the mode it started under (agy only applies a change at its next prompt): the
// running turn reports it while the picker shows the new one. Gerdr's own adapters apply a change at once.
func TestTurnMode(t *testing.T) {
	h := &Hub{opt: Options{Dir: t.TempDir()}, sessions: map[string]*Session{}, subs: map[*Sub]struct{}{}, log: slog.Default()}
	h.defs = []AgentDef{{ID: "agy", Command: []string{"agy", "acp"}}}
	for _, c := range []struct {
		agent, want string
	}{{"agy", "default"}, {"claude", ""}} {
		s := &Session{h: h, loaded: true, rec: Record{ID: "s-" + c.agent, Agent: c.agent},
			modes: &acp.SessionModeState{CurrentModeId: "default", AvailableModes: []acp.SessionMode{{Id: "default"}, {Id: "auto"}}}}
		s.running = true
		s.turnMode = s.turnModeLocked()
		if m := s.turnInfoLocked().Mode; m != "" {
			t.Fatalf("%s: nothing changed yet, got turn mode %q", c.agent, m)
		}
		s.modes.CurrentModeId = "auto" // a person switched mid-turn
		if m := s.turnInfoLocked().Mode; m != c.want {
			t.Fatalf("%s: turn mode %q, want %q", c.agent, m, c.want)
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
