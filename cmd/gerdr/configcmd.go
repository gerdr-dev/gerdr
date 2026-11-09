package main

// gerdr config get / set / unset / keys: change gerdr's settings (config.json) from the command line.
//
// Why not just let agents edit config.json: set touches one field instead of rewriting the file (a stale copy
// would undo what the UI just saved), unknown keys and wrong types fail here instead of leaving a file the UI
// can't read, and `keys` says what each setting is. It writes the file directly, so it works with gerdr
// stopped too; a running gerdr picks the change up within a couple of seconds (config.Store.Watch) and
// pushes it to every open window. API keys go through the same secrets.json split as Settings does.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/gerdr-dev/gerdr/internal/config"
)

type configKey struct {
	kind string // bool | number | string | object | array
	enum []string
	desc string
	more string // longer help, printed by `gerdr config keys <name>`
}

// configKeys mirrors ui/src/lib/store/config.ts's Config. Keep them in step.
var configKeys = map[string]configKey{
	"theme":           {kind: "string", enum: []string{"system", "light", "dark"}, desc: "color theme (a device that picked its own in Settings keeps that; this is the value for the others)"},
	"fontSize":        {kind: "number", desc: "font size; only the starting value for a device that hasn't picked its own (each device keeps its own size)"},
	"sendKey":         {kind: "string", enum: []string{"enter", "mod+enter"}, desc: "enter = Enter sends, Shift+Enter newline; mod+enter = Cmd/Ctrl+Enter sends (a device that picked its own in Settings keeps that; this is the value for the others)"},
	"prefix":          {kind: "string", desc: `shortcut prefix key, tmux-style, e.g. "ctrl+b"`},
	"keys":            {kind: "object", desc: `shortcut overrides: action → list of key combos, e.g. {"splitRight": ["prefix %"]}`},
	"notifications":   {kind: "bool", desc: "system notifications when a turn finishes or needs you (a device that picked its own in Settings keeps that; this is the value for the others)"},
	"keepTabs":        {kind: "number", desc: "how many tabs stay mounted after you switch away (keeps scroll and expanded state); 1 = none (a device that picked its own in Settings keeps that; this is the value for the others)"},
	"expandEdits":     {kind: "bool", desc: "show file edits' diffs expanded by default"},
	"showThinking":    {kind: "bool", desc: "show thinking on its own line, expanded (default on)"},
	"confirmLinks":    {kind: "bool", desc: "ask before opening an external link from a chat (default on)"},
	"copyOnSelect":    {kind: "bool", desc: "Labs: copy text as soon as it is selected (a device that picked its own in Settings keeps that; this is the value for the others)"},
	"virtualList":     {kind: "bool", desc: "Labs: virtual scrolling for chats (a device that picked its own in Settings keeps that; this is the value for the others)"},
	"readFromTop":     {kind: "bool", desc: "Labs: stop following the bottom at the start of a reply text taller than the screen (a device that picked its own in Settings keeps that; this is the value for the others)"},
	"keepAwakeRemote": {kind: "bool", desc: "Labs: keep the computer awake (lid closed too) while a remote device is connected"},
	"remoteScreen":    {kind: "bool", desc: "Remote access: see this computer's screen and click / type on it from another device (macOS)"},
	"defaultAgent":    {kind: "string", desc: "agent id new sessions use (claude, codex, native, or an id from agents)"},
	"defaultModes":    {kind: "object", desc: `per agent id: the mode new sessions start in, e.g. {"claude": "acceptEdits"}`},
	"defaultConfig":   {kind: "object", desc: `per agent id: option id → value for new sessions, e.g. {"claude": {"model": "opus", "effort": "high"}}`},
	"pinnedCommands":  {kind: "array", desc: "slash commands (no /) pinned beside the input box"},
	"agents":          {kind: "array", desc: "agents (roles) from Settings → Agent: {id, name, base: claude|codex|native, prompt, skills, mcp, providers, env…}; address one as agents.<id>; `gerdr config keys agents` for every field", more: agentsHelp},
	"projects":        {kind: "object", desc: "per project id (default, or the id of a project you made): skills / MCP servers its sessions don't get, {\"default\": {\"disabledSkills\": [...], \"disabledMcp\": [...]}}; `gerdr config keys projects` for more", more: projectsHelp},
	"agentBins":       {kind: "object", desc: "which claude / codex executable to run: {\"claude\": \"/path/to/claude\"}"},
	"llm":             {kind: "object", desc: "Gerdr Agent's model providers: {providers: [{id, name, api (omit = OpenAI-compatible, \"anthropic\" = Anthropic Messages), baseUrl, apiKey, apiKeyEnv, models: [{id, window, compactAt, efforts, maxOutput, images, thinking}]}], defaultModel: \"provider/model\"}; a model needs only its id, the rest is guessed from it: window = context window in tokens, compactAt = token count at which the context is auto-compacted (default 90% of window); apiKey is stored in secrets.json and reads back as <hidden>"},
	"workSessions":    {kind: "object", desc: "managed by the UI: each panel's agent conversation"},
	"workAgents":      {kind: "object", desc: "managed by the UI: each panel's agent"},
}

