package work

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// emptyPool drops idle VMs so a test starts from a known pool.
func emptyPool() {
	vmPool.mu.Lock()
	vmPool.free = nil
	vmPool.mu.Unlock()
}

// made runs f and returns how many VMs it built.
func made(f func()) int64 {
	before := vmsMade.Load()
	f()
	return vmsMade.Load() - before
}

func mustRun(t *testing.T, r *Runner, fn string, input any) (any, []Event) {
	t.Helper()
	res, evs, err := run(t, r, fn, input)
	if err != nil {
		t.Fatalf("%s: %v", fn, err)
	}
	return res, evs
}

// Every field of runner is either the VM's (kept) or the run's (set by begin, zeroed by end): a new field has to be
// put in one of the two lists in pool.go, or this fails.
func TestRunnerFieldsClassified(t *testing.T) {
	var fields []string
	rt := reflect.TypeOf(runner{})
	for i := 0; i < rt.NumField(); i++ {
		fields = append(fields, rt.Field(i).Name)
	}
	listed := append(append([]string{}, runnerPerVM...), runnerPerRun...)
	sort.Strings(fields)
	sort.Strings(listed)
	if strings.Join(fields, ",") != strings.Join(listed, ",") {
		t.Fatalf("runner fields %v, but pool.go lists %v: say whether a new field is per VM or per run", fields, listed)
	}

	p, err := newPooledVM()
	if err != nil {
		t.Fatal(err)
	}
	r := New(t.TempDir(), nil)
	p.run.begin(context.Background(), r, "proj", "/dir", func(Event) {}, 3)
	p.run.bodies = append(p.run.bodies, &fetchBody{})
	v := reflect.ValueOf(p.run).Elem()
	for _, name := range runnerPerRun {
		if v.FieldByName(name).IsZero() {
			t.Fatalf("begin left %s unset: the test should set every per-run field", name)
		}
	}
	p.run.end()
	for _, name := range runnerPerRun {
		if !v.FieldByName(name).IsZero() {
			t.Fatalf("end left %s from the run that ended", name)
		}
	}
	if p.run.vm != p.vm {
		t.Fatal("end dropped the VM")
	}
}

func TestPoolReusesVM(t *testing.T) {
	emptyPool()
	r, _ := newProject(t, map[string]string{"t.ts": `export function f() { return 1 }`})
	if n := made(func() {
		mustRun(t, r, "t.f", nil)
		mustRun(t, r, "t.f", nil)
		mustRun(t, r, "t.f", nil)
	}); n != 1 {
		t.Fatalf("three runs in a row built %d VMs, want 1", n)
	}
}

// A module's top-level state is the run's own: a counter at module level starts over every run, and its `var`s
// don't become globals.
func TestPoolModuleStateFresh(t *testing.T) {
	emptyPool()
	r, _ := newProject(t, map[string]string{"t.ts": `
var topVar = 'mine'
function topFn() {}
let n = 0
export function inc() { n++; return { n, global: typeof globalThis.topVar + '/' + typeof globalThis.topFn } }`})
	for i := 0; i < 3; i++ {
		res, _ := mustRun(t, r, "t.inc", nil)
		m := res.(map[string]any)
		if m["n"] != float64(1) && m["n"] != int64(1) {
			t.Fatalf("run %d: module counter %v, want 1 (module state carried over)", i, m["n"])
		}
		if m["global"] != "undefined/undefined" {
			t.Fatalf("module-level declarations became globals: %v", m["global"])
		}
	}
}

// Whatever a run does to the shared JS world, the next run in the same VM sees it as it was installed.
func TestPoolRestoresJSWorld(t *testing.T) {
	emptyPool()
	r, _ := newProject(t, map[string]string{
		"dirty.ts": `
export function dirty() {
  globalThis.leak = 1
  Object.prototype.polluted = 'yes'
  Array.prototype.extra = () => 1
  Array.prototype.map = () => 'broken map'
  String.prototype.trim = () => 'broken trim'
  Number.prototype.toLocaleString = () => 'broken toLocaleString'
  Promise.prototype.then = function () { throw new Error('broken then') }
  JSON.parse = () => 'broken parse'
  Math.random = () => 4
  Math.extra = 1
  console.log = () => {}
  delete globalThis.btoa
  globalThis.URL = null
  Object.setPrototypeOf(Math, { inherited: true })
  Object.getPrototypeOf([][Symbol.iterator]()).next = () => ({ done: true })
  Object.getPrototypeOf(function* () {}).prototype.next = () => ({ done: true })
  // Trying to break the restore itself
  Object.prototype.get = function () { return 1 }
  Object.prototype.value = 5
  Object.prototype.writable = true
  Reflect.ownKeys = () => []
  Reflect.defineProperty = () => true
  Object.is = () => true
  return 'dirty'
}`,
		"check.ts": `
export async function check() {
  const out = {}
  out.leak = typeof globalThis.leak
  out.polluted = ({}).polluted === undefined
  out.extra = [].extra === undefined
  out.map = [1, 2].map((x) => x * 2).join(',')
  out.trim = ' a '.trim()
  out.locale = (1234.5).toLocaleString()
  out.then = await Promise.resolve(7)
  out.parse = JSON.parse('{"a":1}').a
  out.random = Math.random() < 1
  out.mathExtra = Math.extra === undefined && Math.inherited === undefined
  out.btoa = typeof btoa === 'function' && btoa('a') === 'YQ=='
  out.url = new URL('https://x.y/p?q=1').searchParams.get('q')
  out.spread = [...[1, 2, 3]].length
  out.gen = [...(function* () { yield 1; yield 2 })()].length
  out.keys = Reflect.ownKeys({ a: 1 }).length
  out.is = Object.is(1, 2)
  out.protoKeys = ['get', 'value', 'writable'].filter((k) => k in {}).join(',')
  console.log('from check')
  return out
}`,
	})
	if n := made(func() {
		mustRun(t, r, "dirty.dirty", nil)
		res, evs := mustRun(t, r, "check.check", nil)
		want := map[string]any{"leak": "undefined", "polluted": true, "extra": true, "map": "2,4", "trim": "a", "locale": "1,234.5",
			"then": int64(7), "parse": int64(1), "random": true, "mathExtra": true, "btoa": true, "url": "1", "spread": int64(3), "gen": int64(2),
			"keys": int64(1), "is": false, "protoKeys": ""}
		got := res.(map[string]any)
		for k, w := range want {
			if fmt.Sprint(got[k]) != fmt.Sprint(w) {
				t.Errorf("%s: got %v, want %v (left over from the previous run)", k, got[k], w)
			}
		}
		logged := false
		for _, e := range evs {
			if e.Type == "log" && strings.Contains(fmt.Sprint(e.Data), "from check") {
				logged = true
			}
		}
		if !logged {
			t.Error("console.log replaced by the previous run: the check run's log never arrived")
		}
	}); n != 1 {
		t.Fatalf("built %d VMs, want 1: the dirty VM should have been restored and reused", n)
	}
}

