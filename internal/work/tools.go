package work

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Work tools: local functions a Work project offers the agents it works with as MCP tools (`gerdr panel mcp`, given
// to the project's Work agent and every session the Work starts, like its skills). A local file declares them next
// to the functions:
//
//	export const tools = {
//	  ask_human: { description: "…", input: { type: "object", properties: { … }, required: [ … ] } },
//	}
//	export async function ask_human(input, ctx) { … ctx.caller.session … }
//
// The tool's name is the key and runs the export of the same name in that file, deterministic like any local
// function. ctx.caller.session is the conversation that called it, so a board knows which task's worker it is
// without the agent having to say. Why tools and not text the agent writes: an agent can call one whenever it
// needs, change or take back what it did, and gets an answer (an id, an error) in the same turn.

// Tool is one declared tool.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Input       map[string]any `json:"inputSchema"`
	Fn          string         `json:"fn"` // file.export it runs
}

// Tools lists the project's declared tools; a name declared twice keeps the first file's (files by name).
func (r *Runner) Tools(project string) ([]Tool, error) {
	dir, err := r.Ensure(project)
	if err != nil {
		return nil, err
	}
	ents, err := os.ReadDir(filepath.Join(dir, "local"))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	names := []string{}
	for _, e := range ents {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	out := []Tool{}
	seen := map[string]bool{}
	for _, n := range names {
		ext := filepath.Ext(n)
		base := strings.TrimSuffix(n, ext)
		if (ext != ".ts" && ext != ".js") || !fileRE.MatchString(base) || strings.HasSuffix(base, ".d") {
			continue
		}
		prog, err := compile(filepath.Join(dir, "local", n))
		if err != nil {
			continue
		}
		ex, err := exportsOf(prog)
		if err != nil {
			continue
		}
		// Most files declare no tools
		decl, ok := toPlain(ex["tools"]).(map[string]any)
		if !ok {
			continue
		}
		keys := make([]string, 0, len(decl))
		for k := range decl {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if seen[k] {
				continue
			}
			if !isFunc(ex[k]) {
				continue // declared without its function: nothing to run
			}
			d, _ := decl[k].(map[string]any)
			desc, _ := d["description"].(string)
			input, _ := d["input"].(map[string]any)
			if input == nil {
				input = map[string]any{"type": "object", "properties": map[string]any{}}
			}
			seen[k] = true
			out = append(out, Tool{Name: k, Description: desc, Input: input, Fn: base + "." + k})
		}
	}
	return out, nil
}

// Instructions is what the panel tells one of its conversations about itself, next to its tools: the text of
// `export function instructions(input, ctx)` in the file that declares them, run for that conversation
// (ctx.caller.session; "" when the panel has none). `gerdr panel mcp` gives it as the MCP server's instructions,
// which the agent keeps in its system prompt: unlike a brief in the first message, it survives /clear and
// compaction. A board says there which task the conversation works on (a worker that had /clear didn't know it was
// one, 2026-11-05).
func (r *Runner) Instructions(ctx context.Context, project, session string) (string, error) {
	dir, err := r.Ensure(project)
	if err != nil {
		return "", err
	}
	ents, err := os.ReadDir(filepath.Join(dir, "local"))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	names := []string{}
	for _, e := range ents {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, n := range names {
		ext := filepath.Ext(n)
		base := strings.TrimSuffix(n, ext)
		if (ext != ".ts" && ext != ".js") || !fileRE.MatchString(base) || strings.HasSuffix(base, ".d") {
			continue
		}
		prog, err := compile(filepath.Join(dir, "local", n))
		if err != nil {
			continue
		}
		ex, err := exportsOf(prog)
		if err != nil {
			continue
		}
		if ex["tools"] == nil || !isFunc(ex["instructions"]) {
			continue
		}
		res, err := r.StartAs(project, base+".instructions", nil, session).Wait(ctx)
		if err != nil {
			return "", err
		}
		text, _ := res.(string)
		return strings.TrimSpace(text), nil
	}
	return "", nil
}

// CallTool runs a declared tool for the conversation that called it (ctx.caller.session).
func (r *Runner) CallTool(ctx context.Context, project, name, session string, args any) (any, error) {
	tools, err := r.Tools(project)
	if err != nil {
		return nil, err
	}
	for _, t := range tools {
		if t.Name == name {
			if err := checkArgs(t.Input, args); err != nil {
				return nil, err
			}
			res, err := r.StartAs(project, t.Fn, args, session).Wait(ctx)
			return stampCard(res, project), err
		}
	}
	return nil, fmt.Errorf("no tool %q in this panel (local/*.ts export const tools)", name)
}

// checkArgs holds a call to the top level of its tool's declared schema: required fields there, string fields
// within their enum. Neither Gerdr's MCP server nor the agent checks it, and a panel function reading a field that
// isn't there gets undefined and carries on: a worker that called task_update with status: "review" instead of
// state: "review" had its result taken as "still working", and the user never saw it was waiting on them
// (2026-11-11). The error names what's wrong and the fields it doesn't know, so the agent calls again correctly.
func checkArgs(schema map[string]any, args any) error {
	m, _ := args.(map[string]any)
	props, _ := schema["properties"].(map[string]any)
	var problems []string
	for _, k := range toStrings(schema["required"]) {
		if v, ok := m[k]; !ok || v == nil {
			problems = append(problems, fmt.Sprintf("%q is required", k))
		}
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var unknown []string
	for _, k := range keys {
		p, ok := props[k].(map[string]any)
		if !ok {
			if props != nil {
				unknown = append(unknown, k)
			}
			continue
		}
		enum := toStrings(p["enum"])
		if s, isStr := m[k].(string); isStr && len(enum) > 0 && !contains(enum, s) {
			problems = append(problems, fmt.Sprintf("%q must be one of %s, not %q", k, strings.Join(enum, " / "), s))
		}
	}
	if len(problems) == 0 {
		// Extra fields alone are let through: harmless unless they stand in for one the tool needs
		return nil
	}
	msg := "invalid input: " + strings.Join(problems, "; ")
	if len(unknown) > 0 {
		msg += fmt.Sprintf(" (this tool has no %s)", strings.Join(unknown, ", "))
	}
	return fmt.Errorf("%s", msg)
}

func toStrings(v any) []string {
	var out []string
	switch x := v.(type) {
	case []string:
		return x
	case []any:
		for _, e := range x {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
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

// stampCard marks a tool result's card ({gerdrCard: {…}}) with the panel that made it: card types are registered per
// panel (ui/src/lib/cards.ts), so a card is drawn by its own panel's code even when another panel — or the same panel
// installed in another project — registered the same type. Set here, not by the panel, so it can't be wrong.
func stampCard(res any, project string) any {
	m, ok := res.(map[string]any)
	if !ok {
		return res
	}
	card, ok := m["gerdrCard"].(map[string]any)
	if !ok {
		return res
	}
	if project == "" {
		project = DefaultProject
	}
	card["panel"] = project
	return res
}

type callerKey struct{}

func callerOf(ctx context.Context) string {
	s, _ := ctx.Value(callerKey{}).(string)
	return s
}
