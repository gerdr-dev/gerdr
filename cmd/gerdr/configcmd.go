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
}

// configKeys mirrors ui/src/lib/store.ts's Config. Keep them in step.
var configKeys = map[string]configKey{
	"theme":           {kind: "string", enum: []string{"system", "light", "dark"}, desc: "color theme"},
	"fontSize":        {kind: "number", desc: "font size; only the starting value for a device that hasn't picked its own (each device keeps its own size)"},
	"sendKey":         {kind: "string", enum: []string{"enter", "mod+enter"}, desc: "enter = Enter sends, Shift+Enter newline; mod+enter = Cmd/Ctrl+Enter sends"},
	"prefix":          {kind: "string", desc: `shortcut prefix key, tmux-style, e.g. "ctrl+b"`},
	"keys":            {kind: "object", desc: `shortcut overrides: action → list of key combos, e.g. {"splitRight": ["prefix %"]}`},
	"notifications":   {kind: "bool", desc: "system notifications when a turn finishes or needs you"},
	"keepTabs":        {kind: "number", desc: "how many tabs stay mounted after you switch away (keeps scroll and expanded state); 1 = none"},
	"expandEdits":     {kind: "bool", desc: "show file edits' diffs expanded by default"},
	"showThinking":    {kind: "bool", desc: "show thinking on its own line, expanded (default on)"},
	"confirmLinks":    {kind: "bool", desc: "ask before opening an external link from a chat (default on)"},
	"copyOnSelect":    {kind: "bool", desc: "Labs: copy text as soon as it is selected"},
	"virtualList":     {kind: "bool", desc: "Labs: virtual scrolling for chats"},
	"keepAwakeRemote": {kind: "bool", desc: "Labs: keep the computer awake (lid closed too) while a remote device is connected"},
	"defaultAgent":    {kind: "string", desc: "agent id new sessions use (claude, codex, native, or an id from agents)"},
	"defaultModes":    {kind: "object", desc: `per agent id: the mode new sessions start in, e.g. {"claude": "acceptEdits"}`},
	"defaultConfig":   {kind: "object", desc: `per agent id: option id → value for new sessions, e.g. {"claude": {"model": "opus", "effort": "high"}}`},
	"pinnedCommands":  {kind: "array", desc: "slash commands (no /) pinned beside the input box"},
	"agents":          {kind: "array", desc: "agents (roles) from Settings → Agent: {id, name, base: claude|codex|native, prompt, skills, mcp, providers, env…}; address one as agents.<id>"},
	"agentBins":       {kind: "object", desc: "which claude / codex executable to run: {\"claude\": \"/path/to/claude\"}"},
	"llm":             {kind: "object", desc: "Gerdr Agent's model providers: {providers: [{id, name, api (omit = OpenAI-compatible, \"anthropic\" = Anthropic Messages), baseUrl, apiKey, apiKeyEnv, models: [{id}]}], defaultModel: \"provider/model\"}; apiKey is stored in secrets.json and reads back as <hidden>"},
	"workSessions":    {kind: "object", desc: "managed by the UI: each project's Work conversation"},
	"workAgents":      {kind: "object", desc: "managed by the UI: each project's Work agent"},
}

func configCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Read and change gerdr's settings (config.json); open windows update within seconds",
		Long: `Read and change gerdr's settings (config.json); open windows update within seconds.

A path is dot-separated; an array element is addressed by its index or by its "id" (agents.reviewer,
llm.providers.deepseek). Values are JSON when they parse as JSON (true, 16, ["a"], {"k": 1}), a plain
string otherwise.

  gerdr config keys
  gerdr config get theme
  gerdr config set theme dark
  gerdr config set defaultModes.claude acceptEdits
  gerdr config unset defaultConfig.codex

API keys (llm.providers.*.apiKey) are stored in secrets.json and always read back as <hidden>.`,
	}
	var force bool
	cmd.AddCommand(&cobra.Command{
		Use:   "keys",
		Short: "List the settings with what they do",
		Args:  cobra.NoArgs,
		Run: func(_ *cobra.Command, _ []string) {
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
	b, _ := json.Marshal(root)
	if err := st.Set(b); err != nil {
		return err
	}
	sec.Prune(st.Get())
	out, _ := getPath(mustDecode(st.Get()), p)
	printJSON(map[string]any{"ok": true, "path": path, "value": out})
	return nil
}

func checkKind(name string, k configKey, v any) error {
	got := "object"
	switch v.(type) {
	case bool:
		got = "bool"
	case float64:
		got = "number"
	case string:
		got = "string"
	case []any:
		got = "array"
	case nil:
		got = "null"
	}
	if got != k.kind {
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
