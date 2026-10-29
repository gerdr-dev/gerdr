package hub

import (
	"context"

	"github.com/gerdr-dev/gerdr/internal/acpx"
	"github.com/gerdr-dev/gerdr/internal/agenthost"
	"github.com/gerdr-dev/gerdr/internal/agents/native"
)

// NativeMCP：Gerdr Agent 实际在用哪些 MCP 服务器、连上没有（设置 → MCP 页）。
// native 已经起着就问它（连接池里有实时状态）；没起、又不是要「检查连接」的，只按配置算，不为了看一眼把它起起来。
// cwd 决定项目级的配置（.mcp.json、~/.claude.json 里这个项目的）；设置页给空，只看用户级的。
func (h *Hub) NativeMCP(ctx context.Context, cwd string, probe bool) ([]native.MCPEntry, error) {
	h.mu.Lock()
	c := h.hosts["native"]
	h.mu.Unlock()
	if c == nil && !probe {
		return native.ResolveMCP(h.opt.Ext, cwd), nil
	}
	if c == nil {
		var err error
		if c, err = h.host(ctx, "native"); err != nil {
			return nil, err
		}
	}
	res, err := agenthost.Call[struct {
		Servers []native.MCPEntry `json:"servers"`
	}](ctx, c, acpx.MethodMCPStatus, map[string]any{"cwd": cwd, "probe": probe})
	return res.Servers, err
}
