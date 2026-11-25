package agenthost_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/acp-go-sdk"
	"github.com/google/uuid"

	"github.com/gerdr-dev/gerdr/acpx"
	"github.com/gerdr-dev/gerdr/agents/codex"
	"github.com/gerdr-dev/gerdr/internal/agenthost"
	"github.com/gerdr-dev/gerdr/internal/agentwire"
)

// TestE2EWorktree (codex): enter_worktree mid-turn, a write into the worktree in the same turn (the sandbox root is
// still the repository then), the next turn running there, and a resume in a new app-server landing there again
// (Gerdr's resume passes the home folder as cwd and the worktree in _meta).
func TestE2EWorktree(t *testing.T) {
	if os.Getenv("GERDR_E2E") != "codex" {
		t.Skip("set GERDR_E2E=codex")
	}
	repo, _ := filepath.EvalSymlinks(t.TempDir())
	for _, args := range [][]string{{"init", "-q"}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "x"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	wt := filepath.Join(repo, ".gerdr", "worktrees", "e2e", "w")
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	r := &rec{t: t}
	h := start(t, "codex", r)
	ns, err := h.Conn.NewSession(ctx, acp.NewSessionRequest{Cwd: repo, McpServers: []acp.McpServer{}, Meta: acpx.Meta(map[string]any{"mode": "auto"})})
	if err != nil {
		t.Fatal(err)
	}
	sid := ns.SessionId
	ask := func(h *agenthost.Host, text string) string {
		t.Helper()
		res, err := h.Conn.Prompt(ctx, acp.PromptRequest{SessionId: sid, MessageId: acp.Ptr(uuid.NewString()), Prompt: []acp.ContentBlock{acp.TextBlock(text)}})
		if err != nil {
			t.Fatal(err)
		}
		if res.StopReason != acp.StopReasonEndTurn {
			t.Fatalf("stop=%s", res.StopReason)
		}
		return r.reset()
	}
	ask(h, "Work in a worktree: call enter_worktree with name e2e/w. Then, in this same turn, create the file made.txt containing hi in that worktree with a shell command. Reply DONE.")
	if b, err := os.ReadFile(filepath.Join(wt, "made.txt")); err != nil || !strings.Contains(string(b), "hi") {
		t.Fatalf("made.txt in the worktree: %q %v", b, err)
	}
	r.mu.Lock()
	wds := append([]string(nil), r.workdirs...)
	r.mu.Unlock()
	if len(wds) == 0 || wds[len(wds)-1] != wt {
		t.Fatalf("workdir reports %v", wds)
	}
	if got := ask(h, "Run pwd with the shell (no workdir argument) and reply with its output only."); !strings.Contains(got, wt) {
		t.Fatalf("next turn not in the worktree: %q", got)
	}

	// A new app-server (Gerdr restarted): resumed the way the hub does it. The old one goes first: a thread has one
	// writer at a time.
	h.Close()
	r2 := &rec{t: t}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h2, err := agenthost.StartInProc(ctx, codex.New(log, agentwire.CodexExt(nil)), r2, log)
	if err != nil {
		t.Fatal(err)
	}
	defer h2.Close()
	if _, err := h2.Conn.ResumeSession(ctx, acp.ResumeSessionRequest{SessionId: sid, Cwd: repo, McpServers: []acp.McpServer{},
		Meta: acpx.Meta(map[string]any{"mode": "auto", acpx.WorkdirKey: wt})}); err != nil {
		t.Fatal(err)
	}
	r = r2
	if got := ask(h2, "Run pwd with the shell (no workdir argument) and reply with its output only."); !strings.Contains(got, wt) {
		t.Fatalf("resumed outside the worktree: %q", got)
	}
}
