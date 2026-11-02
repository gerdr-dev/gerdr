package native

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gerdr-dev/gerdr/internal/i18n"

	"github.com/coder/acp-go-sdk"
	"github.com/google/uuid"
)

// /mcp: the user's view of this session's MCP servers. The agent connects them itself, so without this a server that
// fails shows up once as a notice and then its tools are just missing. Answered here without a model call, and kept
// out of the history: the model has nothing to do with it, and the next turn's tool list already reflects any reconnect.
//
//	/mcp                    every server: transport, connected or the error, its tools
//	/mcp <name>             one server's tools with their descriptions
//	/mcp reconnect [name]   drop the connection (and a remembered failure) and connect again; all servers without a name
func (a *Agent) mcpCommand(ctx context.Context, s *session, args string) (acp.PromptResponse, error) {
	servers := s.mcpServers(s.cwd())
	var b strings.Builder
	fields := strings.Fields(args)
	switch {
	case len(servers) == 0:
		b.WriteString(i18n.S("这个会话没有配 MCP 服务器。", "No MCP servers are configured for this session."))
	case len(fields) > 0 && fields[0] == "reconnect":
		target := servers
		if len(fields) > 1 {
			target = pickServers(servers, fields[1])
			if target == nil {
				b.WriteString(unknownServer(fields[1], servers))
				break
			}
		}
		for _, m := range target {
			a.mcp.forget(m.Name)
		}
		// Warn again on the next turn if it still fails: the user just asked about it.
		s.mu.Lock()
		for _, m := range target {
			delete(s.mcpWarned, m.Name)
		}
		s.mu.Unlock()
		b.WriteString(i18n.S("已重连。", "Reconnected."))
		a.mcpReport(ctx, &b, target, false)
	case len(fields) == 1:
		target := pickServers(servers, fields[0])
		if target == nil {
			b.WriteString(unknownServer(fields[0], servers))
			break
		}
		a.mcpReport(ctx, &b, target, true)
	case len(fields) > 1:
		b.WriteString(i18n.S("用法：`/mcp`、`/mcp <名字>`、`/mcp reconnect [名字]`", "Usage: `/mcp`, `/mcp <name>`, `/mcp reconnect [name]`"))
	default:
		a.mcpReport(ctx, &b, servers, false)
		b.WriteString(i18n.S("\n`/mcp <名字>` 看工具说明，`/mcp reconnect [名字]` 重连。", "\n`/mcp <name>` shows tool descriptions, `/mcp reconnect [name]` reconnects."))
	}
	if ctx.Err() != nil {
		return acp.PromptResponse{StopReason: acp.StopReasonCancelled}, nil
	}
	s.send(acp.SessionUpdate{AgentMessageChunk: &acp.SessionUpdateAgentMessageChunk{Content: acp.TextBlock(b.String()), MessageId: acp.Ptr(uuid.NewString())}})
	return acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
}

// mcpReport connects the servers (as a turn would, so a server not started yet starts now) and writes one entry per
// server. detail: list each tool with its description instead of just the names.
func (a *Agent) mcpReport(ctx context.Context, b *strings.Builder, servers []MCPServer, detail bool) {
	clients, errs := a.mcp.get(ctx, servers, 30*time.Second)
	byName := map[string]*mcpClient{}
	for _, c := range clients {
		byName[c.name] = c
	}
	for _, m := range servers {
		fmt.Fprintf(b, "\n**%s** · %s\n", m.Name, mcpWhere(m))
		c := byName[m.Name]
		if c == nil {
			err := errs[m.Name]
			msg := i18n.S("没连上", "Not connected")
			if errors.Is(err, context.DeadlineExceeded) {
				msg = i18n.S("30 秒内没连上（可能还在启动，过一会儿再看）", "Didn't connect within 30 seconds (it may still be starting; check again in a moment)")
			} else if err != nil {
				msg = i18n.S("没连上：", "Not connected: ") + oneLine(err.Error(), 400)
			}
			b.WriteString("- " + msg + "\n")
			continue
		}
		tools := append([]mcpTool(nil), c.tools...)
		sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
		if i18n.IsZH() {
			fmt.Fprintf(b, "- 已连上，%d 个工具\n", len(tools))
		} else if len(tools) == 1 {
			b.WriteString("- Connected, 1 tool\n")
		} else {
			fmt.Fprintf(b, "- Connected, %d tools\n", len(tools))
		}
		if detail {
			for _, t := range tools {
				ro := ""
				if t.Annotations != nil && t.Annotations.ReadOnlyHint {
					ro = i18n.S("（只读，不用审批）", " (read-only, no approval needed)")
				}
				fmt.Fprintf(b, "  - `%s`%s %s\n", t.Name, ro, oneLine(t.Description, 200))
			}
		} else if len(tools) > 0 {
			names := make([]string, len(tools))
			for i, t := range tools {
				names[i] = "`" + t.Name + "`"
			}
			b.WriteString("- " + strings.Join(names, " ") + "\n")
		}
	}
}

// mcpWhere: how the server is reached, as configured (${VAR} left unexpanded so secrets in the environment don't
// show; headers and env are never shown).
func mcpWhere(m MCPServer) string {
	if m.remote() || m.Type == "sse" {
		t := m.Type
		if t == "" {
			t = "http"
		}
		return t + " `" + m.URL + "`"
	}
	return "stdio `" + oneLine(strings.Join(append([]string{m.Command}, m.Args...), " "), 200) + "`"
}

func pickServers(servers []MCPServer, name string) []MCPServer {
	for _, m := range servers {
		if m.Name == name {
			return []MCPServer{m}
		}
	}
	return nil
}

func unknownServer(name string, servers []MCPServer) string {
	names := make([]string, len(servers))
	for i, m := range servers {
		names[i] = "`" + m.Name + "`"
	}
	return fmt.Sprintf(i18n.S("没有叫 `%s` 的 MCP 服务器。这个会话有：%s", "No MCP server named `%s`. This session has: %s"), name, strings.Join(names, " "))
}
