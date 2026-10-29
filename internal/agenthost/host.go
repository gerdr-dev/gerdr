// Package agenthost 是 ACP 的 client 那一头：把一个 ACP agent 接上，替 hub 收发。
//
// 内置的两个适配器（claude / codex）走**进程内管道**：适配器照样是一个完整的 ACP agent
// （同一份代码也能 `gerdr acp claude` 跑成 stdio agent 给别的编辑器用），只是两头之间
// 不经过子进程，省掉一层进程和序列化之外的开销。外部 agent（gemini、opencode…）走 stdio。
package agenthost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strings"

	"github.com/coder/acp-go-sdk"

	"github.com/gerdr-dev/gerdr/internal/acpx"
	"github.com/gerdr-dev/gerdr/internal/sysenv"
)

// Handler 是 hub 那一侧：收 agent 推来的东西。
type Handler interface {
	OnUpdate(n acp.SessionNotification)
	OnPermission(ctx context.Context, req acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error)
	OnAsk(ctx context.Context, req acpx.AskRequest) (acpx.AskResponse, error)
}

// InProcAgent 是内置适配器要实现的东西。
type InProcAgent interface {
	acp.Agent
	SetConn(*acp.AgentSideConnection)
	Close()
}

type Host struct {
	Conn *acp.ClientSideConnection
	Info acp.InitializeResponse
	stop func()
}

func (h *Host) Close() {
	if h.stop != nil {
		h.stop()
	}
}

// Steering 报告对方认不认 `_session/steering`。
func (h *Host) Steering() bool {
	s, ok := h.Info.Meta["steering"].(map[string]any)
	return ok && s["supported"] == true
}

func StartInProc(ctx context.Context, agent InProcAgent, handler Handler, log *slog.Logger) (*Host, error) {
	c2aR, c2aW := io.Pipe()
	a2cR, a2cW := io.Pipe()
	aconn := acp.NewAgentSideConnection(agent, a2cW, c2aR)
	aconn.SetLogger(log)
	agent.SetConn(aconn)
	cconn := acp.NewClientSideConnection(&client{h: handler}, c2aW, a2cR)
	cconn.SetLogger(log)
	h := &Host{Conn: cconn, stop: func() {
		agent.Close()
		_ = c2aW.Close()
		_ = a2cW.Close()
	}}
	if err := h.initialize(ctx); err != nil {
		h.Close()
		return nil, err
	}
	return h, nil
}

// StartProcess 起一个外部 ACP agent（stdio）。
func StartProcess(ctx context.Context, argv []string, cwd string, handler Handler, log *slog.Logger) (*Host, error) {
	if len(argv) == 0 {
		return nil, errors.New("empty command")
	}
	sysenv.AdoptLoginPath()
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = cwd
	cmd.Env = sysenv.ChildEnv()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = &stderrLog{log: log}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", strings.Join(argv, " "), err)
	}
	cconn := acp.NewClientSideConnection(&client{h: handler}, stdin, stdout)
	cconn.SetLogger(log)
	h := &Host{Conn: cconn, stop: func() {
		_ = stdin.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}}
	go func() { _ = cmd.Wait() }()
	if err := h.initialize(ctx); err != nil {
		h.Close()
		return nil, err
	}
	return h, nil
}

func (h *Host) initialize(ctx context.Context) error {
	res, err := h.Conn.Initialize(ctx, acp.InitializeRequest{
		ProtocolVersion: acp.ProtocolVersionNumber,
		ClientInfo:      &acp.Implementation{Name: "gerdr", Version: "1"},
		ClientCapabilities: acp.ClientCapabilities{
			Meta: acpx.Meta(map[string]any{"ask": true}),
		},
	})
	if err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	h.Info = res
	return nil
}

type stderrLog struct{ log *slog.Logger }

func (w *stderrLog) Write(p []byte) (int, error) {
	if s := strings.TrimSpace(string(p)); s != "" {
		w.log.Debug("agent stderr", "line", s)
	}
	return len(p), nil
}

// client 实现 acp.Client。gerdr 不声明 fs / terminal 能力，agent 自己读写文件、自己跑命令。
type client struct{ h Handler }

func (c *client) ReadTextFile(ctx context.Context, p acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	return acp.ReadTextFileResponse{}, acp.NewMethodNotFound("fs/read_text_file")
}
func (c *client) WriteTextFile(ctx context.Context, p acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	return acp.WriteTextFileResponse{}, acp.NewMethodNotFound("fs/write_text_file")
}
func (c *client) RequestPermission(ctx context.Context, p acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	return c.h.OnPermission(ctx, p)
}
func (c *client) SessionUpdate(ctx context.Context, n acp.SessionNotification) error {
	c.h.OnUpdate(n)
	return nil
}
func (c *client) CreateTerminal(ctx context.Context, p acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	return acp.CreateTerminalResponse{}, acp.NewMethodNotFound("terminal/create")
}
func (c *client) KillTerminal(ctx context.Context, p acp.KillTerminalRequest) (acp.KillTerminalResponse, error) {
	return acp.KillTerminalResponse{}, acp.NewMethodNotFound("terminal/kill")
}
func (c *client) TerminalOutput(ctx context.Context, p acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	return acp.TerminalOutputResponse{}, acp.NewMethodNotFound("terminal/output")
}
func (c *client) ReleaseTerminal(ctx context.Context, p acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return acp.ReleaseTerminalResponse{}, acp.NewMethodNotFound("terminal/release")
}
func (c *client) WaitForTerminalExit(ctx context.Context, p acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	return acp.WaitForTerminalExitResponse{}, acp.NewMethodNotFound("terminal/wait_for_exit")
}

func (c *client) HandleExtensionMethod(ctx context.Context, method string, params json.RawMessage) (any, error) {
	if method == acpx.MethodAsk {
		var req acpx.AskRequest
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, acp.NewInvalidParams(err.Error())
		}
		return c.h.OnAsk(ctx, req)
	}
	return nil, acp.NewMethodNotFound(method)
}

// Call 调一个扩展方法并解出结果。
func Call[T any](ctx context.Context, h *Host, method string, params any) (T, error) {
	var out T
	raw, err := h.Conn.CallExtension(ctx, method, params)
	if err != nil {
		return out, err
	}
	err = json.Unmarshal(raw, &out)
	return out, err
}