// A global called during a run talks to that run: its logs go to its own events, not to the run before.
func TestPoolGlobalsSeeCurrentRun(t *testing.T) {
	emptyPool()
	r, _ := newProject(t, map[string]string{"t.ts": `export function say(input) { console.log('run ' + input.n); return input.n }`})
	for i := 1; i <= 3; i++ {
		_, evs := mustRun(t, r, "t.say", map[string]any{"n": i})
		var logs []string
		for _, e := range evs {
			if e.Type == "log" {
				logs = append(logs, fmt.Sprint(e.Data))
			}
		}
		if len(logs) != 1 || !strings.Contains(logs[0], fmt.Sprintf("run %d", i)) {
			t.Fatalf("run %d got logs %v", i, logs)
		}
	}
}

// A run that throws leaves a usable, clean VM: pooled and restored.
func TestPoolAfterThrow(t *testing.T) {
	emptyPool()
	r, _ := newProject(t, map[string]string{"t.ts": `
export function boom() { globalThis.left = 1; throw new Error('boom') }
export function look() { return typeof globalThis.left }`})
	if n := made(func() {
		if _, _, err := run(t, r, "t.boom", nil); err == nil || !strings.Contains(err.Error(), "boom") {
			t.Fatalf("want boom, got %v", err)
		}
		if res, _ := mustRun(t, r, "t.look", nil); res != "undefined" {
			t.Fatalf("global from the thrown run still there: %v", res)
		}
	}); n != 1 {
		t.Fatalf("built %d VMs, want 1", n)
	}
}

// Not trusted again: a VM whose run was interrupted, or whose changes can't be undone, is dropped.
func TestPoolDropsUntrusted(t *testing.T) {
	emptyPool()
	r, _ := newProject(t, map[string]string{"t.ts": `
export function spin() { for (;;) {} }
export function stuck() { Object.defineProperty(globalThis, 'stuck', { value: 1, configurable: false }); return 1 }
export function frozen() { Object.freeze(Math); return 1 }
export function sealedIn() { Object.preventExtensions(JSON); return 1 }
export function look() { return typeof globalThis.stuck + '/' + Object.isFrozen(Math) + '/' + Object.isExtensible(JSON) }`})

	// Interrupted
	n := made(func() {
		rn := r.Start("", "t.spin", nil)
		time.Sleep(50 * time.Millisecond)
		rn.Abort()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := rn.Wait(ctx); err == nil {
			t.Fatal("spin wasn't stopped")
		}
		mustRun(t, r, "t.look", nil)
	})
	if n != 2 {
		t.Fatalf("after an interrupted run: built %d VMs, want 2 (the interrupted one dropped)", n)
	}

	for _, fn := range []string{"t.stuck", "t.frozen", "t.sealedIn"} {
		emptyPool()
		n := made(func() {
			mustRun(t, r, fn, nil)
			if res, _ := mustRun(t, r, "t.look", nil); res != "undefined/false/true" {
				t.Fatalf("after %s the next run saw %v", fn, res)
			}
		})
		if n != 2 {
			t.Fatalf("after %s: built %d VMs, want 2 (the one it changed for good dropped)", fn, n)
		}
	}
}

// Runs at the same time each have their own VM: one run's globals never show in another.
func TestPoolConcurrentRuns(t *testing.T) {
	emptyPool()
	r, _ := newProject(t, map[string]string{"t.ts": `
export async function mark(input, ctx) {
  if (globalThis.owner !== undefined) return 'saw ' + globalThis.owner
  globalThis.owner = input.n
  await ctx.sleep(5)
  return globalThis.owner === input.n ? 'ok' : 'clobbered by ' + globalThis.owner
}`})
	var wg sync.WaitGroup
	errs := make(chan string, 64)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 4; j++ {
				res, _, err := run(t, r, "t.mark", map[string]any{"n": i*10 + j})
				if err != nil || res != "ok" {
					errs <- fmt.Sprintf("run %d/%d: %v %v", i, j, res, err)
				}
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
	vmPool.mu.Lock()
	idle := len(vmPool.free)
	vmPool.mu.Unlock()
	if idle > vmPoolMax {
		t.Fatalf("%d idle VMs kept, max %d", idle, vmPoolMax)
	}
}
