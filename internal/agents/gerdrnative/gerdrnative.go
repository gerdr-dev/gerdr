// Package gerdrnative wires the public native agent (github.com/gerdr-dev/gerdr/native) into gerdr: skills and
// MCP servers from gerdr's settings (internal/ext), MCP sign-in (internal/mcpauth), the login shell's PATH and the
// child environment (internal/sysenv), and API keys kept in secrets.json (internal/config). native itself imports
// none of these, so other programs can embed it; everything gerdr-specific comes in through native.Options here.
package gerdrnative

import (
	"context"

	"github.com/gerdr-dev/gerdr/internal/config"
	"github.com/gerdr-dev/gerdr/internal/ext"
	"github.com/gerdr-dev/gerdr/internal/mcpauth"
	"github.com/gerdr-dev/gerdr/internal/sysenv"
	"github.com/gerdr-dev/gerdr/native"
)

// SessionEnvKey: bash commands see which session they run in (`gerdr session current` asks /api/resolve with it).
const SessionEnvKey = "GERDR_NATIVE_SESSION_ID"

// Options is native.Options as gerdr runs it. em nil = only Claude Code's skills and no MCP servers (the
// stand-alone `gerdr acp native`).
func Options(dir string, em *ext.Manager, cfg func() native.Config, prompt string) native.Options {
	return native.Options{
		Dir: dir, Config: cfg, Prompt: prompt,
		Skills: func(cwd string) []native.Skill { return skillsFor(em, cwd) },
		MCP: func(cwd string) []native.MCPServer {
			var out []native.MCPServer
			for _, s := range resolveMCP(em) {
				out = append(out, MCPServer(s))
			}
			return out
		},
		MCPAuth: Auth{},
		Env:     ChildEnv,
		BashEnv: func(sessionID string, _ map[string]any) []string {
			return []string{SessionEnvKey + "=" + sessionID}
		},
		SafeCommand: sysenv.SafeGerdrCLI,
	}
}

// ChildEnv is the environment for the agent's child processes: the login shell's PATH, minus the variables that
// would make them mistake who they are (sysenv.ChildEnv).
func ChildEnv(extra ...string) []string {
	sysenv.AdoptLoginPath()
	return sysenv.ChildEnv(extra...)
}

// Auth hands native the tokens from mcpauth.Default (Claude Code's login, else gerdr's own), looked up per call
// since main sets Default at startup.
type Auth struct{}

func (Auth) Token(ctx context.Context, name, url string, headers map[string]string) string {
	if st := mcpauth.Default; st != nil {
		return st.Token(ctx, name, url, headers)
	}
	return ""
}

func (Auth) Invalidate(name, url string) {
	if st := mcpauth.Default; st != nil {
		st.Invalidate(name, url)
	}
}

// MCPServer converts gerdr's MCP setting to native's.
func MCPServer(s ext.MCPServer) native.MCPServer {
	return native.MCPServer{Name: s.Name, Type: s.Type, Command: s.Command, Args: s.Args, Env: s.Env, URL: s.URL, Headers: s.Headers}
}

// resolveMCP：Native 引擎要用的服务器 = Gerdr 那份里开着的（角色限定了的再筛）。不看设置里的来源 ——
// 来源只管 Claude Code / Codex。以前「Agent 自己的」对 Native 来说就是借 Claude Code 配的（~/.claude.json），
// 分出「引擎」之后用户看不懂一个引擎为什么跟着另一个引擎的配置走，改成只用 Gerdr 的（升级时 ext.MigrateNativeMCP 把借着用的导入过来一次）。
func resolveMCP(m *ext.Manager) []ext.MCPServer {
	if m == nil {
		return nil
	}
	var out []ext.MCPServer
	for _, s := range m.Settings().MCP {
		if s.Enabled && m.AllowMCP(s.Name) {
			out = append(out, s)
		}
	}
	return out
}

// skillsFor：给这个 agent 的 skill —— 内置的（gerdr、gerdr-ui）总是给；「agent 自己的」当成 Claude Code 的那份
// （用户级、项目级：这个 agent 没有自己的 skill 目录，用户的 skill 基本都在 ~/.claude/skills 里）；
// 设置里的来源（只用 agent 的 / 两者 / 只用 gerdr 的）照常生效。同名的先到先得。
func skillsFor(m *ext.Manager, cwd string) []native.Skill {
	var all []ext.Skill
	src := ext.SourceAgent
	if m != nil {
		all = append(all, m.Builtin()...)
		src = m.Settings().SkillSource
		if src.UsesGerdr() {
			all = append(all, m.Skills()...)
		}
	}
	if src != ext.SourceGerdr {
		all = append(all, ext.ClaudeSkills(cwd)...)
	}
	seen := map[string]bool{}
	var kept []ext.Skill
	for _, s := range all {
		if !s.Enabled || seen[s.Name] {
			continue
		}
		seen[s.Name] = true
		kept = append(kept, s)
	}
	var out []native.Skill
	for _, s := range m.FilterSkills(kept) { // 角色限定了 skill 的话只留挑中的（m 是 nil 也行）
		out = append(out, native.Skill{Name: s.Name, Description: s.Description, Dir: s.Dir})
	}
	return out
}

// WithKeys swaps config.Hidden for the real keys from secrets.json.
func WithKeys(c native.Config, sec *config.Secrets) native.Config {
	ps := make([]native.Provider, len(c.Providers))
	for i, p := range c.Providers {
		ps[i] = WithKey(p, sec)
	}
	c.Providers = ps
	return c
}

func WithKey(p native.Provider, sec *config.Secrets) native.Provider {
	if p.APIKey == config.Hidden {
		p.APIKey = sec.Get(config.LLMKeyName(p.ID))
	}
	return p
}
