// Package work runs the business logic behind the Work view: local functions in local/*.ts of each
// project's Work directory (see Runner.Dir), the same shape and runtime as a hosted function platform's:
//
//	export async function summary(input, ctx) {
//	  const me = await ctx.mcp("okr", "get_current_user", {})
//	  const { stdout } = await ctx.exec("glab", ["mr", "list", "-F", "json"])
//	  return { me, mrs: JSON.parse(stdout) }
//	}
//
// Called by name ("okr.summary" = export summary of local/okr.ts) from the Work page, without a
// model in the loop: deterministic logic the page needs every time it opens shouldn't cost an agent
// turn. esbuild bundles the TS to CommonJS and goja (pure-Go JS) runs it, so no node is needed.
//
// What a function can do is what ctx and the globals offer (fn.go): run commands (ctx.exec), fetch, call MCP tools
// (gerdr's own list, then Claude Code's and Codex's, signed in through internal/mcpauth), read and write the Work
// directory (ctx.fs), read the user's stored keys (ctx.secrets), report progress and log. Runs outlive the request,
// stream their progress and can be stopped (runs.go).
package work

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// builtin/base goes into every project's Work directory (the site skeleton, a blank starting page, the
// agent's CLAUDE.md). The default project used to get an OKR page laid over it; that was one person's
// setup, not a default, so every project now starts blank and install removes the old OKR files unless
// they were edited.
//
//go:embed builtin
var builtin embed.FS

// CallMCP calls one tool on a named MCP server and returns the raw tools/call result.
type CallMCP func(ctx context.Context, server, tool string, args map[string]any) (json.RawMessage, error)

// DefaultProject is the project (a gerdr "space" in the layout) everyone starts with.
const DefaultProject = "default"

// Runner runs every project's local functions. Each project (Code workspaces + its own Work) has its
// own Work directory: the default project's is <GERDR_DIR>/work (where it lived before projects
// existed), others' are <GERDR_DIR>/projects/<id>/work.
type Runner struct {
	Root       string // <GERDR_DIR>
	MCP        CallMCP
	MCPServers func() []MCPServer       // ctx.mcp.servers()
	Secret     func(name string) string // ctx.secrets.get; "" = none
	Host       Host                     // ctx.sessions / ctx.workspaces / ctx.dirs.recent (host.go); nil = not available
	Log        *slog.Logger

	mu        sync.Mutex
	installed map[string]bool
	runs      map[string]*Run
	pruned    map[string]time.Time // log directories by when they were last pruned
	sched_    scheduler            // schedules/ (schedule.go)
}

func New(root string, mcp CallMCP) *Runner {
	return &Runner{Root: root, MCP: mcp, installed: map[string]bool{}}
}

func errBadProject(id string) error { return fmt.Errorf("bad project name %q", id) }

// validProject: a project's id is its name as the user typed it (ui/src/lib/layout.ts projectNameError has the same
// rules): it names the Work directory and appears in the address, so no path separators, dots or control characters.
func validProject(id string) bool {
	if id == "" || utf8.RuneCountInString(id) > 64 || strings.TrimSpace(id) != id {
		return false
	}
	return !strings.ContainsFunc(id, func(c rune) bool { return c == '/' || c == '\\' || c == '.' || c < 0x20 })
}

// A project has panels: its Work (key: the project's id) and any number more, each a whole Work app of its own (pages,
// local functions, skills, schedules, data) in <project dir>/panels/<name> (key: "<project>/<name>"; the default
// project's are in <root>/panels). Everything about a Work goes by its key: functions, tools, schedules, its agent's
// conversation. The layout (Code's workspaces, which window a session is in) goes by the project alone (SplitKey).
const PanelsDir = "panels"

// SplitKey: "lb/board" → ("lb", "board"); "lb" → ("lb", ""); "" → (DefaultProject, "").
func SplitKey(key string) (project, panel string) {
	project, panel, _ = strings.Cut(key, "/")
	if project == "" {
		project = DefaultProject
	}
	return project, panel
}

// PanelKey is the key of a project's panel ("" = its Work).
func PanelKey(project, panel string) string {
	if project == "" {
		project = DefaultProject
	}
	if panel == "" {
		return project
	}
	return project + "/" + panel
}

