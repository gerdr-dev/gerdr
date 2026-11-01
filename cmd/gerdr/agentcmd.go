package main

// gerdr agent create / show / rm: one step for a whole agent (role) — the agent itself (config.json agents), the
// skills and MCP servers it brings (into gerdr's pool, <GERDR_DIR>/skills and ext.json), and its new-session
// defaults (defaultModes / defaultConfig). The storage stays a shared pool (skills and MCP servers are reused across
// agents, MCP OAuth is keyed by server name, the secrets live in one place); this only spares the caller the four
// separate edits and the half-done state when one of them fails. `show` prints an agent in the same shape, so its
// output can be fed back to `create`.

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/gerdr-dev/gerdr/internal/config"
	"github.com/gerdr-dev/gerdr/internal/ext"
)

const agentSpecHelp = `The spec is one JSON object: the agent's own fields (` + "`gerdr config keys agents`" + `) plus two optional keys:

  define    skills / MCP servers to add to gerdr's pool along with it:
              {"skills": [{"name": "code-review", "content": "---\nname: code-review\ndescription: …\n---\n…"},
                          {"name": "pdf-tools", "dir": "/path/to/a/skill/folder"}],
               "mcp":    [{"name": "github", "url": "https://…", "headers": {"Authorization": "Bearer …"}},
                          {"name": "fs", "command": "npx", "args": ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"]}]}
            Their names are added to the agent's skills / mcp list when it has one. One that already exists with
            the same content is left alone; with different content it is refused unless --replace.
  defaults  new sessions' defaults: {"mode": "plan", "config": {"model": "opus", "effort": "high"}}

  gerdr agent create '{"id": "reviewer", "name": "Reviewer", "base": "claude", "prompt": "Review only, don't edit.",
    "skills": ["code-review"], "mcp": [], "define": {"skills": [{"name": "code-review", "content": "…"}]},
    "defaults": {"mode": "plan"}}'
  gerdr agent create --file reviewer.json`

type skillDef struct {
	Name    string `json:"name"`
	Content string `json:"content,omitempty"`
	Dir     string `json:"dir,omitempty"`
}

// mcpDef: an MCPServer whose "enabled" defaults to true and whose type is inferred when left out.
type mcpDef struct {
	ext.MCPServer
	Enabled *bool `json:"enabled,omitempty"`
}

type agentDefaults struct {
	Mode   string            `json:"mode,omitempty"`
	Config map[string]string `json:"config,omitempty"`
}

type agentSpec struct {
	agent    map[string]any
	skills   []skillDef
	mcp      []ext.MCPServer
	defaults *agentDefaults
}

func parseAgentSpec(b []byte) (*agentSpec, error) {
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("the spec is not a JSON object: %w", err)
	}
	sp := &agentSpec{agent: raw}
	if d, ok := raw["define"]; ok {
		delete(raw, "define")
		var def struct {
			Skills []skillDef `json:"skills"`
			MCP    []mcpDef   `json:"mcp"`
		}
		if err := remarshal(d, &def); err != nil {
			return nil, fmt.Errorf("define: %w", err)
		}
		sp.skills = def.Skills
		for _, m := range def.MCP {
			s := m.MCPServer
			s.Enabled = m.Enabled == nil || *m.Enabled
			if s.Type == "" {
				if s.Remote() {
					s.Type = "http"
				} else {
					s.Type = "stdio"
				}
			}
			sp.mcp = append(sp.mcp, s)
		}
	}
	if d, ok := raw["defaults"]; ok {
		delete(raw, "defaults")
		sp.defaults = &agentDefaults{}
		if err := remarshal(d, sp.defaults); err != nil {
			return nil, fmt.Errorf("defaults: %w", err)
		}
	}
	return sp, nil
}

func anys(list []string) []any {
	out := make([]any, len(list))
	for i, s := range list {
		out[i] = s
	}
	return out
}

func remarshal(in, out any) error {
	b, _ := json.Marshal(in)
	return json.Unmarshal(b, out)
}

func readSpecArg(a []string, file string) ([]byte, error) {
	switch {
	case file != "" && len(a) > 0:
		return nil, fmt.Errorf("either the spec as an argument or --file")
	case file == "-" || (file == "" && len(a) == 0):
		return io.ReadAll(os.Stdin)
	case file != "":
		return os.ReadFile(file)
	}
	return []byte(a[0]), nil
}

