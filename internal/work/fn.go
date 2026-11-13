package work

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/MoeForge/moejs"
	"github.com/MoeForge/moejs/engine"
	"github.com/evanw/esbuild/pkg/api"
)

// The function runtime, kept compatible with a hosted function platform's so a function written for one runs on the
// other: same globals (fetch, URL, TextDecoder, btoa, console) and the same ctx members where gerdr has the thing behind
// them. Left out on purpose: ctx.db (business tables), ctx.oauth / upload / file / folders (connections and an
// asset store), ctx.browser (not ported yet), ctx.llm (batch 3).
//
// Every host call finishes synchronously and hands back a settled promise, so `await` works and the function's own
// promise has settled by the time it returns: there is no event loop (no setTimeout; ctx.sleep waits).
//
// The engine is moejs (pure Go; it replaced goja on 2026-11-13: 5× faster runs, a third of the allocations, regular
// expressions that match Node's and stop on an interrupt). Each run gets a new runtime, which costs microseconds: its
// builtins are frozen and shared by every runtime, so nothing a run does to them (or to its globals) reaches the next.

// Event is what a run reports while it goes: progress (ctx.progress) and log (console.*, ctx.log).
type Event struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

// Fn is a callable function: "file.export".
type Fn struct {
	Name string `json:"name"`
	File string `json:"file"`
}

// MCPServer is one entry of ctx.mcp.servers().
type MCPServer struct {
	Name   string `json:"name"`
	Status string `json:"status"` // enabled / disabled
	Source string `json:"source"` // gerdr / claude / codex: where it's configured
}

// File names: lower or upper case letters, digits, - and _, not starting with _ — so `_api.ts` is a shared helper that
// other files import, not something a page can call.
var fileRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

