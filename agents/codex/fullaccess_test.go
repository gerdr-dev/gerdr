package codex

import "testing"

// A switch to full-access mid-turn: what codex still asks under the turn's old policy is accepted, a sub-agent's asks
// follow its parent's mode.
func TestFullAccess(t *testing.T) {
	main := &session{mode: modeAuto}
	a := &Agent{sessions: map[string]*session{"t": main, "sub": {sub: &subInfo{parent: main}}}}
	if a.fullAccess("t") || a.fullAccess("sub") || a.fullAccess("unknown") {
		t.Fatal("auto asks")
	}
	main.mode = modeFull
	if !a.fullAccess("t") || !a.fullAccess("sub") {
		t.Fatal("full-access accepts")
	}
}