// strList reads an agent's skills / mcp; ok is false when the field is absent (= follow the global settings).
func strList(m map[string]any, k string) (list []string, ok bool) {
	v, ok := m[k].([]any)
	if !ok {
		return nil, false
	}
	for _, x := range v {
		if s, isStr := x.(string); isStr {
			list = append(list, s)
		}
	}
	return list, true
}

func agentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Create, show or remove an agent (role) together with its skills, MCP servers and defaults",
		Long: `Create, show or remove an agent (role in Settings → Agent) together with its skills, MCP servers and
new-session defaults, in one step. Open windows update within seconds. List the agents with ` + "`gerdr session agents`" + `.

` + agentSpecHelp,
	}
	var file string
	var replace bool
	create := &cobra.Command{
		Use:   "create [spec]",
		Short: "Create an agent from a JSON spec (argument, --file, or stdin), adding the skills / MCP it defines",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			b, err := readSpecArg(a, file)
			if err != nil {
				return err
			}
			sp, err := parseAgentSpec(b)
			if err != nil {
				return err
			}
			return createAgent(sp, replace)
		},
	}
	create.Flags().StringVar(&file, "file", "", "read the spec from this file (- = stdin)")
	create.Flags().BoolVar(&replace, "replace", false, "replace an agent with the same id, and skills / MCP servers with the same name")
	cmd.AddCommand(create)

	cmd.AddCommand(&cobra.Command{
		Use:   "show <id>",
		Short: "Print an agent as a spec for `gerdr agent create`: its fields, defaults, and the gerdr skills / MCP it lists",
		Args:  cobra.ExactArgs(1),
		RunE:  func(_ *cobra.Command, a []string) error { return showAgent(a[0]) },
	})

	var prune bool
	rm := &cobra.Command{
		Use:   "rm <id>",
		Short: "Remove an agent and its new-session defaults",
		Args:  cobra.ExactArgs(1),
		RunE:  func(_ *cobra.Command, a []string) error { return removeAgent(a[0], prune) },
	}
	rm.Flags().BoolVar(&prune, "prune", false, "also delete gerdr skills / MCP servers it listed that no other agent lists (kept while the global source hands gerdr's to every session; note the Gerdr Agent engine always uses gerdr's MCP, so an agent on it without an mcp list loses a pruned server too)")
	cmd.AddCommand(rm)
	return cmd
}