// source finds local/<file>.ts (or .js).
func source(dir, file string) (string, error) {
	if !fileRE.MatchString(file) {
		return "", fmt.Errorf("bad function name: the format is file.export (okr.summary), and files starting with _ are helpers, not callable")
	}
	for _, ext := range []string{".ts", ".js"} {
		p := filepath.Join(dir, "local", file+ext)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("no local/%s.ts", file)
}

// List is every function the project's local/ files export.
func (r *Runner) List(project string) ([]Fn, error) {
	dir, err := r.Ensure(project)
	if err != nil {
		return nil, err
	}
	ents, err := os.ReadDir(filepath.Join(dir, "local"))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	out := []Fn{}
	for _, e := range ents {
		ext := filepath.Ext(e.Name())
		base := strings.TrimSuffix(e.Name(), ext)
		if e.IsDir() || (ext != ".ts" && ext != ".js") || !fileRE.MatchString(base) || strings.HasSuffix(base, ".d") {
			continue
		}
		prog, err := compile(filepath.Join(dir, "local", e.Name()))
		if err != nil {
			continue
		}
		ex, err := exportsOf(prog)
		if err != nil {
			continue
		}
		for k, v := range ex {
			if isFunc(v) {
				out = append(out, Fn{Name: base + "." + k, File: "local/" + e.Name()})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// bundle bundles one local/ file with what it imports (esbuild). Called through compile (compile.go), which caches.
func bundle(path string) (code string, inputs []string, err error) {
	res := api.Build(api.BuildOptions{
		EntryPoints: []string{path},
		Bundle:      true, // relative imports (local/_api.ts) come along
		Format:      api.FormatCommonJS,
		Platform:    api.PlatformNeutral,
		Target:      api.ES2024, // what moejs runs; esbuild lowers anything newer
		Write:       false,
		LogLevel:    api.LogLevelSilent,
		// The files read, so a cached result is reused only while none of them changed
		Metafile:      true,
		AbsWorkingDir: filepath.Dir(path),
	})
	if len(res.Errors) > 0 {
		var msgs []string
		for _, e := range res.Errors {
			loc := ""
			if e.Location != nil {
				loc = fmt.Sprintf("%s:%d:%d ", filepath.Base(e.Location.File), e.Location.Line, e.Location.Column)
			}
			msgs = append(msgs, loc+e.Text)
		}
		return "", nil, errors.New("compile failed: " + strings.Join(msgs, "; "))
	}
	if len(res.OutputFiles) == 0 {
		return "", nil, errors.New("esbuild produced nothing")
	}
	var meta struct {
		Inputs map[string]json.RawMessage `json:"inputs"`
	}
	_ = json.Unmarshal([]byte(res.Metafile), &meta)
	for in := range meta.Inputs {
		if !filepath.IsAbs(in) {
			in = filepath.Join(filepath.Dir(path), in)
		}
		inputs = append(inputs, in)
	}
	return string(res.OutputFiles[0].Contents), inputs, nil
}

// load runs the bundled CommonJS (compile's script: a function of module, exports, require) in rt and returns
// module.exports. Each call is a fresh module: its top-level state starts over every run.
func load(rt *moejs.Runtime, prog *moejs.Script) (jsValue, error) {
	wrapper, err := rt.RunScript(prog)
	if err != nil {
		return moejs.Undefined(), jsError(rt, err)
	}
	module, err := rt.ParseJSONString(`{"exports":{}}`)
	if err != nil {
		return moejs.Undefined(), err
	}
	exports, _ := rt.Get(module, "exports")
	require, _ := rt.FromGo(moejs.NativeFunc(func(realm *moejs.Realm, _ jsValue, args []jsValue) (jsValue, error) {
		name, _ := realm.ToString(moejs.Arg(args, 0))
		return moejs.Undefined(), realm.Throw(engine.ObjectValue(realm.NewError(engine.KindTypeError,
			"local functions can't require(%q): import code from local/ by relative path; what they can do is on ctx", name.GoString())))
	}))
	if _, err := rt.CallFunction(wrapper, moejs.Undefined(), module, exports, require); err != nil {
		return moejs.Undefined(), jsError(rt, err)
	}
	return rt.Get(module, "exports")
}

// exportsOf loads prog in a bare runtime and returns its exports (functions as *moejs.Object): what List / Tools see.
func exportsOf(prog *moejs.Script) (map[string]any, error) {
	rt := moejs.NewRuntime(moejs.Options{})
	ex, err := load(rt, prog)
	if err != nil {
		return nil, err
	}
	g, err := rt.ToGo(ex)
	if err != nil {
		return nil, err
	}
	m, _ := g.(map[string]any)
	return m, nil
}

// isFunc: an exported value (from exportsOf) is a function.
func isFunc(v any) bool {
	o, ok := v.(*moejs.Object)
	return ok && engine.IsCallable(engine.ObjectValue(o))
}

// runner is one run of one function, in a runtime of its own.
type runner struct {
	ctx     context.Context
	rt      *moejs.Runtime
	r       *Runner
	project string
	dir     string // the project's Work directory
	emit    func(Event)
	depth   int          // events this run emits: one deeper than the event that triggered it (trigger.go depthOf)
	bodies  []*fetchBody // fetch response bodies, closed at the end if still open
	getter  *jsValue     // defineGetter's helper, made on first use
}

func (r *Runner) runCode(ctx context.Context, project, dir, file, fn string, prog *moejs.Script, input any, emit func(Event)) (any, error) {
	run := &runner{ctx: ctx, rt: moejs.NewRuntime(moejs.Options{}), r: r, project: project, dir: dir, emit: emit, depth: depthOf(input)}
	defer run.cleanup()
	if err := run.installGlobals(); err != nil {
		return nil, err
	}
	// Stop / timeout: interrupt the JS (a host call blocked in fetch / exec returns too, its ctx is the same)
	stop := context.AfterFunc(ctx, func() { run.rt.Interrupt("stopped") })
	defer stop()
	return run.call(file, fn, prog, input)
}

// call loads the module and calls its export fn with (input, ctx), waiting for a returned promise.
func (run *runner) call(file, fn string, prog *moejs.Script, input any) (any, error) {
	rt := run.rt
	exports, err := load(rt, prog)
	if err != nil {
		return nil, err
	}
	f, err := rt.Get(exports, fn)
	if err != nil || !engine.IsCallable(f) {
		return nil, fmt.Errorf("%s doesn't export a function named %s", file, fn)
	}
	in, err := rt.FromGo(input)
	if err != nil {
		return nil, err
	}
	res, err := rt.CallFunction(f, moejs.Undefined(), in, run.ctxObject())
	if err != nil {
		return nil, jsError(rt, err)
	}
	if st, v, ok := moejs.PromiseResult(res); ok {
		switch st {
		case moejs.PromiseFulfilled:
			res = v
		case moejs.PromiseRejected:
			return nil, jsError(rt, rt.Realm().Throw(v))
		default:
			return nil, errors.New("the function's promise never settled (there's no setTimeout here: to wait, await ctx.sleep(ms))")
		}
	}
	return plain(toPlain(run.toGo(res))), nil
}

func (r *runner) cleanup() {
	for _, b := range r.bodies {
		b.finish()
	}
}

func (r *runner) log(level string) func(args ...jsValue) {
	return func(args ...jsValue) {
		realm := r.rt.Realm()
		parts := make([]string, len(args))
		for i, a := range args {
			if a.IsObject() && a.AsObject().ClassName() != "Error" && !engine.IsCallable(a) {
				b, _ := json.Marshal(toPlain(r.toGo(a)))
				parts[i] = string(b)
			} else if s, err := realm.ToString(a); err == nil {
				parts[i] = s.GoString()
			} else {
				parts[i] = a.String()
			}
		}
		r.emit(Event{Type: "log", Data: map[string]any{"level": level, "message": strings.Join(parts, " ")}})
	}
}

// installGlobals: console, fetch and URL. TextEncoder / TextDecoder (with { stream: true }), atob / btoa and Intl
// (toLocaleString with separators) are moejs's own.
func (r *runner) installGlobals() error {
	rt := r.rt
	console := r.obj(map[string]any{"log": r.log("info"), "info": r.log("info"), "warn": r.log("warn"), "error": r.log("error")})
	if err := rt.SetGlobal("console", console); err != nil {
		return err
	}
	if err := rt.SetGlobal("fetch", r.fn(r.fetch)); err != nil {
		return err
	}
	if err := rt.SetGlobal("__parseURL", r.fn(func(raw, base string) map[string]any {
		u, err := url.Parse(raw)
		if err == nil && base != "" {
			var b *url.URL
			if b, err = url.Parse(base); err == nil {
				u = b.ResolveReference(u)
			}
		}
		if err != nil || u.Scheme == "" || u.Host == "" {
			r.throw(r.typeError("Invalid URL: %s", raw))
		}
		// As in browsers: an http(s) URL with no path has the path /
		if u.Path == "" && u.Opaque == "" && (u.Scheme == "http" || u.Scheme == "https") {
			u.Path, u.RawPath = "/", ""
		}
		q := map[string]any{}
		for k, v := range u.Query() {
			q[k] = v[0]
		}
		search, hash := "", ""
		if u.RawQuery != "" {
			search = "?" + u.RawQuery
		}
		if u.Fragment != "" {
			hash = "#" + u.Fragment
		}
		return map[string]any{"href": u.String(), "protocol": u.Scheme + ":", "host": strings.ToLower(u.Host), "hostname": strings.ToLower(u.Hostname()), "port": u.Port(),
			"pathname": u.EscapedPath(), "search": search, "hash": hash, "origin": u.Scheme + "://" + u.Host, "query": q}
	})); err != nil {
		return err
	}
	_, err := rt.RunScript(urlPolyfill)
	return err
}

var urlPolyfill = mustScript("url.js", `
class URL {
  constructor(u, base) {
    const p = __parseURL(String(u), base === undefined ? '' : String(base))
    Object.assign(this, p)
    const q = p.query
    delete this.query
    this.searchParams = { get: (k) => (k in q ? q[k] : null), has: (k) => k in q }
  }
  toString() { return this.href }
  toJSON() { return this.href }
}
globalThis.URL = URL
`)

func (r *runner) ctxObject() jsValue {
	// ctx.mcp(server, tool, args) is a function with a member (servers), so it's built as an object of its own
	mcp, err := r.rt.FromGo(r.fn(r.mcp))
	if err == nil {
		err = r.mcpServers(mcp)
	}
	if err != nil {
		r.throw(err)
	}
	return r.obj(map[string]any{
		// ctx.caller.session: the conversation that called this function as a Work tool (tools.go); "" otherwise
		"caller": map[string]any{"session": callerOf(r.ctx)},
		// ctx.secrets.get(name): a key the user stored with `gerdr panel secret set <name>` (secrets.json, never in
		// config or transcripts); null when there's none. Pass it to commands through ctx.exec's env.
		"secrets": map[string]any{"get": func(name string) any {
			if r.r.Secret != nil {
				if v := r.r.Secret(name); v != "" {
					return v
				}
			}
			return nil
		}},
		"fetch": func() {
			r.throw(errors.New("there's no ctx.fetch: use the global fetch(url, init)"))
		},
		"fetchAll": r.fetchAll,
		"html":     r.html,
		// await ctx.mcp(server, tool, args): structuredContent, else the text parsed as JSON, else the text; a tool
		// error rejects. ctx.mcp.servers(): [{ name, status, source }] — what's configured, so a page can hide what isn't there
		"mcp": mcp,
		// await ctx.exec(cmd, [args…], { cwd, input, timeout, env }) → { code, stdout, stderr, truncated } (exec.go)
		"exec": r.exec,
		"fs":   r.fsObject(),
		// Handing work to agents (host.go). ctx.workspace (singular, below) is this Work project; ctx.workspaces are Code's.
		"sessions":   r.sessionsObject(),
		"workspaces": r.workspacesObject(),
		"artifacts":  r.artifactsObject(),
		"dirs":       r.dirsObject(),
		// Events (trigger.go, docs/dev/EVENTS.md): what a poll found goes out as events; triggers/ subscribe to them
		"events": r.eventsObject(),
		// ctx.states.get(source, key) → { state, detail, url, at } | null: what a source last said one thing is like
		// (docs/dev/EVENTS.md, "Waiting for a state"); to wait on it, write a trigger with until
		"states": map[string]any{"get": func(source, key string) any {
			if r.r.States == nil {
				r.throw(errors.New("ctx.states: not available here (needs Gerdr's server)"))
			}
			st, ok := r.r.States(source, key)
			if !ok {
				return nil
			}
			return map[string]any{"state": st.State, "detail": st.Detail, "url": st.URL, "at": st.At}
		}},
		// What on this panel needs the user, in Gerdr's inbox and the phone's notifications (inbox.go)
		"inbox":     r.inboxObject(),
		"workspace": map[string]any{"project": r.project, "dir": r.dir, "logs_dir": r.r.logDir(r.project)},
		// gerdr's interface is Chinese: messages meant for the user can follow this
		"locale": "zh",
		"progress": func(v jsValue) {
			r.emit(Event{Type: "progress", Data: toPlain(r.toGo(v))})
		},
		"log": r.log("info"),
		"sleep": func(ms int64) *promise {
			select {
			case <-time.After(time.Duration(min(max(ms, 0), 60_000)) * time.Millisecond):
			case <-r.ctx.Done():
				r.throw(r.ctx.Err())
			}
			return r.resolved(moejs.Undefined())
		},
	})
}

// mcp is ctx.mcp(server, tool, args).
func (r *runner) mcp(server, tool string, args jsValue) *promise {
	var a map[string]any
	if !args.IsNullish() {
		// Through JSON, as the server will see them (numbers as float64, not int64)
		m, ok := plain(toPlain(r.toGo(args))).(map[string]any)
		if !ok {
			return r.rejected(errors.New("ctx.mcp's third argument must be an object: ctx.mcp('server', 'tool', { … })"))
		}
		a = m
	}
	v, err := r.r.callMCP(r.ctx, server, tool, a)
	if err != nil {
		return r.rejected(err)
	}
	return r.resolved(v)
}

// mcpServers sets ctx.mcp.servers on the ctx.mcp function.
func (r *runner) mcpServers(mcp jsValue) error {
	servers, err := r.rt.FromGo(r.fn(func() []map[string]any {
		out := []map[string]any{}
		if r.r.MCPServers != nil {
			for _, s := range r.r.MCPServers() {
				out = append(out, map[string]any{"name": s.Name, "status": s.Status, "source": s.Source})
			}
		}
		return out
	}))
	if err != nil {
		return err
	}
	realm := r.rt.Realm()
	_, err = mcp.AsObject().Set(realm, engine.StringKey(realm.InternGoString("servers")), servers, mcp)
	return err
}

func intOf(v any, def int) int {
	switch x := v.(type) {
	case int64:
		return int(x)
	case float64:
		return int(x)
	case int:
		return x
	}
	return def
}

// Error is an exception the function threw: Message for the page, Stack for the CLI and the run log.
type Error struct {
	Message string
	Stack   string
}

func (e *Error) Error() string { return e.Message }

// jsError turns a JS exception into an error with the message and the stack apart.
func jsError(rt *moejs.Runtime, err error) error {
	var ex *moejs.Exception
	var ie *moejs.InterruptedError
	switch {
	case errors.As(err, &ie):
		return fmt.Errorf("%v", ie.Value)
	case !errors.As(err, &ex):
		return err
	}
	v := ex.Value
	if v.IsObject() {
		e := &Error{Message: ex.Message(), Stack: rt.StackTrace(ex)}
		if e.Message == "" {
			if s, err := rt.Realm().ToString(v); err == nil {
				e.Message = s.GoString()
			}
		}
		e.Message = cleanMessage(e.Message)
		return e
	}
	if s, err := rt.Realm().ToString(v); err == nil {
		return errors.New(cleanMessage(s.GoString()))
	}
	return errors.New(cleanMessage(ex.Error()))
}

// What a Go error thrown into JS used to carry under goja (a "GoError: " prefix and the Go call site); kept for
// messages a script built from one and stored.
var goErrorNoise = regexp.MustCompile(`(?:GoError: )|(?:\s+at [\w./()*-]+ \(native\))`)

func cleanMessage(m string) string { return strings.TrimSpace(goErrorNoise.ReplaceAllString(m, "")) }

// toPlain turns what ToGo gives into JSON-able values (functions, promises and other engine objects become null).
func toPlain(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, vv := range x {
			out[k] = toPlain(vv)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, vv := range x {
			out[i] = toPlain(vv)
		}
		return out
	case *moejs.Object:
		return nil
	default:
		if x != nil && reflect.TypeOf(x).Kind() == reflect.Func {
			return nil
		}
		return x
	}
}
