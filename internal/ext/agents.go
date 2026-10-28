package ext

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

func home() string {
	h, _ := os.UserHomeDir()
	return h
}

// ---- claude ----

// ClaudeSkills：claude 自己会加载的 skill —— 用户级 ~/.claude/skills、项目级（cwd 往上每一层的
// .claude/skills）、已启用插件里的。插件的名字是 `<插件名>:<skill>`。
func ClaudeSkills(cwd string) []Skill {
	out := scanSkills(filepath.Join(home(), ".claude", "skills"), "claude")
	if cwd != "" {
		for d := cwd; ; d = filepath.Dir(d) {
			if d != home() {
				out = append(out, scanSkills(filepath.Join(d, ".claude", "skills"), "claude-project")...)
			}
			if p := filepath.Dir(d); p == d {
				break
			}
		}
	}
	for _, pl := range ClaudePlugins(cwd) {
		short, _, _ := strings.Cut(pl.ID, "@")
		for _, sk := range scanSkills(filepath.Join(pl.Path, "skills"), "claude-plugin:"+pl.ID) {
			sk.Name = short + ":" + sk.Name
			out = append(out, sk)
		}
	}
	return out
}

type ClaudePlugin struct {
	ID   string `json:"id"` // 名字@市场
	Path string `json:"path"`
}

