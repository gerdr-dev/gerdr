// Package ext 管 skill 和 MCP 从哪来：用 agent 自己的、gerdr 自己的、还是两份都用。
//
// gerdr 自己的那份：skill 在 <GERDR_DIR>/skills/<名字>/SKILL.md，MCP 在 <GERDR_DIR>/ext.json。
// 两家 agent 吃它们的方式完全不同（实测，见 docs/dev/ARCHITECTURE.md「skill / MCP」）：
//
//   - claude：skill 走 `--add-dir`（那个目录下的 .claude/skills 会被加载，名字不带前缀；
//     `--plugin-dir` 也行但名字会变成 `gerdr:xxx`）；关掉 agent 自己的 skill 走设置里的
//     `skillOverrides: {名字: "off"}`，插件里的 skill 只能整个插件关（`enabledPlugins`）。
//     MCP 走 `--mcp-config <json>`，只用 gerdr 的再加 `--strict-mcp-config`。
//   - codex：thread/start / thread/resume 的 `config` 是按键覆盖、**合并**进用户配置的 ——
//     给 `mcp_servers = {}` 删不掉任何东西，只能逐个 `mcp_servers.<名>.enabled = false`；
//     skill 用 `skills.config = [{path, enabled:false}]` 按会话关，gerdr 的目录用
//     `skills/extraRoots/set` 加进去（那是整个 app-server 进程的设置）。
package ext

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Source：从哪儿取。
type Source string

const (
	SourceAgent Source = "agent" // 只用 agent 自己配的（默认，和在终端里用一样）
	SourceBoth  Source = "both"  // agent 自己的 + gerdr 的
	SourceGerdr Source = "gerdr" // 只用 gerdr 的，agent 自己配的一律关掉
)

func (s Source) valid() bool { return s == SourceAgent || s == SourceBoth || s == SourceGerdr }

// UsesGerdr：gerdr 那份要不要加进去。
func (s Source) UsesGerdr() bool { return s == SourceBoth || s == SourceGerdr }

type MCPServer struct {
	Name    string            `json:"name"`
	Type    string            `json:"type,omitempty"` // stdio（默认）| http | sse
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Enabled bool              `json:"enabled"`
}

func (s MCPServer) Remote() bool {
	return s.Type == "http" || s.Type == "sse" || (s.Command == "" && s.URL != "")
}

type Settings struct {
	MCPSource   Source      `json:"mcpSource"`
	SkillSource Source      `json:"skillSource"`
	MCP         []MCPServer `json:"mcp"`
	// DisabledSkills：gerdr 自己那份里关掉的（文件留着，只是不给 agent）。
	DisabledSkills []string `json:"disabledSkills"`
}

type Skill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Dir         string `json:"dir"`
	Enabled     bool   `json:"enabled"`
	// Origin：gerdr / claude（用户级）/ claude-project / claude-plugin:<id> / codex
	Origin string `json:"origin"`
}

var nameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

// ValidName：skill 和 MCP 的名字。codex 的覆盖键是 `mcp_servers.<名>.enabled`，带点就拆错了。
func ValidName(n string) bool { return nameRE.MatchString(n) }

type Manager struct {
	*core
	// only：角色（设置 → Agent 里配的）只给它挑的那几个；nil = 不限，全照全局设置。见 Scoped。
	only *Only
}

// core 是同一个 GERDR_DIR 下所有 Manager（全局的 + 各角色 Scoped 出来的）共用的：同一把锁、内置 skill 只装一次
// （各装各的会在别的 claude 正读着时把 builtin-skills 删了重写）。
type core struct {
	dir         string
	mu          sync.Mutex
	builtinOnce sync.Once
}

func New(dir string) *Manager { return &Manager{core: &core{dir: dir}} }

// Only：一个角色限定的 skill / MCP（按名字）。某一项是 nil = 那一项不限，照全局的来源给；
// 非 nil（哪怕是空的）= 只给列出来的那些 —— 空列表就是一个都不给。
// 只在「全局设置下本来就会给的」里面挑：不会把来源里没有的东西加进来。
type Only struct {
	Skills []string
	MCP    []string
}

// Scoped：给一个角色用的 Manager —— 设置、目录和全局的同一份，只是 skill / MCP 按 o 再筛一遍。
// o 两项都是 nil 就原样返回。
func (m *Manager) Scoped(o Only) *Manager {
	if m == nil || (o.Skills == nil && o.MCP == nil) {
		return m
	}
	return &Manager{core: m.core, only: &o}
}

// LimitsSkills / LimitsMCP：这个角色有没有限定。m 可以是 nil。
func (m *Manager) LimitsSkills() bool { return m != nil && m.only != nil && m.only.Skills != nil }
func (m *Manager) LimitsMCP() bool    { return m != nil && m.only != nil && m.only.MCP != nil }

// AllowSkill / AllowMCP：角色限定里有没有这个名字；没限定的一律 true。
func (m *Manager) AllowSkill(name string) bool {
	return !m.LimitsSkills() || contains(m.only.Skills, name)
}

