package work

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newProject is a Runner with a default project whose local/ holds the given files.
func newProject(t *testing.T, files map[string]string) (*Runner, string) {
	t.Helper()
	r := New(t.TempDir(), nil)
	dir, err := r.Ensure("")
	if err != nil {
		t.Fatal(err)
	}
	for name, src := range files {
		p := filepath.Join(dir, "local", name)
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return r, dir
}

func run(t *testing.T, r *Runner, fn string, input any) (any, []Event, error) {
	t.Helper()
	rn := r.Start("", fn, input)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := rn.Wait(ctx)
	evs, _, _ := rn.Since(0)
	return res, evs, err
}

func TestExec(t *testing.T) {
	r, dir := newProject(t, map[string]string{"t.ts": `
export async function echo(input, ctx) {
  const a = await ctx.exec('sh', ['-c', 'echo "$1-$X"; echo err >&2; exit 3', '_', input.v], { env: { X: 'y' } })
  const b = await ctx.exec('cat', { input: 'from stdin' })
  const c = await ctx.exec('pwd', [], { cwd: 'local' })
  let missing = ''
  await ctx.exec('no-such-command-xyz').catch((e) => (missing = e.message))
  let outside = ''
  await ctx.exec('ls', [], { cwd: '..' }).catch((e) => (outside = e.message))
  let slow = ''
  await ctx.exec('sleep', ['5'], { timeout: 200 }).catch((e) => (slow = e.message))
  return { a, b: b.stdout, c: c.stdout.trim(), missing, outside, slow }
}`})
	res, _, err := run(t, r, "t.echo", map[string]any{"v": "x"})
	if err != nil {
		t.Fatal(err)
	}
	m := res.(map[string]any)
	a := m["a"].(map[string]any)
	if a["stdout"] != "x-y\n" || a["stderr"] != "err\n" || a["code"] != float64(3) {
		t.Errorf("a = %v", a)
	}
	real, _ := filepath.EvalSymlinks(filepath.Join(dir, "local"))
	if m["b"] != "from stdin" || (m["c"] != filepath.Join(dir, "local") && m["c"] != real) {
		t.Errorf("b = %q c = %q", m["b"], m["c"])
	}
	for _, k := range []string{"missing", "outside", "slow"} {
		if m[k] == "" {
			t.Errorf("%s didn't reject", k)
		}
	}
}

func TestFetchHTMLAndGlobals(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/json":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"hi":"` + req.Header.Get("X-T") + `"}`))
		case "/stream":
			for _, part := range []string{"你", "好"} {
				w.Write([]byte(part))
				w.(http.Flusher).Flush()
			}
		default:
			w.Write([]byte(`<html><body><h1 class="t">Title</h1><script>x()</script><p>a <b>b</b></p></body></html>`))
		}
	}))
	defer srv.Close()
	r, _ := newProject(t, map[string]string{"t.ts": `
export async function go(input, ctx) {
  const j = await (await fetch(input.base + '/json', { headers: { 'X-T': 'there' } })).json()
  const res = await fetch(input.base + '/stream')
  const reader = res.body.getReader(), dec = new TextDecoder()
  let s = ''
  for (;;) { const { done, value } = await reader.read(); if (done) break; s += dec.decode(value, { stream: true }) }
  const [page, bad] = await ctx.fetchAll([input.base + '/page', 'ftp://nope'])
  const doc = ctx.html(await page.text())
  const u = new URL('/a?b=1#c', input.base)
  console.log('hello', { n: 1 })
  ctx.progress({ done: 1, total: 2 })
  await ctx.sleep(1)
  return { j, s, ok: res.ok, h1: doc.find('h1.t')[0].text, text: doc.text(), bad: bad.ok, path: u.pathname, q: u.searchParams.get('b'), b64: atob(btoa('x')), n: (1234.5).toLocaleString('en-US') }
}`})
	res, evs, err := run(t, r, "t.go", map[string]any{"base": srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	m := res.(map[string]any)
	if m["j"].(map[string]any)["hi"] != "there" || m["s"] != "你好" || m["ok"] != true || m["h1"] != "Title" || m["text"] != "Title a b" ||
		m["bad"] != false || m["path"] != "/a" || m["q"] != "1" || m["b64"] != "x" || m["n"] != "1,234.5" {
		t.Errorf("res = %v", m)
	}
	var log, prog bool
	for _, e := range evs {
		d, _ := e.Data.(map[string]any)
		log = log || e.Type == "log" && d["message"] == `hello {"n":1}`
		prog = prog || e.Type == "progress" && d["total"] == int64(2)
	}
	if !log || !prog {
		t.Errorf("events = %v", evs)
	}
}

func TestFS(t *testing.T) {
	r, dir := newProject(t, map[string]string{"t.ts": `
export async function go(input, ctx) {
  const { etag } = ctx.fs.write('data/a.txt', 'one')
  const read = ctx.fs.read('data/a.txt')
  let stale = ''
  try { ctx.fs.write('data/a.txt', 'two', { etag: 'old' }) } catch (e) { stale = e.message }
  ctx.fs.write('data/a.txt', 'two', { etag })
  ctx.fs.copy('data/a.txt', 'data/b.txt')
  ctx.fs.move('data/b.txt', 'data/c.txt')
  let escape = ''
  try { ctx.fs.read('../x') } catch (e) { escape = e.message }
  return { read, stale, escape, list: ctx.fs.list('data').map((e) => e.path), none: ctx.fs.stat('nope'), now: ctx.fs.read('data/c.txt') }
}`})
	res, _, err := run(t, r, "t.go", nil)
	if err != nil {
		t.Fatal(err)
	}
	m := res.(map[string]any)
	if m["read"] != "one" || m["now"] != "two" || m["stale"] == "" || m["escape"] == "" || m["none"] != nil {
		t.Errorf("res = %v", m)
	}
	if l := m["list"].([]any); len(l) != 2 || l[0] != "data/a.txt" || l[1] != "data/c.txt" {
		t.Errorf("list = %v", l)
	}
	if _, err := os.Stat(filepath.Join(dir, "data", "c.txt")); err != nil {
		t.Error(err)
	}
}

func TestAbortLogsListSecrets(t *testing.T) {
	r, _ := newProject(t, map[string]string{
		"t.ts": `import { twice } from './_lib'
export async function slow(input, ctx) { ctx.progress({ step: 1 }); await ctx.exec('sleep', ['20'], { timeout: 60000 }); return 1 }
export async function key(input, ctx) { return [ctx.secrets.get('TOKEN'), ctx.secrets.get('NONE'), twice(2)] }
export async function boom() { throw new Error('bad thing') }`,
		"_lib.ts": `export const twice = (n: number) => n * 2`,
	})
	r.Secret = func(n string) string { return map[string]string{"TOKEN": "s3"}[n] }

	rn := r.Start("", "t.slow", nil)
	time.Sleep(300 * time.Millisecond)
	t0 := time.Now()
	rn.Abort()
	if _, err := rn.Wait(context.Background()); err == nil || time.Since(t0) > 5*time.Second {
		t.Errorf("abort: err=%v after %v", err, time.Since(t0))
	}

	res, _, err := run(t, r, "t.key", nil)
	if err != nil {
		t.Fatal(err)
	}
	if l := res.([]any); l[0] != "s3" || l[1] != nil || l[2] != float64(4) {
		t.Errorf("key = %v", l)
	}
	if _, _, err := run(t, r, "t.boom", nil); err == nil || err.Error() != "bad thing" {
		t.Errorf("boom: %v", err)
	}
	if _, _, err := run(t, r, "_lib.twice", nil); err == nil {
		t.Error("a helper file was callable")
	}

	fns, err := r.List("")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range fns {
		if strings.HasPrefix(f.Name, "t.") || strings.HasPrefix(f.Name, "_") {
			names = append(names, f.Name)
		}
	}
	if got := strings.Join(names, ","); got != "t.boom,t.key,t.slow" {
		t.Errorf("functions = %s", got)
	}

	logs, err := r.Logs("", "t.boom", 10)
	if err != nil || len(logs) != 1 || logs[0].Status != "error" || logs[0].Message != "bad thing" {
		t.Fatalf("logs = %+v, %v", logs, err)
	}
	lines, err := r.LogOf("", logs[0].ID)
	if err != nil || len(lines) < 2 || !strings.Contains(lines[len(lines)-1].Data.(map[string]any)["stack"].(string), "at boom (t.ts:") {
		t.Errorf("log of %s = %+v, %v", logs[0].ID, lines, err)
	}
	all, _ := r.Logs("", "t", 10)
	if len(all) != 3 { // slow, key, boom (the _lib attempt isn't a "t" run)
		t.Errorf("runs of t = %d: %+v", len(all), all)
	}
}
