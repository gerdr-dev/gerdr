package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/url"
	"os"

	"github.com/spf13/cobra"
)

// gerdr panel mcp: a project's Work tools (internal/work/tools.go) as an MCP server on stdio, for one conversation.
// The hub hands it to the project's Work agent and to the sessions the Work starts (hub.Options.MCP); every call
// goes to the backend's /api/work/tools with --session, which the local function sees as ctx.caller.session. Only
// what agents use: initialize, tools/list, tools/call (and ping). The list is read on each tools/list, so tools a
// Work adds show up without restarting anything but the agent's own tool cache.
func workMCPCmd(project func() string) *cobra.Command {
	var session string
	c := &cobra.Command{
		Use:    "mcp",
		Short:  "Serve a panel's tools over MCP (stdio) for one conversation",
		Hidden: true, // started by gerdr for agents, not typed
		Args:   cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			api, err := newAPIClient()
			if err != nil {
				return err
			}
			return serveWorkMCP(api, project(), session)
		},
	}
	c.Flags().StringVar(&session, "session", "", "the conversation the calls are made for (ctx.caller.session)")
	return c
}

type rpcMsg struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

func serveWorkMCP(api *apiClient, project, session string) error {
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 1<<20), 32<<20)
	out := json.NewEncoder(os.Stdout)
	reply := func(id json.RawMessage, result any, rpcErr error) {
		m := map[string]any{"jsonrpc": "2.0", "id": id}
		if rpcErr != nil {
			m["error"] = map[string]any{"code": -32603, "message": rpcErr.Error()}
		} else {
			m["result"] = result
		}
		_ = out.Encode(m)
	}
	q := "?project=" + url.QueryEscape(project)
	for in.Scan() {
		var m rpcMsg
		if json.Unmarshal(in.Bytes(), &m) != nil || len(m.ID) == 0 {
			continue // notifications (initialized, cancelled) need no answer
		}
		switch m.Method {
		case "initialize":
			var p struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			_ = json.Unmarshal(m.Params, &p)
			if p.ProtocolVersion == "" {
				p.ProtocolVersion = "2024-11-05"
			}
			reply(m.ID, map[string]any{
				"protocolVersion": p.ProtocolVersion,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "gerdr-work", "version": "1"},
				"instructions":    "Tools of the panel that started you. Use them to report on and change your task there.",
			}, nil)
		case "ping":
			reply(m.ID, map[string]any{}, nil)
		case "tools/list":
			var res struct {
				List []struct {
					Name        string         `json:"name"`
					Description string         `json:"description"`
					InputSchema map[string]any `json:"inputSchema"`
				} `json:"list"`
			}
			if err := api.do("GET", "/api/work/tools"+q, nil, &res); err != nil {
				reply(m.ID, nil, err)
				continue
			}
			tools := []map[string]any{}
			for _, t := range res.List {
				tools = append(tools, map[string]any{"name": t.Name, "description": t.Description, "inputSchema": t.InputSchema})
			}
			reply(m.ID, map[string]any{"tools": tools}, nil)
		case "tools/call":
			var p struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			_ = json.Unmarshal(m.Params, &p)
			var res struct {
				Result any `json:"result"`
			}
			// A failing function is the tool's answer (isError), not a protocol error: the agent reads why and adjusts
			if err := api.do("POST", "/api/work/tools/"+url.PathEscape(p.Name)+q, map[string]any{"session": session, "args": p.Arguments}, &res); err != nil {
				reply(m.ID, map[string]any{"content": []map[string]any{{"type": "text", "text": err.Error()}}, "isError": true}, nil)
				continue
			}
			b, _ := json.Marshal(res.Result)
			reply(m.ID, map[string]any{"content": []map[string]any{{"type": "text", "text": string(b)}}}, nil)
		default:
			reply(m.ID, nil, fmt.Errorf("method %s not supported", m.Method))
		}
	}
	return in.Err()
}