// ClaudePlugins：用户 / 项目设置里 enabledPlugins 为 true、并且装了的插件。
func ClaudePlugins(cwd string) []ClaudePlugin {
	enabled := map[string]bool{}
	files := []string{filepath.Join(home(), ".claude", "settings.json")}
	if cwd != "" {
		files = append(files, filepath.Join(cwd, ".claude", "settings.json"), filepath.Join(cwd, ".claude", "settings.local.json"))
	}
	for _, f := range files {
		var s struct {
			EnabledPlugins map[string]bool `json:"enabledPlugins"`
		}
		if readJSON(f, &s) == nil {
			for k, v := range s.EnabledPlugins {
				enabled[k] = v
			}
		}
	}
	var inst struct {
		Plugins map[string][]struct {
			InstallPath string `json:"installPath"`
		} `json:"plugins"`
	}
	_ = readJSON(filepath.Join(home(), ".claude", "plugins", "installed_plugins.json"), &inst)
	var out []ClaudePlugin
	for id, on := range enabled {
		if !on || len(inst.Plugins[id]) == 0 {
			continue
		}
		out = append(out, ClaudePlugin{ID: id, Path: inst.Plugins[id][len(inst.Plugins[id])-1].InstallPath})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ClaudeMCP：~/.claude.json 里用户级的 + 这个项目的 + 项目根上的 .mcp.json。
// （claude.ai 账号里连的 connector 不在这里，它们只在 claude 自己那边。）
func ClaudeMCP(cwd string) []MCPServer {
	var cfg struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
		Projects   map[string]struct {
			MCPServers map[string]json.RawMessage `json:"mcpServers"`
		} `json:"projects"`
	}
	_ = readJSON(filepath.Join(home(), ".claude.json"), &cfg)
	all := map[string]json.RawMessage{}
	for k, v := range cfg.MCPServers {
		all[k] = v
	}
	if p, ok := cfg.Projects[cwd]; ok && cwd != "" {
		for k, v := range p.MCPServers {
			all[k] = v
		}
	}
	if cwd != "" {
		var proj struct {
			MCPServers map[string]json.RawMessage `json:"mcpServers"`
		}
		if readJSON(filepath.Join(cwd, ".mcp.json"), &proj) == nil {
			for k, v := range proj.MCPServers {
				all[k] = v
			}
		}
	}
	var out []MCPServer
	for name, raw := range all {
		var s MCPServer
		_ = json.Unmarshal(raw, &s)
		s.Name, s.Enabled = name, true
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ClaudeArgs：按设置给 claude 加的启动参数。cwd 用来找项目级的 skill / 插件（只用 gerdr 的时候要关掉它们）。
func (m *Manager) ClaudeArgs(id, cwd string) []string {
	s := m.Settings()
	var args []string
	if s.MCPSource.UsesGerdr() {
		servers := map[string]any{}
		for _, v := range s.MCP {
			if !v.Enabled {
				continue
			}
			if v.Remote() {
				t := v.Type
				if t == "" {
					t = "http"
				}
				servers[v.Name] = map[string]any{"type": t, "url": v.URL, "headers": v.Headers}
			} else {
				servers[v.Name] = map[string]any{"command": v.Command, "args": nonNil(v.Args), "env": v.Env}
			}
		}
		b, _ := json.Marshal(map[string]any{"mcpServers": servers})
		args = append(args, "--mcp-config", string(b))
		if s.MCPSource == SourceGerdr {
			args = append(args, "--strict-mcp-config")
		}
	}
	settings := map[string]any{}
	overrides := map[string]string{}
	if dir, err := m.ClaudeAddDir(id, s.SkillSource.UsesGerdr()); err == nil && dir != "" {
		args = append(args, "--add-dir", dir)
	}
	if s.SkillSource == SourceGerdr {
		gerdr := map[string]bool{}
		for _, sk := range append(m.Skills(), m.Builtin()...) {
			gerdr[sk.Name] = true
		}
		for _, sk := range ClaudeSkills(cwd) {
			if !strings.HasPrefix(sk.Origin, "claude-plugin:") && !gerdr[sk.Name] {
				overrides[sk.Name] = "off"
			}
		}
		// 插件里的 skill 按名字关不掉（实测），只能把插件整个关了 —— 它带的 hook / MCP 也跟着没了。
		plugins := map[string]bool{}
		for _, p := range ClaudePlugins(cwd) {
			plugins[p.ID] = false
		}
		if len(plugins) > 0 {
			settings["enabledPlugins"] = plugins
		}
	}
	if len(overrides) > 0 {
		settings["skillOverrides"] = overrides
	}
	if len(settings) > 0 {
		b, _ := json.Marshal(settings)
		args = append(args, "--settings", string(b))
	}
	return args
}

// ---- codex ----

// CodexSkills：~/.codex/skills 和 ~/.agents/skills 下的（只给界面列着看、导入用；
// 真正给 codex 关哪些，codex 适配器用 skills/list 问它自己，那份才全）。
func CodexSkills() []Skill {
	out := scanSkills(filepath.Join(home(), ".codex", "skills"), "codex")
	out = append(out, scanSkills(filepath.Join(home(), ".agents", "skills"), "codex")...)
	return out
}

// CodexMCP：~/.codex/config.toml 的 [mcp_servers.*]。
func CodexMCP() []MCPServer {
	var cfg struct {
		MCPServers map[string]struct {
			Command     string            `toml:"command"`
			Args        []string          `toml:"args"`
			Env         map[string]string `toml:"env"`
			URL         string            `toml:"url"`
			HTTPHeaders map[string]string `toml:"http_headers"`
			Enabled     *bool             `toml:"enabled"`
		} `toml:"mcp_servers"`
	}
	dir := os.Getenv("CODEX_HOME")
	if dir == "" {
		dir = filepath.Join(home(), ".codex")
	}
	if _, err := toml.DecodeFile(filepath.Join(dir, "config.toml"), &cfg); err != nil {
		return nil
	}
	var out []MCPServer
	for name, v := range cfg.MCPServers {
		s := MCPServer{Name: name, Command: v.Command, Args: v.Args, Env: v.Env, URL: v.URL, Headers: v.HTTPHeaders, Enabled: v.Enabled == nil || *v.Enabled}
		if v.URL != "" {
			s.Type = "http"
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// CodexMCPOverrides：gerdr 的 MCP 写成 codex 的配置覆盖（`mcp_servers.<名>` = 表）。
// codex 只认 stdio 和 streamable http，sse 的跳过。
func (m *Manager) CodexMCPOverrides() map[string]any {
	s := m.Settings()
	out := map[string]any{}
	if !s.MCPSource.UsesGerdr() {
		return out
	}
	for _, v := range s.MCP {
		if !v.Enabled || v.Type == "sse" {
			continue
		}
		if v.Remote() {
			t := map[string]any{"url": v.URL}
			if len(v.Headers) > 0 {
				t["http_headers"] = v.Headers
			}
			out["mcp_servers."+v.Name] = t
		} else {
			t := map[string]any{"command": v.Command, "args": nonNil(v.Args)}
			if len(v.Env) > 0 {
				t["env"] = v.Env
			}
			out["mcp_servers."+v.Name] = t
		}
	}
	return out
}

func nonNil(a []string) []string {
	if a == nil {
		return []string{}
	}
	return a
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}
