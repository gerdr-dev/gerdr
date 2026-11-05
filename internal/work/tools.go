package work

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dop251/goja"
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
		vm := goja.New()
		exports, err := load(vm, prog)
		if err != nil {
			continue
		}
		// Most files declare no tools: Get gives nil then, and Export on it panics (it took the whole endpoint down)
		v := exports.Get("tools")
		if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
			continue
		}
		decl, ok := v.Export().(map[string]any)
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
			if _, ok := goja.AssertFunction(exports.Get(k)); !ok {
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

// CallTool runs a declared tool for the conversation that called it (ctx.caller.session).
func (r *Runner) CallTool(ctx context.Context, project, name, session string, args any) (any, error) {
	tools, err := r.Tools(project)
	if err != nil {
		return nil, err
	}
	for _, t := range tools {
		if t.Name == name {
			res, err := r.StartAs(project, t.Fn, args, session).Wait(ctx)
			return stampCard(res, project), err
		}
	}
	return nil, fmt.Errorf("no tool %q in this panel (local/*.ts export const tools)", name)
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
