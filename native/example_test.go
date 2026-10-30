package native_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"

	"github.com/coder/acp-go-sdk"

	"github.com/gerdr-dev/gerdr/acpx"
	"github.com/gerdr-dev/gerdr/native"
)

// client is the embedding program's side of ACP: it receives session updates, answers permission requests and
// gerdr's agent → client extension (_gerdr/ask, the ask_user tool). The file-system and terminal methods are
// never called by the native agent (it reads files and runs commands itself).
type client struct {
	mu   sync.Mutex
	text strings.Builder
}

func (c *client) SessionUpdate(ctx context.Context, n acp.SessionNotification) error {
	u := n.Update
	switch {
	case u.AgentMessageChunk != nil && u.AgentMessageChunk.Content.Text != nil:
		c.mu.Lock()
		c.text.WriteString(u.AgentMessageChunk.Content.Text.Text)
		c.mu.Unlock()
	case u.SessionInfoUpdate != nil:
		// gerdr's extras ride in _meta.gerdr: progress (output / context tokens), tasks, notice, autoTurn,
		// sessionId + cleared after /clear. See the package documentation.
	}
	return nil
}

func (c *client) RequestPermission(ctx context.Context, p acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{Selected: &acp.RequestPermissionOutcomeSelected{OptionId: "allow_once"}}}, nil
}

func (c *client) HandleExtensionMethod(ctx context.Context, method string, params json.RawMessage) (any, error) {
	if method == acpx.MethodAsk {
		return acpx.AskResponse{Cancelled: true, Reason: "no user here"}, nil
	}
	return nil, acp.NewMethodNotFound(method)
}

func (c *client) ReadTextFile(context.Context, acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	return acp.ReadTextFileResponse{}, acp.NewMethodNotFound("fs/read_text_file")
}
func (c *client) WriteTextFile(context.Context, acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	return acp.WriteTextFileResponse{}, acp.NewMethodNotFound("fs/write_text_file")
}
func (c *client) CreateTerminal(context.Context, acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	return acp.CreateTerminalResponse{}, acp.NewMethodNotFound("terminal/create")
}
func (c *client) KillTerminal(context.Context, acp.KillTerminalRequest) (acp.KillTerminalResponse, error) {
	return acp.KillTerminalResponse{}, acp.NewMethodNotFound("terminal/kill")
}
func (c *client) TerminalOutput(context.Context, acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	return acp.TerminalOutputResponse{}, acp.NewMethodNotFound("terminal/output")
}
func (c *client) ReleaseTerminal(context.Context, acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return acp.ReleaseTerminalResponse{}, acp.NewMethodNotFound("terminal/release")
}
func (c *client) WaitForTerminalExit(context.Context, acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	return acp.WaitForTerminalExitResponse{}, acp.NewMethodNotFound("terminal/wait_for_exit")
}
func (c *client) UnstableCompleteElicitation(context.Context, acp.UnstableCompleteElicitationNotification) error {
	return nil
}
func (c *client) UnstableCreateElicitation(context.Context, acp.UnstableCreateElicitationRequest) (acp.UnstableCreateElicitationResponse, error) {
	return acp.UnstableCreateElicitationResponse{}, acp.NewMethodNotFound("elicitation/create")
}
func (c *client) UnstableConnectMcp(context.Context, acp.UnstableConnectMcpRequest) (acp.UnstableConnectMcpResponse, error) {
	return acp.UnstableConnectMcpResponse{}, acp.NewMethodNotFound("mcp/connect")
}
func (c *client) UnstableDisconnectMcp(context.Context, acp.UnstableDisconnectMcpRequest) (acp.UnstableDisconnectMcpResponse, error) {
	return acp.UnstableDisconnectMcpResponse{}, acp.NewMethodNotFound("mcp/disconnect")
}

// Run the native agent in-process and talk ACP to it over two pipes.
func Example() {
	// A stand-in for a Chat Completions endpoint that always says the same thing.
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, `data: {"choices":[{"delta":{"content":"hello from the model"}}]}`+"\n\n")
		io.WriteString(w, `data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`+"\n\n")
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer model.Close()
	dir, _ := os.MkdirTemp("", "native-example")
	defer os.RemoveAll(dir)

	cfg := native.Config{Providers: []native.Provider{{
		ID: "gw", BaseURL: model.URL, APIKey: "sk-…",
		Models: []native.Model{{ID: "deepseek-v4-pro"}},
	}}}
	agent := native.New(slog.New(slog.NewTextHandler(io.Discard, nil)), native.Options{
		Dir:                dir,
		Config:             func() native.Config { return cfg },
		SystemPrompt:       func(sessionID, cwd string, meta map[string]any) string { return "You are a helpful assistant." },
		NoInstructionFiles: true,
		NoMemory:           true,
		BashEnv: func(sessionID string, meta map[string]any) []string {
			return []string{fmt.Sprintf("MY_CHAT_ID=%v", meta["chatId"])}
		},
	})
	defer agent.Close()

	// Two pipes: client → agent and agent → client.
	toAgentR, toAgentW := io.Pipe()
	toClientR, toClientW := io.Pipe()
	agentConn := acp.NewAgentSideConnection(agent, toClientW, toAgentR)
	agent.SetConn(agentConn)
	c := &client{}
	conn := acp.NewClientSideConnection(c, toAgentW, toClientR)

	ctx := context.Background()
	if _, err := conn.Initialize(ctx, acp.InitializeRequest{ProtocolVersion: acp.ProtocolVersionNumber}); err != nil {
		panic(err)
	}
	// _meta is handed to SystemPrompt, BashEnv and caller tools; _meta.gerdr.mode / .model pick the
	// permission mode and model ("provider/model").
	sess, err := conn.NewSession(ctx, acp.NewSessionRequest{
		Cwd: dir, McpServers: []acp.McpServer{},
		Meta: map[string]any{"chatId": "c42", "gerdr": map[string]any{"mode": "bypassPermissions"}},
	})
	if err != nil {
		panic(err)
	}
	// Thinking level per turn: the "effort" config option ("off" works on every model that can turn it off).
	_, _ = conn.SetSessionConfigOption(ctx, acp.SetSessionConfigOptionRequest{ValueId: &acp.SetSessionConfigOptionValueId{
		SessionId: sess.SessionId, ConfigId: "effort", Value: "off",
	}})
	res, err := conn.Prompt(ctx, acp.PromptRequest{
		SessionId: sess.SessionId,
		MessageId: acp.Ptr("m1"), // keep it: _gerdr/rewind goes back to a message by this id
		Prompt:    []acp.ContentBlock{acp.TextBlock("hi")},
	})
	if err != nil {
		panic(err)
	}
	c.mu.Lock()
	fmt.Println(res.StopReason, c.text.String())
	c.mu.Unlock()
	// Output: end_turn hello from the model
}