// agentsHelp mirrors hub.AgentDef and Settings → Agent's editor (ui/src/components/AgentSettings.tsx).
const agentsHelp = `Each element of agents is one agent (role) offered when starting a session:

  id         unique, [a-z0-9-]; not claude / codex / native (those are the built-in ones)
  name       shown in the UI
  base       the engine: claude (Claude Code) | codex (Codex) | native (Gerdr Agent, calls the model API itself)
  command    instead of base: an external ACP agent's command line, e.g. ["gemini", "--experimental-acp"],
             ["opencode", "acp"], ["pi-acp"] (pi: npm i -g @mariozechner/pi-coding-agent pi-acp)
  prompt     extra system prompt appended for this agent
  skills     only these skills, by name. Omit = whatever the global Skill settings give; [] = none.
             Skills that come with the repository being worked on are given anyway (no list can name them).
  mcp        only these MCP servers, by name. Omit = whatever the global MCP settings give; [] = none.
  providers  native only: which llm.providers ids it may use (gerdr config get llm.providers); omit = all
  bin, args  claude / codex only: another executable, extra arguments
  env        claude / codex / command: environment variables (e.g. {"CLAUDE_CONFIG_DIR": "~/.claude-work"} for a
             second account, {"OPENCODE_CONFIG": "~/oc-work.json"} for another opencode config)

skills / mcp may name any switched-on skill / MCP server: built-in, gerdr's own (gerdr skill add, gerdr mcp add)
or the engine's own (Claude Code's / Codex's), whatever the global source says. Switched-off ones are never given.
The names: ` + "`gerdr skill list`" + ` / ` + "`gerdr mcp list`" + `. A name that isn't there is ignored.
To set up an agent with new skills / MCP in one go, use ` + "`gerdr agent create`" + ` instead.

New sessions' defaults are stored per agent id, outside agents:
  defaultModes.<id>    mode, e.g. bypassPermissions (Full access), acceptEdits, default, plan
  defaultConfig.<id>   option id → value, e.g. {"model": "opus", "effort": "high"}
  defaultAgent         the agent id the new-session screen picks first

  gerdr config add agents '{"id":"reviewer","name":"Reviewer","base":"claude","prompt":"Review only, don't edit","skills":["code-review"],"mcp":[]}'
  gerdr config set agents.reviewer.mcp '["github"]'
  gerdr config set defaultModes.reviewer plan
  gerdr config unset agents.reviewer`

// projectsHelp mirrors ext.Manager.ForProject and the project's Skills & MCP dialog (ui/src/components/ProjectExtDialog.tsx).
const projectsHelp = `projects.<id> holds settings of one project (the id is the project's, e.g. default, lb):

  disabledSkills   skills this project's sessions don't get, by name
  disabledMcp      MCP servers this project's sessions don't get, by name (Claude Code: claude.ai connectors too,
                   e.g. "claude.ai Notion")

Everything else is given as usual (the global settings, the agent's own list). Use it when projects work differently:
one Codex / Claude agent then serves every project, instead of one agent (role) per project. Names: ` + "`gerdr skill list`" + ` /
` + "`gerdr mcp list`" + `. A Claude Code plugin's skill can't be switched off alone: the plugin goes off (with its MCP and
hooks) once all of its skills are listed.

A session is in the project whose workspace shows it (else the one it was started in). A change applies before each
session's next turn (Claude Code restarts its process, Codex re-attaches the thread).

  gerdr config set projects.default.disabledSkills '["lb-cli", "deva"]'
  gerdr config set projects.default.disabledMcp '["okr", "foxden"]'
  gerdr config unset projects.default.disabledMcp`

func configCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Read and change Gerdr's settings (config.json); open windows update within seconds",
		Long: `Read and change Gerdr's settings (config.json); open windows update within seconds.

A path is dot-separated; an array element is addressed by its index or by its "id" (agents.reviewer,
llm.providers.deepseek). Values are JSON when they parse as JSON (true, 16, ["a"], {"k": 1}), a plain
string otherwise.

  gerdr config keys
  gerdr config get theme
  gerdr config set theme dark
  gerdr config set defaultModes.claude acceptEdits
  gerdr config add agents '{"id": "reviewer", "name": "Reviewer", "base": "claude"}'
  gerdr config unset defaultConfig.codex

API keys (llm.providers.*.apiKey) are stored in secrets.json and always read back as <hidden>.`,
	}
	var force bool
	cmd.AddCommand(&cobra.Command{
		Use:   "keys [name]",
		Short: "List the settings with what they do (with a name: that one in full)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			if len(a) == 1 {
				d, ok := configKeys[a[0]]
				if !ok {
					return fmt.Errorf("unknown setting %q", a[0])
				}
				fmt.Printf("%s (%s): %s\n", a[0], d.kind, d.desc)
				if d.more != "" {
					fmt.Printf("\n%s\n", d.more)
				}
				return nil
			}
			names := make([]string, 0, len(configKeys))
			for k := range configKeys {
				names = append(names, k)
			}
			sort.Strings(names)
			for _, k := range names {
				d := configKeys[k]
				t := d.kind
				if len(d.enum) > 0 {
					t = strings.Join(d.enum, "|")
				}
				fmt.Printf("%-16s %-18s %s\n", k, t, d.desc)
			}
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "get [path]",
		Short: "Print a setting (the whole config without a path) as JSON",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			st, _ := openConfig()
			var root any
			if err := json.Unmarshal(st.Get(), &root); err != nil {
				return fmt.Errorf("config.json: %w", err)
			}
			v := root
			if len(a) == 1 {
				var ok bool
				if v, ok = getPath(root, splitPath(a[0])); !ok {
					return fmt.Errorf("%s is not set", a[0])
				}
			}
			printJSON(v)
			return nil
		},
	})
	set := &cobra.Command{
		Use:   "set <path> <value>",
		Short: "Set a setting",
		Args:  cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, a []string) error {
			var v any
			if err := json.Unmarshal([]byte(a[1]), &v); err != nil {
				v = a[1]
			}
			return editConfig(a[0], v, false, force)
		},
	}
	set.Flags().BoolVar(&force, "force", false, "allow a key `gerdr config keys` doesn't list")
	cmd.AddCommand(set)
	cmd.AddCommand(&cobra.Command{
		Use:   "add <path> <value>",
		Short: "Append an element to a list (agents, llm.providers…); an element with the same id is refused",
		Args:  cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, a []string) error {
			var v any
			if err := json.Unmarshal([]byte(a[1]), &v); err != nil {
				v = a[1]
			}
			return appendConfig(a[0], v)
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "unset <path>",
		Short: "Remove a setting (back to its default)",
		Args:  cobra.ExactArgs(1),
		RunE:  func(_ *cobra.Command, a []string) error { return editConfig(a[0], nil, true, true) },
	})
	return cmd
}

func openConfig() (*config.Store, *config.Secrets) {
	dir := config.FromEnv().Dir
	st := config.NewStore(filepath.Join(dir, "config.json"), `{}`)
	sec := config.NewSecrets(filepath.Join(dir, "secrets.json"))
	st.SetFilter(sec.Split)
	return st, sec
}

