package main

// gerdr mcp / gerdr skill: manage gerdr's own MCP servers and skills (Settings → Agent → Skill / MCP) from the
// command line, so an agent can set up a new agent end to end: add the skill / MCP here, then the agent itself
// with `gerdr config add agents …`.
//
// Like `gerdr config`, these write <GERDR_DIR>/ext.json and <GERDR_DIR>/skills directly (works with gerdr stopped).
// The backend reads both fresh for every new agent process, and its ext.Manager.Watch tells open Settings pages
// to reload. Claude Code's / Codex's own skills and MCP are only read here, never written (same rule as the UI).

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/gerdr-dev/gerdr/internal/config"
	"github.com/gerdr-dev/gerdr/internal/ext"
)

func openExt() *ext.Manager { return ext.New(config.FromEnv().Dir) }

func parseSource(s string) (ext.Source, error) {
	switch ext.Source(s) {
	case ext.SourceAgent, ext.SourceBoth, ext.SourceGerdr:
		return ext.Source(s), nil
	}
	return "", fmt.Errorf("source is agent (only the engine's own), both, or gerdr (only gerdr's), not %q", s)
}

const sourceHelp = `Where engines get them from: agent = only Claude Code's / Codex's own (the default, same as in a terminal),
both = theirs plus gerdr's, gerdr = only gerdr's. The native engine (Gerdr Agent) always uses gerdr's MCP.`

// ---- MCP ----

