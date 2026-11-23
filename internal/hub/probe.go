package hub

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/coder/acp-go-sdk"

	"github.com/gerdr-dev/gerdr/acpx"
	"github.com/gerdr-dev/gerdr/internal/agenthost"
	"github.com/gerdr-dev/gerdr/internal/i18n"
)

// ProbeAgent starts an external ACP agent's command once, opens a session, reads the modes and config options
// (model, effort…) it reports and shuts it down again. Settings → Agent calls it as soon as a command is filled in:
// those options are otherwise only known after a first real session, so a new external agent had no model to pick
// as its default. What it finds is cached under id (the same optCache a real session fills), so the editor and
// `gerdr agent` see it on the next look too.
func (h *Hub) ProbeAgent(ctx context.Context, id string, command []string, env map[string]string) (agentOpts, error) {
	if len(command) == 0 {
		return agentOpts{}, errors.New(i18n.S("没填启动命令", "No command given"))
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second) // npx may download the package first
	defer cancel()
	log := h.log.With("probe", id)
	home, _ := os.UserHomeDir()
	c, err := agenthost.StartProcess(ctx, command, AgentDef{Env: env}.envList(), home, probeHandler{}, log)
	if err != nil {
		return agentOpts{}, probeErr(ctx, err)
	}
	defer c.Close()
	res, err := c.Conn.NewSession(ctx, acp.NewSessionRequest{Cwd: home, McpServers: []acp.McpServer{}})
	if err != nil {
		return agentOpts{}, probeErr(ctx, err)
	}
	if c.Info.AgentCapabilities.SessionCapabilities.Close != nil {
		cctx, ccancel := context.WithTimeout(context.Background(), 3*time.Second)
		_, _ = c.Conn.CloseSession(cctx, acp.CloseSessionRequest{SessionId: res.SessionId})
		ccancel()
	}
	o := agentOpts{Modes: res.Modes, Config: res.ConfigOptions}
	if o.Modes != nil {
		o.Config = withoutModeOption(o.Modes.AvailableModes, o.Config)
	}
	if id != "" {
		h.opts.set(id, res.Modes, res.ConfigOptions)
		h.Broadcast("agents", h.Agents()) // the editor's dropdowns read the agent list
	}
	return o, nil
}

func probeErr(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return errors.New(i18n.S("启动超时：命令没有按 ACP 回话（是不是少了 acp 之类的参数？）", "Timed out: the command didn't answer over ACP (missing an `acp` argument?)"))
	}
	msg := err.Error()
	if strings.Contains(strings.ToLower(msg), "auth") {
		return fmt.Errorf(i18n.S("它要先登录：在终端里跑一次这个命令登录好再试（%s）", "It needs signing in first: run it once in a terminal to sign in, then try again (%s)"), msg)
	}
	return err
}

// probeHandler answers a probe's agent: nothing is asked of it, so permission / questions are refused.
type probeHandler struct{}

func (probeHandler) OnUpdate(acp.SessionNotification) {}
func (probeHandler) OnPermission(context.Context, acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{Cancelled: &acp.RequestPermissionOutcomeCancelled{}}}, nil
}
func (probeHandler) OnAsk(context.Context, acpx.AskRequest) (acpx.AskResponse, error) {
	return acpx.AskResponse{}, errors.New("probe")
}