func (m *Manager) AllowMCP(name string) bool {
	return !m.LimitsMCP() || contains(m.only.MCP, name)
}

// FilterSkills / FilterMCP：按角色限定筛（没限定原样返回）。
func (m *Manager) FilterSkills(in []Skill) []Skill {
	if !m.LimitsSkills() {
		return in
	}
	var out []Skill
	for _, s := range in {
		if m.AllowSkill(s.Name) {
			out = append(out, s)
		}
	}
	return out
}

func (m *Manager) FilterMCP(in []MCPServer) []MCPServer {
	if !m.LimitsMCP() {
		return in
	}
	var out []MCPServer
	for _, s := range in {
		if m.AllowMCP(s.Name) {
			out = append(out, s)
		}
	}
	return out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func (m *Manager) file() string      { return filepath.Join(m.dir, "ext.json") }
func (m *Manager) SkillsDir() string { return filepath.Join(m.dir, "skills") }

func (m *Manager) Settings() Settings {
	m.mu.Lock()
	defer m.mu.Unlock()
	var s Settings
	if b, err := os.ReadFile(m.file()); err == nil {
		_ = json.Unmarshal(b, &s)
	}
	if !s.MCPSource.valid() {
		s.MCPSource = SourceAgent
	}
	if !s.SkillSource.valid() {
		s.SkillSource = SourceAgent
	}
	if s.MCP == nil {
		s.MCP = []MCPServer{}
	}
	if s.DisabledSkills == nil {
		s.DisabledSkills = []string{}
	}
	return s
}

func (m *Manager) Save(s Settings) error {
	if !s.MCPSource.valid() || !s.SkillSource.valid() {
		return errors.New("来源只能是 agent / both / gerdr")
	}
	seen := map[string]bool{}
	for _, v := range s.MCP {
		if !ValidName(v.Name) {
			return fmt.Errorf("MCP 名字 %q 不合法：只能用字母、数字、- 和 _", v.Name)
		}
		if seen[v.Name] {
			return fmt.Errorf("MCP 名字 %q 重复了", v.Name)
		}
		seen[v.Name] = true
		if v.Remote() {
			if v.URL == "" {
				return fmt.Errorf("MCP %q 缺 URL", v.Name)
			}
		} else if v.Command == "" {
			return fmt.Errorf("MCP %q 缺命令", v.Name)
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	b, _ := json.MarshalIndent(s, "", "  ")
	if err := os.MkdirAll(m.dir, 0o700); err != nil {
		return err
	}
	tmp := m.file() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, m.file())
}

// Fingerprint：设置 + gerdr skill 目录的样子。claude 的进程拿它判断要不要换（参数只能启动时给）。
func (m *Manager) Fingerprint() string {
	s := m.Settings()
	h := sha256.New()
	_ = json.NewEncoder(h).Encode(s)
	for _, sk := range m.Builtin() {
		fmt.Fprintln(h, "builtin", sk.Name, sk.Enabled)
	}
	if s.SkillSource != SourceAgent {
		for _, sk := range m.Skills() {
			fmt.Fprintln(h, sk.Name, sk.Enabled)
		}
	}
	if m.only != nil {
		_ = json.NewEncoder(h).Encode(m.only)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// ---- gerdr 自己的 skill ----

func (m *Manager) Skills() []Skill {
	dis := map[string]bool{}
	for _, n := range m.Settings().DisabledSkills {
		dis[n] = true
	}
	out := scanSkills(m.SkillsDir(), "gerdr")
	for i := range out {
		out[i].Enabled = !dis[out[i].Name]
	}
	return out
}

func (m *Manager) skillPath(name string) (string, error) {
	if !ValidName(name) {
		return "", fmt.Errorf("skill 名字 %q 不合法：只能用字母、数字、- 和 _", name)
	}
	return filepath.Join(m.SkillsDir(), name, "SKILL.md"), nil
}

func (m *Manager) ReadSkill(name string) (string, error) {
	p, err := m.skillPath(name)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(p)
	return string(b), err
}

func (m *Manager) WriteSkill(name, content string) error {
	p, err := m.skillPath(name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(content), 0o644)
}

func (m *Manager) DeleteSkill(name string) error {
	p, err := m.skillPath(name)
	if err != nil {
		return err
	}
	return os.RemoveAll(filepath.Dir(p))
}

// ImportSkill 把 agent 那边的一个 skill（整个目录，脚本和参考文件一起）拷进 gerdr。
// src 必须是 AgentSkills 列出来的某一个 —— 调用方核过，这里不信任意路径。
func (m *Manager) ImportSkill(src Skill) error {
	if !ValidName(src.Name) {
		return fmt.Errorf("skill 名字 %q 不合法", src.Name)
	}
	dst := filepath.Join(m.SkillsDir(), src.Name)
	if _, err := os.Stat(dst); err == nil {
		return fmt.Errorf("gerdr 里已经有一个叫 %s 的 skill 了", src.Name)
	}
	return copyDir(src.Dir, dst)
}

// ClaudeAddDir：给 claude `--add-dir` 的目录。它要的布局是 <dir>/.claude/skills/<名>/SKILL.md，
// 所以做一个壳：.claude/skills 下只放这次要给它的那几个 skill 的符号链接（内置的 + 用得上 gerdr 的时候
// gerdr 自己那份里开着的）。关掉的不链进来，用不着 skillOverrides。
// --add-dir 同时会放开这个目录的文件权限，所以别直接给 GERDR_DIR —— 那里有令牌。
// 每个会话一个壳（id）：同时起的两个 claude 别互相改对方正在读的目录。
func (m *Manager) ClaudeAddDir(id string, includeGerdr bool) (string, error) {
	shell := filepath.Join(m.dir, "run", "claude-skills", id)
	skills := filepath.Join(shell, ".claude", "skills")
	_ = os.RemoveAll(skills)
	if err := os.MkdirAll(skills, 0o755); err != nil {
		return "", err
	}
	list := m.Builtin()
	if includeGerdr {
		list = append(list, m.Skills()...)
	}
	list = m.FilterSkills(list)
	n := 0
	for _, sk := range list {
		if !sk.Enabled {
			continue
		}
		if err := os.Symlink(sk.Dir, filepath.Join(skills, filepath.Base(sk.Dir))); err == nil {
			n++
		}
	}
	if n == 0 {
		return "", nil
	}
	return shell, nil
}

// ---- 内置 skill ----

//go:embed all:builtin
var builtinFS embed.FS

func (m *Manager) BuiltinDir() string { return filepath.Join(m.dir, "builtin-skills") }

// installBuiltin 把内置 skill 写到 <dir>/builtin-skills（每次启动覆盖：内容是 gerdr 的，不是用户的；
// 想改就导入到 gerdr 自己那份里改、把内置的关掉）。
func (m *Manager) installBuiltin() {
	m.builtinOnce.Do(func() {
		dst := m.BuiltinDir()
		_ = os.RemoveAll(dst)
		_ = fs.WalkDir(builtinFS, "builtin", func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			b, err := builtinFS.ReadFile(p)
			if err != nil {
				return err
			}
			t := filepath.Join(dst, filepath.FromSlash(strings.TrimPrefix(p, "builtin/")))
			_ = os.MkdirAll(filepath.Dir(t), 0o755)
			return os.WriteFile(t, b, 0o644)
		})
	})
}

// Builtin：gerdr 内置的 skill（多会话协作、改界面）。不管来源选的是什么都给 agent，除非在设置里关掉。
func (m *Manager) Builtin() []Skill {
	m.installBuiltin()
	dis := map[string]bool{}
	for _, n := range m.Settings().DisabledSkills {
		dis[n] = true
	}
	out := scanSkills(m.BuiltinDir(), "builtin")
	for i := range out {
		out[i].Enabled = !dis[out[i].Name]
	}
	return out
}

// ---- 扫 skill ----

func scanSkills(root, origin string) []Skill {
	var out []Skill
	ents, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	for _, e := range ents {
		dir := filepath.Join(root, e.Name())
		if st, err := os.Stat(dir); err != nil || !st.IsDir() { // 跟符号链接
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
		if err != nil {
			continue
		}
		name, desc := frontmatter(string(b))
		if name == "" {
			name = e.Name()
		}
		out = append(out, Skill{Name: name, Description: desc, Dir: dir, Enabled: true, Origin: origin})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// frontmatter 读 SKILL.md 开头 --- 之间的 name / description。description 可以是多行的
// （`description: >` / `|` 后面跟缩进的几行，或者直接换行接着写），拼成一行。
func frontmatter(s string) (name, desc string) {
	s = strings.TrimPrefix(s, "\ufeff")
	if !strings.HasPrefix(s, "---") {
		return "", ""
	}
	rest := s[3:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", ""
	}
	lines := strings.Split(rest[:end], "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		k, v, ok := strings.Cut(line, ":")
		if !ok || strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue
		}
		v = strings.TrimSpace(v)
		switch strings.TrimSpace(k) {
		case "name":
			name = strings.Trim(v, `"'`)
		case "description":
			if v == "" || v == "|" || v == ">" || v == ">-" || v == "|-" {
				var parts []string
				for i+1 < len(lines) && (strings.HasPrefix(lines[i+1], " ") || strings.HasPrefix(lines[i+1], "\t")) {
					i++
					parts = append(parts, strings.TrimSpace(lines[i]))
				}
				desc = strings.Join(parts, " ")
			} else {
				desc = strings.Trim(v, `"'`)
			}
		}
	}
	return name, desc
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		t := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(t, 0o755)
		}
		if d.Type()&fs.ModeSymlink != 0 {
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(target, t)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		info, _ := d.Info()
		mode := fs.FileMode(0o644)
		if info != nil {
			mode = info.Mode().Perm()
		}
		return os.WriteFile(t, b, mode)
	})
}
