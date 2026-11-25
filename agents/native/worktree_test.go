package native

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coder/acp-go-sdk"
	"github.com/google/uuid"

	"github.com/gerdr-dev/gerdr/acpx"
	"github.com/gerdr-dev/gerdr/internal/agenthost"
)

func (h *handler) workdirs() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for _, n := range h.updates {
		if u := n.Update.SessionInfoUpdate; u != nil {
			if d, ok := acpx.GetMeta(u.Meta, acpx.WorkdirKey); ok {
				out = append(out, d.(string))
			}
		}
	}
	return out
}

// The session moves into a worktree mid-turn (the next tool call already works there), stays there across a resume,
// and leaves it with exit_worktree.
func TestWorktreeTools(t *testing.T) {
	step := 0
	e := setup(t, func(n int, msgs []any) reply {
		if lastRole(msgs) == "user" {
			step++
		}
		switch {
		case lastRole(msgs) == "user" && step == 1:
			return reply{calls: [][2]string{{"enter_worktree", `{"name":"fix/x"}`}}}
		case step == 1 && n == 1:
			return reply{calls: [][2]string{{"write", `{"path":"made.txt","content":"x"}`}}}
		case lastRole(msgs) == "user" && step == 3:
			return reply{calls: [][2]string{{"exit_worktree", `{"action":"remove","discard_changes":true}`}}}
		}
		return reply{text: "ok"}
	})
	repo, _ := filepath.EvalSymlinks(e.cwd)
	for _, args := range [][]string{{"init", "-q"}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "x"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	sid := e.newSession(modeBypass)
	if _, err := e.prompt(sid, uuid.NewString(), "work in a worktree"); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(repo, ".gerdr", "worktrees", "fix", "x")
	if _, err := os.Stat(filepath.Join(wt, "made.txt")); err != nil {
		t.Fatalf("the next call didn't work in the worktree: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, "made.txt")); err == nil {
		t.Fatal("written in the repository instead")
	}
	if w := e.h.workdirs(); len(w) != 1 || w[0] != wt {
		t.Fatalf("workdir updates %v", w)
	}

	// Gerdr restarts: the session resumes in the worktree, and says so
	ag2 := New(slog.New(slog.NewTextHandler(io.Discard, nil)), Options{Dir: e.ag.st.dir, Config: e.ag.cfg})
	h2 := &handler{}
	c2, err := agenthost.StartInProc(context.Background(), ag2, h2, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	if _, err := c2.Conn.ResumeSession(context.Background(), acp.ResumeSessionRequest{SessionId: sid, Cwd: e.cwd}); err != nil {
		t.Fatal(err)
	}
	if s, _ := ag2.session(sid); s == nil || s.cwd() != wt || s.home() != e.cwd {
		t.Fatalf("resumed in %v", s)
	}
	if w := h2.workdirs(); len(w) != 1 || w[0] != wt {
		t.Fatalf("resume didn't report the worktree: %v", w)
	}
	if _, err := c2.Conn.Prompt(context.Background(), acp.PromptRequest{SessionId: sid, MessageId: acp.Ptr(uuid.NewString()), Prompt: []acp.ContentBlock{acp.TextBlock("hi")}}); err != nil {
		t.Fatal(err)
	}
	if sys := e.llm.request(-1)["messages"].([]any)[0].(map[string]any)["content"].(string); !strings.Contains(sys, wt) {
		t.Fatal("the system prompt doesn't name the worktree")
	}

	step = 2
	if _, err := c2.Conn.Prompt(context.Background(), acp.PromptRequest{SessionId: sid, MessageId: acp.Ptr(uuid.NewString()), Prompt: []acp.ContentBlock{acp.TextBlock("leave it")}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatalf("worktree still there: %v", err)
	}
	if s, _ := ag2.session(sid); s.cwd() != e.cwd {
		t.Fatalf("not back home: %s", s.cwd())
	}
	if w := h2.workdirs(); w[len(w)-1] != repo {
		t.Fatalf("leaving didn't report home: %v", w)
	}
}