// mcpView is an MCP server as printed: env / header values hidden, they are usually tokens and whatever is
// printed ends up in the agent's transcript.
type mcpView struct {
	Name    string            `json:"name"`
	Type    string            `json:"type,omitempty"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Enabled bool              `json:"enabled"`
}

func hideValues(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := map[string]string{}
	for k := range m {
		out[k] = "<hidden>"
	}
	return out
}

func viewMCP(list []ext.MCPServer) []mcpView {
	out := []mcpView{}
	for _, s := range list {
		out = append(out, mcpView{s.Name, s.Type, s.Command, s.Args, hideValues(s.Env), s.URL, hideValues(s.Headers), s.Enabled})
	}
	return out
}

func mcpNames(list []ext.MCPServer, enabledOnly bool) []string {
	out := []string{}
	for _, s := range list {
		if !enabledOnly || s.Enabled {
			out = append(out, s.Name)
		}
	}
	return out
}

// mcpEngines: the MCP names each engine gets under the global settings, i.e. what an agent without an "mcp" list
// gets. One with a list may name any switched-on server, gerdr's or the engine's own (ext.GerdrMCPOn). Mirrors
// candidates() in ui/src/components/AgentSettings.tsx.
func mcpEngines(st ext.Settings) map[string][]string {
	gerdr := mcpNames(st.MCP, true)
	out := map[string][]string{"native": gerdr}
	for id, own := range map[string][]ext.MCPServer{"claude": ext.ClaudeMCP(""), "codex": ext.CodexMCP()} {
		var names []string
		if st.MCPSource != ext.SourceAgent {
			names = append(names, gerdr...)
		}
		if st.MCPSource != ext.SourceGerdr {
			for _, n := range mcpNames(own, true) {
				if !slices.Contains(names, n) {
					names = append(names, n)
				}
			}
		}
		out[id] = append([]string{}, names...)
	}
	return out
}

// givenTo: the engines that get name by default under the current settings (agents that list names get theirs).
func givenTo(engines map[string][]string, name string) []string {
	out := []string{}
	for _, id := range []string{"claude", "codex", "native"} {
		if slices.Contains(engines[id], name) {
			out = append(out, id)
		}
	}
	return out
}

func findMCP(list []ext.MCPServer, name string) int {
	return slices.IndexFunc(list, func(s ext.MCPServer) bool { return s.Name == name })
}

func kvFlags(list []string, what string) (map[string]string, error) {
	if len(list) == 0 {
		return nil, nil
	}
	out := map[string]string{}
	for _, kv := range list {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("--%s takes KEY=VALUE, not %q", what, kv)
		}
		out[k] = v
	}
	return out, nil
}

func mcpCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Manage gerdr's own MCP servers (Settings → Agent → MCP)",
		Long: `Manage gerdr's own MCP servers (Settings → Agent → MCP); open windows update within seconds.

An agent (gerdr config keys agents) can list MCP servers by name: any switched-on one, gerdr's or its engine's
own, whatever the source. An agent without a list gets what the source gives ("default" in ` + "`gerdr mcp list`" + `).
To give an agent a new server: add it here, then put its name in agents.<id>.mcp.

` + sourceHelp + `

  gerdr mcp list
  gerdr mcp add github --url https://api.githubcopilot.com/mcp/ --header "Authorization=Bearer $TOKEN"
  gerdr mcp add fs -- npx -y @modelcontextprotocol/server-filesystem /tmp
  gerdr mcp import linear          # copy one from Claude Code's / Codex's own config
  gerdr mcp disable fs
  gerdr mcp source both`,
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "Print gerdr's MCP servers, Claude Code's and Codex's own, the source, and what each engine gets by default",
		Args:  cobra.NoArgs,
		Run: func(_ *cobra.Command, _ []string) {
			st := openExt().Settings()
			printJSON(map[string]any{
				"source":  st.MCPSource,
				"servers": viewMCP(st.MCP),
				"claude":  viewMCP(ext.ClaudeMCP("")),
				"codex":   viewMCP(ext.CodexMCP()),
				"default": mcpEngines(st),
			})
		},
	})

	var url, typ string
	var envs, headers []string
	var disabled, replace bool
	add := &cobra.Command{
		Use:   "add <name> [--url URL | -- command args…]",
		Short: "Add an MCP server to gerdr's list (stdio: the command after --; remote: --url)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(c *cobra.Command, a []string) error {
			s := ext.MCPServer{Name: a[0], URL: url, Enabled: !disabled}
			var err error
			if s.Env, err = kvFlags(envs, "env"); err != nil {
				return err
			}
			if s.Headers, err = kvFlags(headers, "header"); err != nil {
				return err
			}
			rest := a[1:]
			if dash := c.ArgsLenAtDash(); dash >= 0 {
				rest = a[dash:]
				if dash != 1 {
					return fmt.Errorf("one name, then -- and the command")
				}
			}
			switch {
			case url != "" && len(rest) > 0:
				return fmt.Errorf("either --url or a command, not both")
			case url != "":
				s.Type = "http"
				if typ != "" {
					s.Type = typ
				}
				if s.Type != "http" && s.Type != "sse" {
					return fmt.Errorf("--type is http or sse for a remote server")
				}
			case len(rest) > 0:
				s.Type, s.Command, s.Args = "stdio", rest[0], rest[1:]
			default:
				return fmt.Errorf("give --url, or the command after --: gerdr mcp add %s -- npx -y some-mcp-server", a[0])
			}
			return addMCP(s, replace)
		},
	}
	add.Flags().StringVar(&url, "url", "", "remote server URL")
	add.Flags().StringVar(&typ, "type", "", "remote transport: http (default) or sse")
	add.Flags().StringArrayVar(&envs, "env", nil, "KEY=VALUE environment variable for a stdio server (repeatable)")
	add.Flags().StringArrayVar(&headers, "header", nil, "KEY=VALUE HTTP header for a remote server (repeatable)")
	add.Flags().BoolVar(&disabled, "disabled", false, "add it switched off")
	add.Flags().BoolVar(&replace, "replace", false, "overwrite a server with the same name")
	cmd.AddCommand(add)

	var importReplace bool
	imp := &cobra.Command{
		Use:   "import <name>",
		Short: "Copy an MCP server from Claude Code's / Codex's own config into gerdr's list",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			for _, list := range [][]ext.MCPServer{ext.ClaudeMCP(""), ext.CodexMCP()} {
				if i := findMCP(list, a[0]); i >= 0 {
					s := list[i]
					if s.Remote() && s.Type == "" {
						s.Type = "http"
					} else if !s.Remote() {
						s.Type = "stdio"
					}
					s.Enabled = true
					return addMCP(s, importReplace)
				}
			}
			return fmt.Errorf("neither Claude Code nor Codex has an MCP server named %q (gerdr mcp list)", a[0])
		},
	}
	imp.Flags().BoolVar(&importReplace, "replace", false, "overwrite a server with the same name")
	cmd.AddCommand(imp)

	cmd.AddCommand(&cobra.Command{
		Use:   "rm <name>",
		Short: "Remove an MCP server from gerdr's list",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			return editMCP(a[0], func(st *ext.Settings, i int) { st.MCP = slices.Delete(st.MCP, i, i+1) })
		},
	})
	for _, on := range []bool{true, false} {
		use := map[bool]string{true: "enable", false: "disable"}[on]
		cmd.AddCommand(&cobra.Command{
			Use:   use + " <name>",
			Short: map[bool]string{true: "Switch one of gerdr's MCP servers on", false: "Switch one of gerdr's MCP servers off (kept in the list)"}[on],
			Args:  cobra.ExactArgs(1),
			RunE: func(_ *cobra.Command, a []string) error {
				return editMCP(a[0], func(st *ext.Settings, i int) { st.MCP[i].Enabled = on })
			},
		})
	}
	cmd.AddCommand(sourceCmd("MCP", func(st *ext.Settings) *ext.Source { return &st.MCPSource }))
	return cmd
}

func addMCP(s ext.MCPServer, replace bool) error {
	m := openExt()
	st := m.Settings()
	if i := findMCP(st.MCP, s.Name); i >= 0 {
		if !replace {
			return fmt.Errorf("gerdr already has an MCP server named %s (--replace to overwrite it)", s.Name)
		}
		st.MCP[i] = s
	} else {
		st.MCP = append(st.MCP, s)
	}
	if err := m.Save(st); err != nil {
		return err
	}
	printJSON(map[string]any{"ok": true, "server": viewMCP([]ext.MCPServer{s})[0], "defaultFor": givenTo(mcpEngines(st), s.Name)})
	if st.MCPSource == ext.SourceAgent {
		fmt.Fprintln(os.Stderr, "note: MCP source is \"agent\": Claude Code / Codex sessions get it only through an agent that lists it in its mcp (or after `gerdr mcp source both`)")
	}
	return nil
}

func editMCP(name string, fn func(st *ext.Settings, i int)) error {
	m := openExt()
	st := m.Settings()
	i := findMCP(st.MCP, name)
	if i < 0 {
		return fmt.Errorf("gerdr has no MCP server named %q (gerdr mcp list)", name)
	}
	fn(&st, i)
	if err := m.Save(st); err != nil {
		return err
	}
	printJSON(map[string]any{"ok": true, "servers": viewMCP(st.MCP)})
	return nil
}

func sourceCmd(what string, field func(*ext.Settings) *ext.Source) *cobra.Command {
	return &cobra.Command{
		Use:   "source [agent|both|gerdr]",
		Short: "Print or set where engines get their " + what + " from",
		Long:  sourceHelp,
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			m := openExt()
			st := m.Settings()
			if len(a) == 0 {
				fmt.Println(*field(&st))
				return nil
			}
			src, err := parseSource(a[0])
			if err != nil {
				return err
			}
			*field(&st) = src
			if err := m.Save(st); err != nil {
				return err
			}
			printJSON(map[string]any{"ok": true, "source": src})
			return nil
		},
	}
}

// ---- skills ----

type skillView struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
	Dir         string `json:"dir"`
}

func viewSkills(list []ext.Skill) []skillView {
	out := []skillView{}
	for _, s := range list {
		out = append(out, skillView{s.Name, s.Description, s.Enabled, s.Dir})
	}
	return out
}

// skillEngines: the skill names each engine gets under the global settings (what an agent without a "skills" list
// gets; one with a list may name any switched-on skill). Mirrors candidates() in ui/src/components/AgentSettings.tsx;
// the native engine's own skills are Claude Code's.
func skillEngines(m *ext.Manager, st ext.Settings) map[string][]string {
	out := map[string][]string{}
	for id, own := range map[string][]ext.Skill{"claude": ext.ClaudeSkills(""), "native": ext.ClaudeSkills(""), "codex": ext.CodexSkills()} {
		names := []string{}
		add := func(n string) {
			if !slices.Contains(names, n) {
				names = append(names, n)
			}
		}
		for _, s := range m.InstalledBuiltin() {
			if s.Enabled {
				add(s.Name)
			}
		}
		if st.SkillSource != ext.SourceAgent {
			for _, s := range m.Skills() {
				if s.Enabled {
					add(s.Name)
				}
			}
		}
		if st.SkillSource != ext.SourceGerdr {
			for _, s := range own {
				add(s.Name)
			}
		}
		out[id] = names
	}
	return out
}

func skillCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skill",
		Short: "Manage gerdr's own skills (Settings → Agent → Skill)",
		Long: `Manage gerdr's own skills (Settings → Agent → Skill); open windows update within seconds.

A skill is a folder with a SKILL.md (front matter with name and description, then the instructions), stored
under <GERDR_DIR>/skills/<name>. An agent (gerdr config keys agents) can list skills by name: any switched-on one
(built-in, gerdr's, or its engine's own), whatever the source. An agent without a list gets what the source gives
("default" in ` + "`gerdr skill list`" + `).

` + sourceHelp + `

  gerdr skill list
  gerdr skill add code-review --file ./SKILL.md     # or --dir ./code-review (with its scripts), or SKILL.md on stdin
  gerdr skill import pdf                            # copy one of Claude Code's / Codex's own skills
  gerdr skill disable gerdr-ui                      # built-in ones can be switched off too
  gerdr skill source both`,
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "Print the skills (built-in, gerdr's, Claude Code's, Codex's), the source, and what each engine gets by default",
		Args:  cobra.NoArgs,
		Run: func(_ *cobra.Command, _ []string) {
			m := openExt()
			st := m.Settings()
			printJSON(map[string]any{
				"source":  st.SkillSource,
				"builtin": viewSkills(m.InstalledBuiltin()),
				"gerdr":   viewSkills(m.Skills()),
				"claude":  viewSkills(ext.ClaudeSkills("")),
				"codex":   viewSkills(ext.CodexSkills()),
				"default": skillEngines(m, st),
			})
		},
	})

	var file, dir string
	var replace bool
	add := &cobra.Command{
		Use:   "add <name> [--file SKILL.md | --dir folder]",
		Short: "Add (or with --replace, overwrite) one of gerdr's skills; without --file / --dir SKILL.md is read from stdin",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			m := openExt()
			name := a[0]
			if dir != "" {
				if file != "" {
					return fmt.Errorf("either --file or --dir")
				}
				b, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
				if err != nil {
					return fmt.Errorf("%s has no SKILL.md", dir)
				}
				if err := checkSkillMD(string(b)); err != nil {
					return err
				}
				if err := m.AddSkillDir(name, dir, replace); err != nil {
					return err
				}
			} else {
				var b []byte
				var err error
				if file != "" {
					b, err = os.ReadFile(file)
				} else {
					b, err = io.ReadAll(os.Stdin)
				}
				if err != nil {
					return err
				}
				if err := checkSkillMD(string(b)); err != nil {
					return err
				}
				if _, err := m.ReadSkill(name); err == nil && !replace {
					return fmt.Errorf("gerdr already has a skill named %s (--replace to overwrite it)", name)
				}
				if err := m.WriteSkill(name, string(b)); err != nil {
					return err
				}
			}
			return printSkillAdded(m, name)
		},
	}
	add.Flags().StringVar(&file, "file", "", "the SKILL.md to store")
	add.Flags().StringVar(&dir, "dir", "", "a skill folder to copy whole (SKILL.md plus scripts / references)")
	add.Flags().BoolVar(&replace, "replace", false, "overwrite a skill with the same name")
	cmd.AddCommand(add)

	var importReplace bool
	imp := &cobra.Command{
		Use:   "import <name>",
		Short: "Copy one of Claude Code's / Codex's own skills (the whole folder) into gerdr's",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			m := openExt()
			for _, list := range [][]ext.Skill{ext.ClaudeSkills(""), ext.CodexSkills()} {
				for _, s := range list {
					if s.Name != a[0] {
						continue
					}
					// a plugin's skill is listed as "plugin:name"; in gerdr it takes its folder's name
					name := s.Name
					if !ext.ValidName(name) {
						name = filepath.Base(s.Dir)
					}
					if err := m.AddSkillDir(name, s.Dir, importReplace); err != nil {
						return err
					}
					return printSkillAdded(m, name)
				}
			}
			return fmt.Errorf("neither Claude Code nor Codex has a skill named %q (gerdr skill list)", a[0])
		},
	}
	imp.Flags().BoolVar(&importReplace, "replace", false, "overwrite a skill with the same name")
	cmd.AddCommand(imp)

	cmd.AddCommand(&cobra.Command{
		Use:   "rm <name>",
		Short: "Delete one of gerdr's skills (its whole folder)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			m := openExt()
			if _, err := m.ReadSkill(a[0]); err != nil {
				return fmt.Errorf("gerdr has no skill named %q (built-in ones can only be disabled)", a[0])
			}
			if err := m.DeleteSkill(a[0]); err != nil {
				return err
			}
			st := m.Settings()
			st.DisabledSkills = slices.DeleteFunc(st.DisabledSkills, func(n string) bool { return n == a[0] })
			if err := m.Save(st); err != nil {
				return err
			}
			printJSON(map[string]any{"ok": true, "removed": a[0]})
			return nil
		},
	})
	for _, on := range []bool{true, false} {
		use := map[bool]string{true: "enable", false: "disable"}[on]
		cmd.AddCommand(&cobra.Command{
			Use:   use + " <name>",
			Short: map[bool]string{true: "Switch a built-in or gerdr skill on", false: "Switch a built-in or gerdr skill off (the files stay)"}[on],
			Args:  cobra.ExactArgs(1),
			RunE: func(_ *cobra.Command, a []string) error {
				m := openExt()
				known := slices.ContainsFunc(append(m.InstalledBuiltin(), m.Skills()...), func(s ext.Skill) bool { return s.Name == a[0] })
				if !known {
					return fmt.Errorf("no built-in or gerdr skill named %q (Claude Code's / Codex's own are switched in their own settings)", a[0])
				}
				st := m.Settings()
				st.DisabledSkills = slices.DeleteFunc(st.DisabledSkills, func(n string) bool { return n == a[0] })
				if !on {
					st.DisabledSkills = append(st.DisabledSkills, a[0])
				}
				if err := m.Save(st); err != nil {
					return err
				}
				printJSON(map[string]any{"ok": true, "name": a[0], "enabled": on})
				return nil
			},
		})
	}
	cmd.AddCommand(sourceCmd("skills", func(st *ext.Settings) *ext.Source { return &st.SkillSource }))
	return cmd
}

// checkSkillMD: an engine only offers a skill whose front matter has a description (that's what it reads to
// decide when to use it), so refuse one without instead of storing a skill that never gets used.
func checkSkillMD(s string) error {
	if _, desc := ext.Frontmatter(s); desc == "" {
		return fmt.Errorf("SKILL.md needs front matter with a description:\n---\nname: my-skill\ndescription: what it does and when to use it\n---\n…instructions…")
	}
	return nil
}

func printSkillAdded(m *ext.Manager, name string) error {
	st := m.Settings()
	printJSON(map[string]any{"ok": true, "name": name, "dir": filepath.Join(m.SkillsDir(), name), "defaultFor": givenTo(skillEngines(m, st), name)})
	if st.SkillSource == ext.SourceAgent {
		fmt.Fprintln(os.Stderr, "note: skill source is \"agent\": sessions get it only through an agent that lists it in its skills (or after `gerdr skill source both`)")
	}
	return nil
}