// panelsDir: where a project's extra panels live.
func (r *Runner) panelsDir(project string) (string, error) {
	if project == DefaultProject {
		return filepath.Join(r.Root, PanelsDir), nil
	}
	if !validProject(project) {
		return "", errBadProject(project)
	}
	return filepath.Join(r.Root, "projects", project, PanelsDir), nil
}

// Dir is the Work directory of a key: a project ("" = the default one) or one of its panels.
func (r *Runner) Dir(key string) (string, error) {
	project, panel := SplitKey(key)
	if panel != "" {
		if !validProject(panel) {
			return "", fmt.Errorf("bad panel name %q", panel)
		}
		d, err := r.panelsDir(project)
		if err != nil {
			return "", err
		}
		return filepath.Join(d, panel), nil
	}
	if project == DefaultProject {
		return filepath.Join(r.Root, "work"), nil
	}
	if !validProject(project) {
		return "", errBadProject(project)
	}
	return filepath.Join(r.Root, "projects", project, "work"), nil
}

// Panels: a project's extra panels (not its Work), by name.
func (r *Runner) Panels(project string) []string {
	d, err := r.panelsDir(project)
	if err != nil {
		return nil
	}
	var out []string
	ents, _ := os.ReadDir(d)
	for _, e := range ents {
		if e.IsDir() && validProject(e.Name()) {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// Projects: the keys of every Work on disk with a UI (each project's Work, then its panels).
func (r *Runner) Projects() []string {
	var out []string
	has := func(key string) bool {
		d, err := r.Dir(key)
		if err != nil {
			return false
		}
		fi, err := os.Stat(filepath.Join(d, "ui"))
		return err == nil && fi.IsDir()
	}
	projects := []string{DefaultProject}
	ents, _ := os.ReadDir(filepath.Join(r.Root, "projects"))
	for _, e := range ents {
		if e.IsDir() && validProject(e.Name()) && e.Name() != DefaultProject {
			projects = append(projects, e.Name())
		}
	}
	for _, p := range projects {
		if has(p) {
			out = append(out, p)
		}
		for _, n := range r.Panels(p) {
			if k := PanelKey(p, n); has(k) {
				out = append(out, k)
			}
		}
	}
	return out
}

// WorkProject is the project whose Work (or one of whose panels) holds dir, false when dir is in none. That's how a
// Work agent (an ordinary session opened in its Work directory, shown in no pane) is placed in its project.
func (r *Runner) WorkProject(dir string) (string, bool) {
	key, ok := r.WorkKey(dir)
	if !ok {
		return "", false
	}
	p, _ := SplitKey(key)
	return p, true
}

// WorkKey is the Work (a project's, or a panel's) whose directory holds dir.
func (r *Runner) WorkKey(dir string) (string, bool) {
	if dir == "" {
		return "", false
	}
	under := func(base string) (string, bool) {
		rel, err := filepath.Rel(base, dir)
		if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
			return "", false
		}
		return filepath.ToSlash(rel), true
	}
	panelOf := func(project, rest string) (string, bool) {
		name, _, _ := strings.Cut(rest, "/")
		if validProject(name) {
			return PanelKey(project, name), true
		}
		return "", false
	}
	if _, ok := under(filepath.Join(r.Root, "work")); ok {
		return DefaultProject, true
	}
	if rest, ok := under(filepath.Join(r.Root, PanelsDir)); ok && rest != "." {
		return panelOf(DefaultProject, rest)
	}
	if rel, ok := under(filepath.Join(r.Root, "projects")); ok {
		id, rest, _ := strings.Cut(rel, "/")
		if !validProject(id) {
			return "", false
		}
		if rest == "work" || strings.HasPrefix(rest, "work/") {
			return id, true
		}
		if strings.HasPrefix(rest, PanelsDir+"/") {
			return panelOf(id, strings.TrimPrefix(rest, PanelsDir+"/"))
		}
	}
	return "", false
}

// ProjectOf is the Work key whose directory contains dir (for `gerdr work …` run by a Work agent from its cwd); the
// default project when it's none of them.
func (r *Runner) ProjectOf(dir string) string {
	if k, ok := r.WorkKey(dir); ok {
		return k
	}
	return DefaultProject
}

// Ensure returns the project's Work directory, installing the bundled files the first time this
// process touches it.
func (r *Runner) Ensure(project string) (string, error) {
	dir, err := r.Dir(project)
	if err != nil {
		return "", err
	}
	r.mu.Lock()
	done := r.installed[dir]
	r.mu.Unlock()
	if !done {
		if err := r.install(dir); err != nil {
			return "", err
		}
		r.mu.Lock()
		r.installed[dir] = true
		r.mu.Unlock()
	}
	return dir, nil
}

// bundled is the files a Work directory gets: rel path → content.
func bundled() map[string][]byte {
	out := map[string][]byte{}
	const root = "builtin/base"
	_ = fs.WalkDir(builtin, root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			out[strings.TrimPrefix(p, root+"/")], _ = builtin.ReadFile(p)
		}
		return nil
	})
	return out
}

