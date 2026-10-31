package work

import (
	"context"
	"encoding/base64"
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

	"github.com/dop251/goja"
	"github.com/evanw/esbuild/pkg/api"
)

// The function runtime, ported from Annulo's (../shuttle internal/localfn) so a function written for one runs on the
// other: same globals (fetch, URL, TextDecoder, btoa, console) and the same ctx members where gerdr has the thing behind
// them. Left out on purpose: ctx.db (Annulo's business tables), ctx.oauth / upload / file / folders (its connections and
// asset store), ctx.browser (not ported yet), ctx.llm (batch 3).
//
// Every host call finishes synchronously and hands back a settled promise, so `await` works and the function's own
// promise has settled by the time it returns: there is no event loop (no setTimeout; ctx.sleep waits).

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
// other files import, not something a page can call (the same rule as Annulo's).
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
		code, err := compile(filepath.Join(dir, "local", e.Name()))
		if err != nil {
			continue
		}
		vm := goja.New()
		exports, err := load(vm, e.Name(), code)
		if err != nil {
			continue
		}
		for _, k := range exports.Keys() {
			if _, ok := goja.AssertFunction(exports.Get(k)); ok {
				out = append(out, Fn{Name: base + "." + k, File: "local/" + e.Name()})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func compile(path string) (string, error) {
	res := api.Build(api.BuildOptions{
		EntryPoints: []string{path},
		Bundle:      true, // relative imports (local/_api.ts) come along
		Format:      api.FormatCommonJS,
		Platform:    api.PlatformNeutral,
		Target:      api.ES2017, // goja runs async/await; esbuild lowers ?. ?? and friends
		Write:       false,
		LogLevel:    api.LogLevelSilent,
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
		return "", errors.New("compile failed: " + strings.Join(msgs, "; "))
	}
	if len(res.OutputFiles) == 0 {
		return "", errors.New("esbuild produced nothing")
	}
	return string(res.OutputFiles[0].Contents), nil
}

// load runs the bundled CommonJS and returns module.exports.
// name labels the script in stacks (the bundled code: line numbers count the imports bundled in before the file).
func load(vm *goja.Runtime, name, code string) (*goja.Object, error) {
	module := vm.NewObject()
	exports := vm.NewObject()
	_ = module.Set("exports", exports)
	_ = vm.Set("module", module)
	_ = vm.Set("exports", exports)
	_ = vm.Set("require", func(name string) goja.Value {
		panic(vm.NewTypeError("local functions can't require(%q): import code from local/ by relative path; what they can do is on ctx", name))
	})
	if _, err := vm.RunScript(name, code); err != nil {
		return nil, jsError(err)
	}
	return module.Get("exports").ToObject(vm), nil
}

// runner is one run of one function.
type runner struct {
	ctx     context.Context
	vm      *goja.Runtime
	r       *Runner
	project string
	dir     string // the project's Work directory
	emit    func(Event)
	bodies  []*fetchBody // fetch response bodies, closed at the end if still open
}

func (r *Runner) runCode(ctx context.Context, project, dir, file, fn, code string, input any, emit func(Event)) (any, error) {
	vm := goja.New()
	vm.SetFieldNameMapper(goja.TagFieldNameMapper("json", true))
	// Stop / timeout: interrupt the JS (a host call blocked in fetch / exec returns too, its ctx is the same)
	stop := context.AfterFunc(ctx, func() { vm.Interrupt("stopped") })
	defer stop()
	run := &runner{ctx: ctx, vm: vm, r: r, project: project, dir: dir, emit: emit}
	defer run.cleanup()
	run.installGlobals()
	exports, err := load(vm, file, code)
	if err != nil {
		return nil, err
	}
	f, ok := goja.AssertFunction(exports.Get(fn))
	if !ok {
		return nil, fmt.Errorf("%s doesn't export a function named %s", file, fn)
	}
	res, err := f(goja.Undefined(), vm.ToValue(input), run.ctxObject())
	if err != nil {
		return nil, jsError(err)
	}
	if p, ok := res.Export().(*goja.Promise); ok {
		switch p.State() {
		case goja.PromiseStateFulfilled:
			res = p.Result()
		case goja.PromiseStateRejected:
			return nil, jsError(nil, p.Result())
		default:
			return nil, errors.New("the function's promise never settled (there's no setTimeout here: to wait, await ctx.sleep(ms))")
		}
	}
	return plain(toPlain(res.Export())), nil
}

func (r *runner) cleanup() {
	for _, b := range r.bodies {
		b.finish()
	}
}

func (r *runner) throw(err error) { panic(r.vm.NewGoError(err)) }

// rejected is a failed promise. Host calls that return a promise fail this way rather than throwing synchronously,
// so ctx.mcp(…).catch(…) catches it, as it would an async function's.
func (r *runner) rejected(err error) *goja.Promise {
	p, _, reject := r.vm.NewPromise()
	_ = reject(r.vm.NewGoError(err))
	return p
}

func (r *runner) resolved(v any) *goja.Promise {
	p, resolve, _ := r.vm.NewPromise()
	_ = resolve(v)
	return p
}

func (r *runner) log(level string) func(args ...goja.Value) {
	return func(args ...goja.Value) {
		parts := make([]string, len(args))
		for i, a := range args {
			if o, ok := a.(*goja.Object); ok && o.ClassName() != "Error" {
				b, _ := json.Marshal(toPlain(o.Export()))
				parts[i] = string(b)
			} else {
				parts[i] = a.String()
			}
		}
		r.emit(Event{Type: "log", Data: map[string]any{"level": level, "message": strings.Join(parts, " ")}})
	}
}

func (r *runner) installGlobals() {
	console := r.vm.NewObject()
	_ = console.Set("log", r.log("info"))
	_ = console.Set("info", r.log("info"))
	_ = console.Set("warn", r.log("warn"))
	_ = console.Set("error", r.log("error"))
	_ = r.vm.Set("console", console)
	_ = r.vm.Set("fetch", r.fetch)
	r.installText()
	_ = r.vm.Set("__parseURL", func(raw, base string) map[string]any {
		u, err := url.Parse(raw)
		if err == nil && base != "" {
			var b *url.URL
			if b, err = url.Parse(base); err == nil {
				u = b.ResolveReference(u)
			}
		}
		if err != nil || u.Scheme == "" || u.Host == "" {
			panic(r.vm.NewTypeError("Invalid URL: %s", raw))
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
	})
	_ = r.vm.Set("btoa", func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) })
	_ = r.vm.Set("atob", func(s string) string {
		b, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			panic(r.vm.NewTypeError("atob: %v", err))
		}
		return string(b)
	})
	if _, err := r.vm.RunString(urlPolyfill); err != nil {
		panic(err)
	}
}

const urlPolyfill = `
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
// goja's Number.prototype.toLocaleString takes its first argument as a radix (toLocaleString('en-US') throws): replace it
// with en-US thousands separators, honouring minimumFractionDigits / maximumFractionDigits (at most 3 decimals by default)
Object.defineProperty(Number.prototype, 'toLocaleString', {
  configurable: true, writable: true,
  value: function (locales, opts) {
    const n = Number(this)
    if (!isFinite(n)) return String(n)
    const o = opts && typeof opts === 'object' ? opts : {}
    const min = Math.min(20, Math.max(0, Math.floor(Number(o.minimumFractionDigits) || 0)))
    const max = Math.min(20, Math.max(min, o.maximumFractionDigits === undefined ? 3 : Math.floor(Number(o.maximumFractionDigits) || 0)))
    let [int, frac = ''] = Math.abs(n).toFixed(max).split('.')
    frac = frac.replace(/0+$/, '')
    while (frac.length < min) frac += '0'
    const zero = /^0*$/.test(int) && /^0*$/.test(frac)
    return (n < 0 && !zero ? '-' : '') + int.replace(/\B(?=(\d{3})+(?!\d))/g, ',') + (frac ? '.' + frac : '')
  },
})
`

func (r *runner) ctxObject() *goja.Object {
	c := r.vm.NewObject()

	secrets := r.vm.NewObject()
	// ctx.secrets.get(name): a key the user stored with `gerdr work secret set <name>` (secrets.json, never in config or
	// transcripts); null when there's none. Pass it to commands through ctx.exec's env.
	_ = secrets.Set("get", func(name string) any {
		if r.r.Secret != nil {
			if v := r.r.Secret(name); v != "" {
				return v
			}
		}
		return nil
	})
	_ = c.Set("secrets", secrets)

	_ = c.Set("fetch", func() {
		r.throw(errors.New("there's no ctx.fetch: use the global fetch(url, init)"))
	})
	_ = c.Set("fetchAll", r.fetchAll)
	_ = c.Set("html", r.html)

	// await ctx.mcp(server, tool, args): structuredContent, else the text parsed as JSON, else the text; a tool error rejects.
	// ctx.mcp.servers(): [{ name, status, source }] — what's configured, so a page can hide what isn't there
	mcpFn := r.vm.ToValue(func(server, tool string, args goja.Value) *goja.Promise {
		var a map[string]any
		if args != nil && !goja.IsUndefined(args) && !goja.IsNull(args) {
			// Through JSON, as the server will see them (numbers as float64, not goja's int64)
			m, ok := plain(toPlain(args.Export())).(map[string]any)
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
	}).(*goja.Object)
	_ = mcpFn.Set("servers", func() []map[string]any {
		out := []map[string]any{}
		if r.r.MCPServers != nil {
			for _, s := range r.r.MCPServers() {
				out = append(out, map[string]any{"name": s.Name, "status": s.Status, "source": s.Source})
			}
		}
		return out
	})
	_ = c.Set("mcp", mcpFn)

	// await ctx.exec(cmd, [args…], { cwd, input, timeout, env }) → { code, stdout, stderr, truncated } (exec.go)
	_ = c.Set("exec", r.exec)
	_ = c.Set("fs", r.fsObject())
	// Handing work to agents (host.go). ctx.workspace (singular, below) is this Work project; ctx.workspaces are Code's.
	_ = c.Set("sessions", r.sessionsObject())
	_ = c.Set("workspaces", r.workspacesObject())
	_ = c.Set("dirs", r.dirsObject())
	_ = c.Set("workspace", map[string]any{"project": r.project, "dir": r.dir, "logs_dir": r.r.logDir(r.project)})
	// gerdr's interface is Chinese: messages meant for the user can follow this
	_ = c.Set("locale", "zh")
	_ = c.Set("progress", func(v goja.Value) {
		r.emit(Event{Type: "progress", Data: toPlain(v.Export())})
	})
	_ = c.Set("log", r.log("info"))
	_ = c.Set("sleep", func(ms int64) *goja.Promise {
		select {
		case <-time.After(time.Duration(min(max(ms, 0), 60_000)) * time.Millisecond):
		case <-r.ctx.Done():
			r.throw(r.ctx.Err())
		}
		return r.resolved(goja.Undefined())
	})
	return c
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
func jsError(err error, vals ...goja.Value) error {
	var v goja.Value
	switch {
	case len(vals) > 0:
		v = vals[0]
	default:
		var ex *goja.Exception
		var ie *goja.InterruptedError
		switch {
		case errors.As(err, &ex):
			v = ex.Value()
			if v == nil {
				return errors.New(ex.String())
			}
		case errors.As(err, &ie):
			return fmt.Errorf("%v", ie.Value())
		default:
			return err
		}
	}
	if o, ok := v.(*goja.Object); ok {
		e := &Error{}
		if m := o.Get("message"); m != nil && !goja.IsUndefined(m) {
			e.Message = m.String()
		}
		if st := o.Get("stack"); st != nil && !goja.IsUndefined(st) {
			e.Stack = st.String()
		}
		if e.Message == "" {
			e.Message = v.String()
		}
		e.Message = cleanMessage(e.Message)
		return e
	}
	if v != nil {
		return errors.New(cleanMessage(v.String()))
	}
	return err
}

// What a Go error thrown into JS carries when turned into a string (String(e), `${e}`): a "GoError: " prefix and the Go
// call site. Scripts often wrap and rethrow it; the page should only see the sentence.
var goErrorNoise = regexp.MustCompile(`(?:GoError: )|(?:\s+at [\w./()*-]+ \(native\))`)

func cleanMessage(m string) string { return strings.TrimSpace(goErrorNoise.ReplaceAllString(m, "")) }

// toPlain turns what goja exports into JSON-able values (functions and promises become null).
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
	case *goja.Promise:
		return nil
	default:
		if x != nil && reflect.TypeOf(x).Kind() == reflect.Func {
			return nil
		}
		return x
	}
}