func editConfig(path string, v any, del, force bool) error {
	p := splitPath(path)
	if len(p) == 0 {
		return fmt.Errorf("empty path")
	}
	if k, ok := configKeys[p[0]]; !ok && !force {
		return fmt.Errorf("unknown setting %q (see `gerdr config keys`; --force to write it anyway)", p[0])
	} else if ok && !del && len(p) == 1 {
		if err := checkKind(p[0], k, v); err != nil {
			return err
		}
	}
	st, sec := openConfig()
	var root any
	if err := json.Unmarshal(st.Get(), &root); err != nil {
		return fmt.Errorf("config.json: %w", err)
	}
	root, err := setPath(root, p, v, del)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return writeConfig(st, sec, root, path)
}

// appendConfig adds v to the end of the list at path (creating the list if it isn't there). Without it a new
// agent meant reading the whole agents array and writing it back, which also undoes whatever the UI saved between.
func appendConfig(path string, v any) error {
	p := splitPath(path)
	if len(p) == 0 {
		return fmt.Errorf("empty path")
	}
	if k, ok := configKeys[p[0]]; !ok {
		return fmt.Errorf("unknown setting %q (see `gerdr config keys`)", p[0])
	} else if len(p) == 1 && k.kind != "array" {
		return fmt.Errorf("%s takes a %s, not a list", p[0], k.kind)
	}
	st, sec := openConfig()
	var root any
	if err := json.Unmarshal(st.Get(), &root); err != nil {
		return fmt.Errorf("config.json: %w", err)
	}
	cur, ok := getPath(root, p)
	var arr []any
	if ok && cur != nil {
		if arr, ok = cur.([]any); !ok {
			return fmt.Errorf("%s is a %T, not a list", path, cur)
		}
	}
	if m, isObj := v.(map[string]any); isObj {
		if id, _ := m["id"].(string); id != "" && index(arr, id) >= 0 {
			return fmt.Errorf("%s already has an element with id %q (change it with `gerdr config set %s.%s.<field> …`)", path, id, path, id)
		}
	}
	root, err := setPath(root, p, append(arr, v), false)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if m, isObj := v.(map[string]any); isObj {
		if id, _ := m["id"].(string); id != "" {
			path += "." + id
		}
	}
	return writeConfig(st, sec, root, path)
}

func writeConfig(st *config.Store, sec *config.Secrets, root any, path string) error {
	if err := saveConfig(st, sec, root, splitPath(path)[0] == "agents"); err != nil {
		return err
	}
	out, _ := getPath(mustDecode(st.Get()), splitPath(path))
	printJSON(map[string]any{"ok": true, "path": path, "value": out})
	return nil
}

// kindOf names a decoded JSON value's type the way configKey.kind does.
func kindOf(v any) string {
	switch v.(type) {
	case bool:
		return "bool"
	case float64:
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	case nil:
		return "null"
	}
	return "object"
}

func checkKind(name string, k configKey, v any) error {
	if got := kindOf(v); got != k.kind {
		return fmt.Errorf("%s takes a %s, not a %s", name, k.kind, got)
	}
	if len(k.enum) > 0 {
		for _, e := range k.enum {
			if v == e {
				return nil
			}
		}
		return fmt.Errorf("%s is one of %s", name, strings.Join(k.enum, ", "))
	}
	return nil
}

