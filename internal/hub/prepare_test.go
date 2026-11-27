package hub

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/coder/acp-go-sdk"
	"github.com/gerdr-dev/gerdr/acpx"
	"github.com/gerdr-dev/gerdr/agents/codex"
	"github.com/gerdr-dev/gerdr/internal/agenthost"
)

type preparingAgent struct {
	fakeAgent
	started chan acp.NewSessionRequest
	ready   chan struct{}
}

func (a *preparingAgent) NewSession(ctx context.Context, p acp.NewSessionRequest) (acp.NewSessionResponse, error) {
	a.started <- p
	select {
	case <-a.ready:
		mode, _ := acpx.GetMeta(p.Meta, "mode")
		modeID, _ := mode.(string)
		return acp.NewSessionResponse{SessionId: "prepared", Modes: codex.SessionModes(modeID)}, nil
	case <-ctx.Done():
		return acp.NewSessionResponse{}, ctx.Err()
	}
}

func TestCreateWhileCodexPrepares(t *testing.T) {
	h, err := New(Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	h.SetAgents([]AgentDef{{ID: "codex", Bin: bin}})
	a := &preparingAgent{
		fakeAgent: fakeAgent{silent: true, release: make(chan struct{}), finished: make(chan string, 1)},
		started:   make(chan acp.NewSessionRequest, 2),
		ready:     make(chan struct{}),
	}
	close(a.release)
	host, err := agenthost.StartInProc(t.Context(), a, &agentHandler{h: h, agent: "codex"}, h.log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	var ready sync.Once
	unblock := func() { ready.Do(func() { close(a.ready) }) }
	t.Cleanup(unblock)
	h.mu.Lock()
	h.hosts["codex"] = host
	h.mu.Unlock()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	s, err := h.Create(ctx, CreateOptions{Agent: "codex", Cwd: t.TempDir(), Mode: "read-only", PrepareInBackground: true})
	if err != nil {
		t.Fatal(err)
	}
	if state := s.Summary(); state.AgentSessionID != "" || state.ModeID != "read-only" {
		t.Fatalf("expected an empty registered session with its chosen mode, got agent id %q and mode %q", state.AgentSessionID, state.ModeID)
	}
	select {
	case p := <-a.started:
		if mode, _ := acpx.GetMeta(p.Meta, "mode"); mode != "read-only" {
			t.Fatalf("agent started in the wrong mode: %v", mode)
		}
	case <-ctx.Done():
		t.Fatal("Codex did not start in the background")
	}

	sent := make(chan error, 1)
	go func() {
		_, err := s.Send(ctx, []acp.ContentBlock{acp.TextBlock("first message")}, "")
		sent <- err
	}()
	unblock()
	select {
	case err := <-sent:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("first send did not finish")
	}
	if got := waitPrompt(t, &a.fakeAgent); got != "first message" {
		t.Fatalf("unexpected first prompt: %q", got)
	}
	if id := s.Record().AgentSessionID; id != "prepared" {
		t.Fatalf("agent session was not registered: %q", id)
	}
	select {
	case <-a.started:
		t.Fatal("the first send started a second thread")
	default:
	}
}
