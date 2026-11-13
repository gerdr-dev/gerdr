package work

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func mustRun(t *testing.T, r *Runner, fn string, input any) (any, []Event) {
	t.Helper()
	res, evs, err := run(t, r, fn, input)
	if err != nil {
		t.Fatalf("%s: %v", fn, err)
	}
	return res, evs
}

// A module's top-level state is the run's own: a counter at module level starts over every run, and its `var`s
// don't become globals.
func TestModuleStateFresh(t *testing.T) {
	r, _ := newProject(t, map[string]string{"t.ts": `
var topVar = 'mine'
function topFn() {}
let n = 0
export function inc() { n++; return { n, global: typeof globalThis.topVar + '/' + typeof globalThis.topFn } }`})
	for i := 0; i < 3; i++ {
		res, _ := mustRun(t, r, "t.inc", nil)
		m := res.(map[string]any)
		if m["n"] != float64(1) {
			t.Fatalf("run %d: module counter %v, want 1 (module state carried over)", i, m["n"])
		}
		if m["global"] != "undefined/undefined" {
			t.Fatalf("module-level declarations became globals: %v", m["global"])
		}
	}
}

// Builtins are frozen and shared by every runtime: changing one throws (functions are strict), and what a run does
// to its own globals never reaches the next run.
func TestRunsIsolated(t *testing.T) {
	r, _ := newProject(t, map[string]string{
		"dirty.ts": `
export function dirty() {
  globalThis.leak = 1
  console.log = () => {}
  globalThis.URL = null
  const tries = {
    proto: () => { Object.prototype.polluted = 'yes' },
    map: () => { Array.prototype.map = () => 'broken map' },
    parse: () => { JSON.parse = () => 'broken parse' },
    btoa: () => { globalThis.btoa = null },
  }
  const threw = []
  for (const k in tries) { try { tries[k]() } catch (e) { threw.push(k + ':' + e.name) } }
  return threw.join(',')
}`,
		"check.ts": `
export async function check() {
  console.log('from check')
  return {
    leak: typeof globalThis.leak,
    polluted: ({}).polluted === undefined,
    map: [1, 2].map((x) => x * 2).join(','),
    parse: JSON.parse('{"a":1}').a,
    btoa: btoa('a'),
    url: new URL('https://x.y/p?q=1').searchParams.get('q'),
    then: await Promise.resolve(7),
  }
}`,
	})
	res, _ := mustRun(t, r, "dirty.dirty", nil)
	if res != "proto:TypeError,map:TypeError,parse:TypeError,btoa:TypeError" {
		t.Fatalf("writing to a builtin should throw a TypeError: %v", res)
	}
	got, evs := mustRun(t, r, "check.check", nil)
	want := map[string]any{"leak": "undefined", "polluted": true, "map": "2,4", "parse": float64(1), "btoa": "YQ==", "url": "1", "then": float64(7)}
	for k, w := range want {
		if fmt.Sprint(got.(map[string]any)[k]) != fmt.Sprint(w) {
			t.Errorf("%s: got %v, want %v (left over from the previous run)", k, got.(map[string]any)[k], w)
		}
	}
	logged := false
	for _, e := range evs {
		if e.Type == "log" && strings.Contains(fmt.Sprint(e.Data), "from check") {
			logged = true
		}
	}
	if !logged {
		t.Error("the check run's log never arrived")
	}
}

// Stop interrupts a run stuck in JS: a loop, and a regular expression backtracking for ever (goja's regexp2
// matches ignored the interrupt).
func TestStopStuckRun(t *testing.T) {
	r, _ := newProject(t, map[string]string{"t.ts": `
export function spin() { for (;;) {} }
export function regex() { return /^(?=a)(a+)+$/.test('a'.repeat(40) + 'b') }`})
	for _, fn := range []string{"t.spin", "t.regex"} {
		rn := r.Start("", fn, nil)
		time.Sleep(50 * time.Millisecond)
		rn.Abort()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, err := rn.Wait(ctx)
		timedOut := ctx.Err() != nil
		cancel()
		if err == nil || timedOut {
			t.Fatalf("%s wasn't stopped: %v", fn, err)
		}
	}
}

// Runs at the same time each have their own runtime: one run's globals never show in another, and a global (console)
// called during a run talks to that run.
func TestConcurrentRuns(t *testing.T) {
	r, _ := newProject(t, map[string]string{"t.ts": `
export async function mark(input, ctx) {
  if (globalThis.owner !== undefined) return 'saw ' + globalThis.owner
  globalThis.owner = input.n
  console.log('run ' + input.n)
  await ctx.sleep(5)
  return globalThis.owner === input.n ? 'ok' : 'clobbered by ' + globalThis.owner
}`})
	var wg sync.WaitGroup
	errs := make(chan string, 256)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 4; j++ {
				n := i*10 + j
				res, evs, err := run(t, r, "t.mark", map[string]any{"n": n})
				if err != nil || res != "ok" {
					errs <- fmt.Sprintf("run %d: %v %v", n, res, err)
				}
				var logs []string
				for _, e := range evs {
					if e.Type == "log" {
						logs = append(logs, fmt.Sprint(e.Data))
					}
				}
				if len(logs) != 1 || !strings.Contains(logs[0], fmt.Sprintf("run %d", n)) {
					errs <- fmt.Sprintf("run %d got logs %v", n, logs)
				}
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}