// install copies the bundled files into dir. A file the user edited is left alone; one still identical
// to what an older gerdr installed is upgraded, or removed when gerdr no longer ships it (hashes of what
// was installed are kept in .builtin.json).
func (r *Runner) install(dir string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := os.MkdirAll(filepath.Join(dir, "local"), 0o755); err != nil {
		return err
	}
	marks := map[string]string{}
	markFile := filepath.Join(dir, ".builtin.json")
	if b, err := os.ReadFile(markFile); err == nil {
		_ = json.Unmarshal(b, &marks)
	}
	files := bundled()
	for rel, want := range files {
		dst := filepath.Join(dir, filepath.FromSlash(rel))
		cur, err := os.ReadFile(dst)
		if err == nil && hash(cur) != marks[rel] && hash(cur) != hash(want) {
			continue // edited by the user
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, want, 0o644); err != nil {
			return err
		}
		marks[rel] = hash(want)
	}
	for rel, h := range marks {
		if _, still := files[rel]; still {
			continue
		}
		dst := filepath.Join(dir, filepath.FromSlash(rel))
		if cur, err := os.ReadFile(dst); err == nil && hash(cur) == h {
			_ = os.Remove(dst) // ours and untouched, but not shipped (any more / for this project)
		}
		delete(marks, rel)
	}
	b, _ := json.MarshalIndent(marks, "", "  ")
	return os.WriteFile(markFile, b, 0o644)
}

func hash(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// Run calls "<file>.<export>" with input and waits for its (JSON-able) result: a run of its own (Start) with ctx's lifetime
// only bounding the wait. Pages and the CLI use Start to stream progress.
func (r *Runner) Run(ctx context.Context, project, name string, input any) (any, error) {
	return r.Start(project, name, input).Wait(ctx)
}

// run compiles and runs one function, logging the run (runs.go).
func (r *Runner) run(ctx context.Context, project, name string, input any, id string, emit func(Event)) (res any, err error) {
	emit, finish := r.runLog(project, id, name, emit)
	defer func() { finish(err) }()
	file, fn, ok := strings.Cut(name, ".")
	if !ok || fn == "" || strings.Contains(fn, ".") {
		return nil, fmt.Errorf("function name %q should look like file.export (okr.summary)", name)
	}
	dir, err := r.Ensure(project)
	if err != nil {
		return nil, err
	}
	path, err := source(dir, file)
	if err != nil {
		return nil, err
	}
	code, err := compile(path)
	if err != nil {
		return nil, err
	}
	return r.runCode(ctx, project, dir, filepath.Base(path), fn, code, input, emit)
}

// callMCP unwraps a tools/call result into a plain value: structuredContent if the server sent
// one, otherwise the text content parsed as JSON when it is JSON, else the text itself.
func (r *Runner) callMCP(ctx context.Context, server, tool string, args map[string]any) (any, error) {
	if r.MCP == nil {
		return nil, errors.New("MCP isn't available")
	}
	raw, err := r.MCP(ctx, server, tool, args)
	if err != nil {
		return nil, fmt.Errorf("%s/%s: %w", server, tool, err)
	}
	var res struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StructuredContent any  `json:"structuredContent"`
		IsError           bool `json:"isError"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	var texts []string
	for _, c := range res.Content {
		if c.Type == "text" {
			texts = append(texts, c.Text)
		}
	}
	text := strings.Join(texts, "\n")
	if res.IsError {
		return nil, fmt.Errorf("%s/%s: %s", server, tool, text)
	}
	if res.StructuredContent != nil {
		return res.StructuredContent, nil
	}
	var v any
	if json.Unmarshal([]byte(text), &v) == nil {
		return v, nil
	}
	return text, nil
}

// plain round-trips through JSON so goja-exported values (int64 vs float64, nested maps) serialize cleanly.
func plain(v any) any {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var out any
	_ = json.Unmarshal(b, &out)
	return out
}
