package claude

import (
	"testing"
	"time"

	"github.com/gerdr-dev/gerdr/acpx"
)

// A session idle past the timeout is reaped — unless a background task or an auto turn is still going: reaping kills
// the task with the process (seen: a 5-minute test run backgrounded by an auto turn, killed by the reaper because the
// last *user* turn was 15 minutes earlier).
func TestReapable(t *testing.T) {
	old := time.Now().Add(-time.Hour)
	mk := func() *session { return &session{p: &proc{}, lastUsed: old} }

	if s := mk(); !s.reapableLocked(15 * time.Minute) {
		t.Fatal("idle session not reapable")
	}
	if s := mk(); func() bool { s.lastUsed = time.Now(); return s.reapableLocked(15 * time.Minute) }() {
		t.Fatal("recently used session reapable")
	}
	s := mk()
	s.tasks = map[string]*acpx.Task{"b1": {ID: "b1", Status: "running", Background: true}}
	if s.reapableLocked(15 * time.Minute) {
		t.Fatal("session with a running background task reapable")
	}
	s.tasks["b1"].Status = "completed"
	if !s.reapableLocked(15 * time.Minute) {
		t.Fatal("session whose tasks all finished not reapable")
	}
	s = mk()
	s.auto = true
	if s.reapableLocked(15 * time.Minute) {
		t.Fatal("session in an auto turn reapable")
	}
}
