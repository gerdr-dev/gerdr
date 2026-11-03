package hub

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gerdr-dev/gerdr/acpx"
)

// The adapter's workdir report (acpx.WorkdirKey) moves the session's WorkDir and is saved; reporting the cwd itself
// means back home. Cwd never changes: it is what the session is grouped by and resumed in.
func TestWorkdirReport(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	h := &Hub{opt: Options{Dir: dir}, sessions: map[string]*Session{}, subs: map[*Sub]struct{}{}, log: slog.Default()}
	s := &Session{h: h, loaded: true, rec: Record{ID: "s1", Agent: "claude", Cwd: "/w"}}
	h.sessions["s1"] = s
	report := func(d string) { s.onGerdrInfo(map[string]any{acpx.WorkdirKey: d}) }

	report("/w") // a process starting at home: nothing to remember
	if s.rec.Workdir != "" || s.rec.WorkDir() != "/w" {
		t.Fatalf("home: %+v", s.rec)
	}
	report("/w/.claude/worktrees/x")
	if s.rec.Cwd != "/w" || s.rec.WorkDir() != "/w/.claude/worktrees/x" {
		t.Fatalf("entered: %+v", s.rec)
	}
	if b, err := os.ReadFile(h.indexPath()); err != nil || !strings.Contains(string(b), `"workdir": "/w/.claude/worktrees/x"`) {
		t.Fatalf("not saved: %s %v", b, err)
	}
	report("/w")
	if s.rec.Workdir != "" {
		t.Fatalf("exited: %+v", s.rec)
	}
}
