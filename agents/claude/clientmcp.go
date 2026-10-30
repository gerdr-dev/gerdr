package claude

import (
	"encoding/json"
	"regexp"

	"github.com/coder/acp-go-sdk"
)

// MCP servers the ACP client passes in session/new (and resume / load) go to claude as one --mcp-config JSON
// string; their tools are pre-approved with --allowedTools mcp__<server>, since the client chose them. With
// Options.StrictMCP they are the only ones (--strict-mcp-config).

func (s *session) setMCP(servers []acp.McpServer) {
	if servers == nil {
		return // resume without the field: keep what session/new gave
	}
	s.mu.Lock()
	s.mcp = servers
	s.mu.Unlock()
}

var mcpNameChars = regexp.MustCompile(`[^A-Za-z0-9_-]`)

// clientMCPArgs: called with s.mu held.
func (s *session) clientMCPArgs() []string {
	cfg := map[string]any{}
	var allow []string
	for _, x := range s.mcp {
		var name string
		switch {
		case x.Stdio != nil:
			env := map[string]string{}
			for _, e := range x.Stdio.Env {
				env[e.Name] = e.Value
			}
			name = x.Stdio.Name
			cfg[name] = map[string]any{"type": "stdio", "command": x.Stdio.Command, "args": x.Stdio.Args, "env": env}
		case x.Http != nil:
			name = x.Http.Name
			cfg[name] = map[string]any{"type": "http", "url": x.Http.Url, "headers": headers(x.Http.Headers)}
		case x.Sse != nil:
			name = x.Sse.Name
			cfg[name] = map[string]any{"type": "sse", "url": x.Sse.Url, "headers": headers(x.Sse.Headers)}
		default:
			continue
		}
		// claude names the tools mcp__<server>__<tool>, with characters outside [A-Za-z0-9_-] replaced
		allow = append(allow, "mcp__"+mcpNameChars.ReplaceAllString(name, "_"))
	}
	var args []string
	if len(cfg) > 0 {
		b, _ := json.Marshal(map[string]any{"mcpServers": cfg})
		args = append(args, "--mcp-config", string(b))
		for _, a := range allow {
			args = append(args, "--allowedTools", a)
		}
	}
	if s.a.opts.StrictMCP {
		args = append(args, "--strict-mcp-config")
	}
	return args
}

func headers(hs []acp.HttpHeader) map[string]string {
	out := map[string]string{}
	for _, h := range hs {
		out[h.Name] = h.Value
	}
	return out
}

// mcpFingerprint: Options.Ext's fingerprint of claude's own MCP configuration ("" without Ext, or with
// StrictMCP, where claude's own servers aren't used).
func (a *Agent) mcpFingerprint(cwd string) string {
	if a.opts.Ext == nil || a.opts.StrictMCP {
		return ""
	}
	return a.opts.Ext.MCPFingerprint(cwd)
}