func createAgent(sp *agentSpec, replace bool) error {
	id, _ := sp.agent["id"].(string)
	if id == "" {
		return fmt.Errorf("the spec needs an id")
	}
	// The defined skills / MCP go into the agent's own lists when it has them; without a list it follows the
	// global source, which may not hand them over.
	var notes []string
	for _, f := range []struct {
		key   string
		names []string
	}{{"skills", defNames(sp.skills)}, {"mcp", mcpNamesOf(sp.mcp)}} {
		if len(f.names) == 0 {
			continue
		}
		if list, ok := strList(sp.agent, f.key); ok {
			for _, n := range f.names {
				if !slices.Contains(list, n) {
					list = append(list, n)
				}
			}
			sp.agent[f.key] = anys(list) // as decoded JSON, which checkAgents and getPath expect
		} else {
			notes = append(notes, fmt.Sprintf("the agent has no %q list, so it gets the defined ones only if the global source includes gerdr's", f.key))
		}
	}

	// Plan everything before writing anything.
	st, sec := openConfig()
	var root any
	if err := json.Unmarshal(st.Get(), &root); err != nil {
		return fmt.Errorf("config.json: %w", err)
	}
	cur, _ := getPath(root, []string{"agents"})
	agents, _ := cur.([]any)
	if i := index(agents, id); i >= 0 {
		if !replace {
			return fmt.Errorf("an agent with id %q exists (--replace to overwrite it, or `gerdr config set agents.%s.<field>`)", id, id)
		}
		agents[i] = sp.agent
	} else {
		agents = append(agents, sp.agent)
	}
	var err error
	if root, err = setPath(root, []string{"agents"}, agents, false); err != nil {
		return err
	}
	if d := sp.defaults; d != nil {
		if d.Mode != "" {
			root, _ = setPath(root, []string{"defaultModes", id}, d.Mode, false)
		}
		if len(d.Config) > 0 {
			cfg := map[string]any{}
			for k, v := range d.Config {
				cfg[k] = v
			}
			root, _ = setPath(root, []string{"defaultConfig", id}, cfg, false)
		}
	}
	if err := checkAgents(root); err != nil {
		return err
	}

	em := openExt()
	oldExt := em.Settings()
	newExt := oldExt
	newExt.MCP = slices.Clone(oldExt.MCP)
	var addedMCP, keptMCP []string
	for _, s := range sp.mcp {
		i := findMCP(newExt.MCP, s.Name)
		if i >= 0 {
			if err := fillHidden(&s, newExt.MCP[i]); err != nil {
				return err
			}
			if sameMCP(s, newExt.MCP[i]) {
				keptMCP = append(keptMCP, s.Name)
				continue
			}
			if !replace {
				return fmt.Errorf("gerdr already has a different MCP server named %s (--replace to overwrite it, or pick another name)", s.Name)
			}
			newExt.MCP[i] = s
		} else {
			if err := fillHidden(&s, ext.MCPServer{}); err != nil {
				return err
			}
			newExt.MCP = append(newExt.MCP, s)
		}
		addedMCP = append(addedMCP, s.Name)
	}
	if err := newExt.Validate(); err != nil {
		return err
	}
	type skillPlan struct {
		def     skillDef
		replace bool
	}
	var plans []skillPlan
	var keptSkills []string
	for _, d := range sp.skills {
		if !ext.ValidName(d.Name) {
			return fmt.Errorf("skill name %q: letters, digits, - and _ only", d.Name)
		}
		if (d.Content == "") == (d.Dir == "") {
			return fmt.Errorf("skill %s: give either content (the SKILL.md) or dir (a skill folder)", d.Name)
		}
		md := d.Content
		if d.Dir != "" {
			b, err := os.ReadFile(filepath.Join(d.Dir, "SKILL.md"))
			if err != nil {
				return fmt.Errorf("skill %s: %s has no SKILL.md", d.Name, d.Dir)
			}
			md = string(b)
		}
		if err := checkSkillMD(md); err != nil {
			return fmt.Errorf("skill %s: %w", d.Name, err)
		}
		if old, err := em.ReadSkill(d.Name); err == nil {
			if old == md {
				keptSkills = append(keptSkills, d.Name)
				continue
			}
			if !replace {
				return fmt.Errorf("gerdr already has a different skill named %s (--replace to overwrite it, or pick another name)", d.Name)
			}
			plans = append(plans, skillPlan{d, true})
		} else {
			plans = append(plans, skillPlan{d, false})
		}
	}

	// Write: skills, then ext.json, then config.json; undo the earlier steps when a later one fails.
	backup, err := os.MkdirTemp(config.FromEnv().Dir, ".agent-create-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(backup)
	var done []skillPlan
	undoSkills := func() {
		for _, p := range done {
			_ = em.DeleteSkill(p.def.Name)
			if p.replace {
				_ = os.Rename(filepath.Join(backup, p.def.Name), filepath.Join(em.SkillsDir(), p.def.Name))
			}
		}
	}
	for _, p := range plans {
		if p.replace {
			if err := os.Rename(filepath.Join(em.SkillsDir(), p.def.Name), filepath.Join(backup, p.def.Name)); err != nil {
				undoSkills()
				return err
			}
		}
		done = append(done, p)
		if p.def.Dir != "" {
			err = em.AddSkillDir(p.def.Name, p.def.Dir, false)
		} else {
			err = em.WriteSkill(p.def.Name, p.def.Content)
		}
		if err != nil {
			undoSkills()
			return fmt.Errorf("skill %s: %w", p.def.Name, err)
		}
	}
	if len(addedMCP) > 0 {
		if err := em.Save(newExt); err != nil {
			undoSkills()
			return err
		}
	}
	if err := saveConfig(st, sec, root, true); err != nil {
		if len(addedMCP) > 0 {
			_ = em.Save(oldExt)
		}
		undoSkills()
		return err
	}

	addedSkills := []string{}
	for _, p := range plans {
		addedSkills = append(addedSkills, p.def.Name)
	}
	out := map[string]any{
		"ok":        true,
		"agent":     sp.agent,
		"added":     map[string]any{"skills": addedSkills, "mcp": nz(addedMCP)},
		"unchanged": map[string]any{"skills": nz(keptSkills), "mcp": nz(keptMCP)},
	}
	if sp.defaults != nil {
		out["defaults"] = sp.defaults
	}
	printJSON(out)
	for _, n := range append(notes, nameWarnings(em, sp.agent)...) {
		fmt.Fprintln(os.Stderr, "note:", n)
	}
	return nil
}

// fillHidden: a spec printed by `gerdr agent show` has env / header values as <hidden>. Put the pool's real value
// back when it has one under that key; refuse otherwise, rather than storing the placeholder as a token.
func fillHidden(s *ext.MCPServer, have ext.MCPServer) error {
	for _, f := range []struct {
		what      string
		dst, from map[string]string
	}{{"env", s.Env, have.Env}, {"header", s.Headers, have.Headers}} {
		for k, v := range f.dst {
			if v != "<hidden>" {
				continue
			}
			real, ok := f.from[k]
			if !ok {
				return fmt.Errorf("MCP %s: %s %s is <hidden>; put the real value in the spec", s.Name, f.what, k)
			}
			f.dst[k] = real
		}
	}
	return nil
}

func sameMCP(a, b ext.MCPServer) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

func defNames(d []skillDef) []string {
	var out []string
	for _, s := range d {
		out = append(out, s.Name)
	}
	return out
}

func mcpNamesOf(list []ext.MCPServer) []string {
	var out []string
	for _, s := range list {
		out = append(out, s.Name)
	}
	return out
}

func nz(a []string) []string {
	if a == nil {
		return []string{}
	}
	return a
}

// nameWarnings: names in the agent's skills / mcp that nothing provides, or that are switched off (never given).
func nameWarnings(em *ext.Manager, agent map[string]any) []string {
	var out []string
	if list, ok := strList(agent, "skills"); ok {
		on, off := map[string]bool{}, map[string]bool{}
		for _, s := range append(em.InstalledBuiltin(), em.Skills()...) {
			if s.Enabled {
				on[s.Name] = true
			} else {
				off[s.Name] = true
			}
		}
		for _, s := range append(ext.ClaudeSkills(""), ext.CodexSkills()...) {
			on[s.Name] = true
		}
		for _, n := range list {
			if off[n] && !on[n] {
				out = append(out, fmt.Sprintf("skill %s is switched off, so it isn't given (gerdr skill enable %s)", n, n))
			} else if !on[n] {
				out = append(out, fmt.Sprintf("no skill named %s (gerdr skill list); it is ignored until one exists", n))
			}
		}
	}
	if list, ok := strList(agent, "mcp"); ok {
		seen, on := map[string]bool{}, map[string]bool{}
		for _, lst := range [][]ext.MCPServer{em.Settings().MCP, ext.ClaudeMCP(""), ext.CodexMCP()} {
			for _, s := range lst {
				seen[s.Name] = true
				on[s.Name] = on[s.Name] || s.Enabled
			}
		}
		for _, n := range list {
			switch {
			case !seen[n]:
				out = append(out, fmt.Sprintf("no MCP server named %s (gerdr mcp list); it is ignored until one exists", n))
			case !on[n]:
				out = append(out, fmt.Sprintf("MCP server %s is switched off, so it isn't given (gerdr mcp enable %s)", n, n))
			}
		}
	}
	return out
}

func showAgent(id string) error {
	st, _ := openConfig()
	root := mustDecode(st.Get())
	out := map[string]any{}
	if id == "claude" || id == "codex" || id == "native" {
		out["id"] = id
		out["builtin"] = true
	} else {
		v, ok := getPath(root, []string{"agents", id})
		if !ok {
			return fmt.Errorf("no agent with id %q (gerdr session agents)", id)
		}
		m, _ := v.(map[string]any)
		maps.Copy(out, m)
		em := openExt()
		def := map[string]any{}
		if list, ok := strList(m, "skills"); ok {
			var skills []skillDef
			for _, s := range em.Skills() {
				if slices.Contains(list, s.Name) {
					skills = append(skills, skillDef{Name: s.Name, Dir: s.Dir})
				}
			}
			if len(skills) > 0 {
				def["skills"] = skills
			}
		}
		if list, ok := strList(m, "mcp"); ok {
			var servers []ext.MCPServer
			for _, s := range em.Settings().MCP {
				if slices.Contains(list, s.Name) {
					servers = append(servers, s)
				}
			}
			if len(servers) > 0 {
				def["mcp"] = viewMCP(servers)
			}
		}
		if len(def) > 0 {
			out["define"] = def
		}
	}
	var d agentDefaults
	if v, ok := getPath(root, []string{"defaultModes", id}); ok {
		d.Mode, _ = v.(string)
	}
	if v, ok := getPath(root, []string{"defaultConfig", id}); ok {
		_ = remarshal(v, &d.Config)
	}
	if d.Mode != "" || len(d.Config) > 0 {
		out["defaults"] = d
	}
	printJSON(out)
	return nil
}

func removeAgent(id string, prune bool) error {
	st, sec := openConfig()
	var root any
	if err := json.Unmarshal(st.Get(), &root); err != nil {
		return fmt.Errorf("config.json: %w", err)
	}
	v, ok := getPath(root, []string{"agents", id})
	if !ok {
		return fmt.Errorf("no agent with id %q (built-in ones can't be removed)", id)
	}
	agent, _ := v.(map[string]any)
	root, _ = setPath(root, []string{"agents", id}, nil, true)
	root, _ = setPath(root, []string{"defaultModes", id}, nil, true)
	root, _ = setPath(root, []string{"defaultConfig", id}, nil, true)
	if d, _ := getPath(root, []string{"defaultAgent"}); d == id {
		root, _ = setPath(root, []string{"defaultAgent"}, nil, true)
	}
	if err := saveConfig(st, sec, root, false); err != nil {
		return err
	}
	out := map[string]any{"ok": true, "removed": id}
	if prune {
		out["pruned"], out["kept"] = pruneFor(agent, root)
	}
	printJSON(out)
	return nil
}

// pruneFor deletes the gerdr skills / MCP servers agent listed that no remaining agent lists. Not when the global
// source hands gerdr's pool to every session: then the item is in use beyond agents.
func pruneFor(agent map[string]any, root any) (pruned, kept map[string][]string) {
	pruned = map[string][]string{"skills": {}, "mcp": {}}
	kept = map[string][]string{"skills": {}, "mcp": {}}
	usedBy := func(key, name string) string {
		v, _ := getPath(root, []string{"agents"})
		arr, _ := v.([]any)
		for _, e := range arr {
			m, _ := e.(map[string]any)
			if list, ok := strList(m, key); ok && slices.Contains(list, name) {
				id, _ := m["id"].(string)
				return id
			}
		}
		return ""
	}
	em := openExt()
	st := em.Settings()
	if list, ok := strList(agent, "skills"); ok {
		for _, n := range list {
			if _, err := em.ReadSkill(n); err != nil {
				continue // not one of gerdr's (built-in, Claude Code's, Codex's): not ours to delete
			}
			switch {
			case st.SkillSource.UsesGerdr():
				kept["skills"] = append(kept["skills"], n+" (the skill source gives gerdr's to every session)")
			case usedBy("skills", n) != "":
				kept["skills"] = append(kept["skills"], n+" (used by "+usedBy("skills", n)+")")
			default:
				if em.DeleteSkill(n) == nil {
					st.DisabledSkills = slices.DeleteFunc(st.DisabledSkills, func(x string) bool { return x == n })
					pruned["skills"] = append(pruned["skills"], n)
				}
			}
		}
	}
	if list, ok := strList(agent, "mcp"); ok {
		for _, n := range list {
			i := findMCP(st.MCP, n)
			if i < 0 {
				continue
			}
			switch {
			case st.MCPSource.UsesGerdr():
				kept["mcp"] = append(kept["mcp"], n+" (the MCP source gives gerdr's to every session)")
			case usedBy("mcp", n) != "":
				kept["mcp"] = append(kept["mcp"], n+" (used by "+usedBy("mcp", n)+")")
			default:
				st.MCP = slices.Delete(st.MCP, i, i+1)
				pruned["mcp"] = append(pruned["mcp"], n)
			}
		}
	}
	if len(pruned["mcp"]) > 0 || len(pruned["skills"]) > 0 {
		if err := em.Save(st); err != nil {
			fmt.Fprintln(os.Stderr, "warning: saving ext.json:", strings.TrimSpace(err.Error()))
		}
	}
	return pruned, kept
}
