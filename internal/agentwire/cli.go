package agentwire

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/gerdr-dev/gerdr/agents/claude"
	"github.com/gerdr-dev/gerdr/agents/codex"
	"github.com/gerdr-dev/gerdr/internal/ext"
	"github.com/gerdr-dev/gerdr/rpcline"
)

// ClaudeExt hands the claude adapter gerdr's skill / MCP settings (m nil = claude's own config only).
func ClaudeExt(m *ext.Manager) claude.Ext { return claudeExt{m} }

type claudeExt struct{ m *ext.Manager }

func (x claudeExt) Fingerprint() string {
	if x.m == nil {
		return ""
	}
	return x.m.Fingerprint()
}

func (x claudeExt) Args(sessionID, cwd string) []string {
	if x.m == nil {
		return nil
	}
	return x.m.ClaudeArgs(sessionID, cwd)
}

func (x claudeExt) MCPFingerprint(cwd string) string { return x.m.ClaudeMCPFingerprint(cwd) }

// CodexExt hands the codex adapter gerdr's skill / MCP settings (m nil = codex's own config only).
func CodexExt(m *ext.Manager) codex.Ext {
	if m == nil {
		return nil // a nil interface, not a wrapped nil pointer: the adapter checks for nil
	}
	return codexExt{m}
}

type codexExt struct{ m *ext.Manager }

func (x codexExt) Fingerprint() string { return x.m.Fingerprint() }

// Config 把 skill / MCP 设置翻成 thread/start、thread/resume 的 config 覆盖。
// 覆盖是**合并**进用户配置的，所以「关掉 codex 自己的」只能逐个列出来关（见 ext 包注释）。
func (x codexExt) Config(ctx context.Context, rpc *rpcline.Conn, cwd string, log *slog.Logger) map[string]any {
	st := x.m.Settings()
	cfg := x.m.CodexMCPOverrides()
	if st.MCPSource == ext.SourceGerdr || x.m.LimitsMCP() {
		mine := map[string]bool{}
		if st.MCPSource.UsesGerdr() {
			for _, v := range st.MCP {
				mine[v.Name] = true
			}
		}
		// 只用 gerdr 的：codex 自己的全关；角色限定了 MCP：codex 自己的里没挑的关（gerdr 那份已经在 CodexMCPOverrides 里筛过）
		for _, v := range ext.CodexMCP() {
			if !mine[v.Name] && (st.MCPSource == ext.SourceGerdr || !x.m.AllowMCP(v.Name)) {
				cfg["mcp_servers."+v.Name+".enabled"] = false
			}
		}
	}
	// gerdr 的 skill 目录是整个 app-server 的设置：每次接 thread 前按当前设置摆一遍。
	// 内置 skill（多会话协作、改界面）不管来源选什么都给；gerdr 自己那份要选了才给。
	x.m.Builtin() // 确保已经装到磁盘上
	roots := []string{x.m.BuiltinDir()}
	if st.SkillSource.UsesGerdr() {
		roots = append(roots, x.m.SkillsDir())
	}
	if err := rpc.Call(ctx, "skills/extraRoots/set", map[string]any{"extraRoots": roots}, nil); err != nil {
		log.Warn("skills/extraRoots/set failed", "err", err)
	}
	if st.SkillSource != ext.SourceAgent || len(st.DisabledSkills) > 0 || x.m.LimitsSkills() {
		var res struct {
			Data []struct {
				Skills []struct {
					Name    string `json:"name"`
					Path    string `json:"path"`
					Scope   string `json:"scope"`
					Enabled bool   `json:"enabled"`
				} `json:"skills"`
			} `json:"data"`
		}
		params := map[string]any{"forceReload": true}
		if cwd != "" {
			params["cwds"] = []string{cwd}
		}
		if err := rpc.Call(ctx, "skills/list", params, &res); err != nil {
			log.Warn("skills/list failed", "err", err)
		}
		dis := map[string]bool{}
		for _, n := range st.DisabledSkills {
			dis[n] = true
		}
		// codex 报回来的路径是解开过符号链接的（/var → /private/var），比前缀前这边也解一遍。
		under := func(p, dir string) bool {
			if real, err := filepath.EvalSymlinks(dir); err == nil && strings.HasPrefix(p, real+string(filepath.Separator)) {
				return true
			}
			return strings.HasPrefix(p, dir+string(filepath.Separator))
		}
		// skills.config 整个被覆盖掉，所以用户自己在 config.toml 里关掉的也要照抄一遍。
		off := []map[string]any{}
		for _, e := range res.Data {
			for _, sk := range e.Skills {
				var disable bool
				switch {
				case !sk.Enabled:
					disable = true
				case under(sk.Path, x.m.BuiltinDir()):
					disable = dis[sk.Name]
				case under(sk.Path, x.m.SkillsDir()):
					disable = dis[sk.Name] || !st.SkillSource.UsesGerdr()
				default:
					disable = st.SkillSource == ext.SourceGerdr && sk.Scope != "system"
				}
				// 角色限定了 skill：没挑的也关。codex 自带的（system）界面上列不出来、挑不了，不动它们。
				if !disable && sk.Scope != "system" && !x.m.AllowSkill(sk.Name) {
					disable = true
				}
				if disable {
					off = append(off, map[string]any{"path": sk.Path, "enabled": false})
				}
			}
		}
		cfg["skills.config"] = off
	}
	return cfg
}
