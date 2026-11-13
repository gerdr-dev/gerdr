package hub

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// SetCwd moves the session's home (resolved, saved, worktree dropped), refuses a missing folder and a running turn.
func TestSetCwd(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	h := &Hub{opt: Options{Dir: dir}, sessions: map[string]*Session{}, subs: map[*Sub]struct{}{}, log: slog.Default()}
	s := &Session{h: h, loaded: true, rec: Record{ID: "s1", Agent: "claude", Cwd: "/old", Workdir: "/old/.claude/worktrees/x"}}
	h.sessions["s1"] = s
	ctx := context.Background()

	if err := s.SetCwd(ctx, filepath.Join(dir, "missing")); err == nil {
		t.Fatal("a missing folder was accepted")
	}
	to := filepath.Join(dir, "new")
	if err := os.Mkdir(to, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCwd(ctx, to); err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.EvalSymlinks(to)
	if s.rec.Cwd != want || s.rec.Workdir != "" {
		t.Fatalf("not moved: %+v", s.rec)
	}
	if b, err := os.ReadFile(h.indexPath()); err != nil || !strings.Contains(string(b), want) {
		t.Fatalf("not saved: %s %v", b, err)
	}
	s.running = true
	if err := s.SetCwd(ctx, dir); err == nil || s.rec.Cwd != want {
		t.Fatalf("moved while running: %v %+v", err, s.rec)
	}
}
