package hub

import (
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
