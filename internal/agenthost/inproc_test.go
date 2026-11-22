package agenthost

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/coder/acp-go-sdk"

	"github.com/gerdr-dev/gerdr/acpx"
)

// stubAgent answers initialize; nothing else should reach it in these tests.
type stubAgent struct {
	acp.Agent
	closed chan struct{}
}

func (a *stubAgent) Initialize(context.Context, acp.InitializeRequest) (acp.InitializeResponse, error) {
	return acp.InitializeResponse{ProtocolVersion: acp.ProtocolVersionNumber}, nil
}
func (a *stubAgent) SetConn(*acp.AgentSideConnection) {}
func (a *stubAgent) Close()                           {}

type nopHandler struct{}

func (nopHandler) OnUpdate(acp.SessionNotification) {}
func (nopHandler) OnPermission(context.Context, acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	return acp.RequestPermissionResponse{}, nil
}
func (nopHandler) OnAsk(context.Context, acpx.AskRequest) (acpx.AskResponse, error) {
	return acpx.AskResponse{}, nil
}

// A prompt over the SDK's 10MB line limit (2026-11-22: a 13MB phone photo) used to leave the hub writing into a pipe
// nobody read: the turn spun and Stop hung behind it. Now the prompt fails at once and says why, later writes fail too,
// and the connection reads as done so the hub starts a fresh agent.
func TestInProcTooLongLineFails(t *testing.T) {
	h, err := StartInProc(context.Background(), &stubAgent{}, nopHandler{}, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()

	done := make(chan error, 1)
	go func() {
		_, err := h.Conn.Prompt(context.Background(), acp.PromptRequest{SessionId: "s",
			Prompt: []acp.ContentBlock{acp.TextBlock(strings.Repeat("x", 11<<20))}})
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "10MB") {
			t.Fatalf("prompt error = %v, want the too-large cause", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the prompt hung")
	}

	cancelled := make(chan error, 1)
	go func() { cancelled <- h.Conn.Cancel(context.Background(), acp.CancelNotification{SessionId: "s"}) }()
	select {
	case <-cancelled: // an error is fine: what matters is that Stop doesn't hang
	case <-time.After(5 * time.Second):
		t.Fatal("cancel hung")
	}
	select {
	case <-h.Conn.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the connection doesn't read as done, the hub would keep the dead agent")
	}
}
