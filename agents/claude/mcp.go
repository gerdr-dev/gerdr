package claude

import (
	"context"
	"time"

	"github.com/gerdr-dev/gerdr/acpx"
)

// mcpReconnect asks every live claude process to reconnect one MCP server (control request mcp_reconnect, which the CLI
// supports in stream-json mode: "Reconnects a disconnected or failed MCP server"). Processes are per conversation, and each
// connects its own MCP servers, so one stuck server means asking all of them. One without that server errors; that's fine.
func (a *Agent) mcpReconnect(ctx context.Context, server string) acpx.MCPReconnectResponse {
	a.mu.Lock()
	ss := make([]*session, 0, len(a.sessions))
	for _, s := range a.sessions {
		ss = append(ss, s)
	}
	a.mu.Unlock()
	var out acpx.MCPReconnectResponse
	for _, s := range ss {
		s.mu.Lock()
		p := s.p
		ok := p != nil && s.exiting == nil && p.alive()
		s.mu.Unlock()
		if !ok {
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		_, err := p.control(cctx, map[string]any{"subtype": "mcp_reconnect", "serverName": server})
		cancel()
		if err != nil {
			out.Error = err.Error()
			continue
		}
		out.Reconnected++
	}
	if out.Reconnected > 0 {
		out.Error = ""
	}
	return out
}