func splitPath(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ".") {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// index finds an array element by position or by its "id".
func index(arr []any, seg string) int {
	if n, err := strconv.Atoi(seg); err == nil {
		if n >= 0 && n < len(arr) {
			return n
		}
		return -1
	}
	for i, e := range arr {
		if m, ok := e.(map[string]any); ok && m["id"] == seg {
			return i
		}
	}
	return -1
}

func getPath(v any, p []string) (any, bool) {
	for _, seg := range p {
		switch n := v.(type) {
		case map[string]any:
			var ok bool
			if v, ok = n[seg]; !ok {
				return nil, false
			}
		case []any:
			i := index(n, seg)
			if i < 0 {
				return nil, false
			}
			v = n[i]
		default:
			return nil, false
		}
	}
	return v, true
}

// setPath returns v with the value at p replaced (or removed), creating objects on the way as needed.
func setPath(v any, p []string, val any, del bool) (any, error) {
	if len(p) == 0 {
		return val, nil
	}
	seg, rest := p[0], p[1:]
	switch n := v.(type) {
	case nil:
		if del {
			return nil, nil
		}
		child, err := setPath(nil, rest, val, del)
		return map[string]any{seg: child}, err
	case map[string]any:
		if del && len(rest) == 0 {
			delete(n, seg)
			return n, nil
		}
		if _, ok := n[seg]; !ok && del {
			return n, nil
		}
		child, err := setPath(n[seg], rest, val, del)
		if err != nil {
			return nil, err
		}
		n[seg] = child
		return n, nil
	case []any:
		i := index(n, seg)
		if i < 0 {
			return nil, fmt.Errorf("no element %q (use an index or an existing id)", seg)
		}
		if del && len(rest) == 0 {
			return append(n[:i], n[i+1:]...), nil
		}
		child, err := setPath(n[i], rest, val, del)
		if err != nil {
			return nil, err
		}
		n[i] = child
		return n, nil
	default:
		return nil, fmt.Errorf("%q is inside a %T, not an object", seg, v)
	}
}

func mustDecode(b []byte) any {
	var v any
	_ = json.Unmarshal(b, &v)
	return v
}

// saveConfig writes root as config.json (API keys split off to secrets.json), checking agents first if asked.
func saveConfig(st *config.Store, sec *config.Secrets, root any, agents bool) error {
	if agents {
		if err := checkAgents(root); err != nil {
			return err
		}
	}
	b, _ := json.Marshal(root)
	if err := st.Set(b); err != nil {
		return err
	}
	sec.Prune(st.Get())
	return nil
}

var agentFields = map[string]string{
	"id": "string", "name": "string", "base": "string", "command": "array", "bin": "string", "args": "array",
	"env": "object", "prompt": "string", "skills": "array", "mcp": "array", "providers": "array",
}

// checkAgents refuses an agents list the backend would quietly drop entries from (hub.ValidAgentDefs) or misread,
// and warns about field names it doesn't know (a typo like "skill" would otherwise just do nothing).
func checkAgents(root any) error {
	v, _ := getPath(root, []string{"agents"})
	if v == nil {
		return nil
	}
	arr, ok := v.([]any)
	if !ok {
		return fmt.Errorf("agents takes a list")
	}
	seen := map[string]bool{"claude": true, "codex": true, "native": true}
	for i, e := range arr {
		m, ok := e.(map[string]any)
		if !ok {
			return fmt.Errorf("agents[%d] is not an object", i)
		}
		id, _ := m["id"].(string)
		switch {
		case id == "":
			return fmt.Errorf("agents[%d] has no id", i)
		case strings.ContainsAny(id, ". "):
			return fmt.Errorf("agent id %q: no dots or spaces", id)
		case id == "claude" || id == "codex" || id == "native":
			return fmt.Errorf("agent id %q is a built-in one; pick another", id)
		case seen[id]:
			return fmt.Errorf("two agents with id %q", id)
		}
		seen[id] = true
		for k, val := range m {
			want, known := agentFields[k]
			if !known {
				fmt.Fprintf(os.Stderr, "warning: agents.%s: unknown field %q (see `gerdr config keys agents`)\n", id, k)
				continue
			}
			if val == nil {
				continue
			}
			if kindOf(val) != want {
				return fmt.Errorf("agents.%s.%s takes %s", id, k, map[string]string{"string": "a string", "array": "a list", "object": "an object"}[want])
			}
			if want == "array" {
				for _, x := range val.([]any) {
					if _, ok := x.(string); !ok {
						return fmt.Errorf("agents.%s.%s is a list of strings", id, k)
					}
				}
			}
		}
		base, _ := m["base"].(string)
		cmd, _ := m["command"].([]any)
		switch {
		case base != "" && base != "claude" && base != "codex" && base != "native":
			return fmt.Errorf("agents.%s.base is claude, codex or native, not %q", id, base)
		case base == "" && len(cmd) == 0:
			return fmt.Errorf("agents.%s needs a base (claude / codex / native) or a command", id)
		}
	}
	return nil
}
