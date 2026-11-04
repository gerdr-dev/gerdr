package work

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/dop251/goja"
)

// Function runs reuse their VMs. Panel pages call functions every few seconds (a board's list, a log), and building a
// VM with its globals (console, fetch, URL, TextDecoder…) for each run was a large part of what the service allocated.
//
// What makes reuse safe:
//   - The globals are installed once per VM, and their Go closures hold the VM's one runner, whose per-run fields
//     (runnerPerRun) are set at the start of a run and zeroed at its end: a global called during a run always sees
//     that run's ctx / project / emit.
//   - A function's module is loaded afresh each run, wrapped in a function scope (compile.go): its top-level state
//     never outlives the run and never becomes a global.
//   - On release, what the run changed in the shared JS world is put back: every own property of globalThis, of
//     every global object and constructor, of their prototypes and of the intrinsics behind syntax (iterators,
//     generators, %TypedArray%) is restored to its snapshot taken right after install (added ones deleted, changed or
//     deleted ones put back, prototypes reset). The restore uses builtins captured at snapshot time, so code that
//     replaced Reflect or Object.is can't break it.
//   - A VM that can't be trusted is dropped, not pooled: the run was interrupted (stop / timeout) or crashed, or the
//     restore failed (a non-configurable global defined, a builtin frozen or sealed).
//
// Not covered: properties of objects two levels down (console.log.x = 1), and the Symbol.for registry, which no
// script can clear. Neither carries meaning from one run to the next in practice.

// vmPoolMax: idle VMs kept. Runs at the same time each take their own; past this many idle, released ones are dropped.
const vmPoolMax = 4

type pooledVM struct {
	vm      *goja.Runtime
	run     *runner
	restore goja.Callable
}

var vmPool struct {
	mu   sync.Mutex
	free []*pooledVM
}

// vmsMade counts VMs built (tests: a second run must not build one).
var vmsMade atomic.Int64

func newPooledVM() (*pooledVM, error) {
	vm := goja.New()
	vm.SetFieldNameMapper(goja.TagFieldNameMapper("json", true))
	run := &runner{vm: vm}
	run.installGlobals()
	v, err := vm.RunString(snapshotJS)
	if err != nil {
		return nil, err
	}
	restore, ok := goja.AssertFunction(v)
	if !ok {
		return nil, errors.New("snapshot didn't return a function")
	}
	vmsMade.Add(1)
	return &pooledVM{vm: vm, run: run, restore: restore}, nil
}

func takeVM() (*pooledVM, error) {
	vmPool.mu.Lock()
	if n := len(vmPool.free); n > 0 {
		p := vmPool.free[n-1]
		vmPool.free = vmPool.free[:n-1]
		vmPool.mu.Unlock()
		return p, nil
	}
	vmPool.mu.Unlock()
	return newPooledVM()
}

// release ends the run on p and pools p again if clean (no interrupt, no crash) and the restore succeeds.
func (p *pooledVM) release(clean bool) {
	p.run.end()
	if !clean || !p.reset() {
		return
	}
	vmPool.mu.Lock()
	if len(vmPool.free) < vmPoolMax {
		vmPool.free = append(vmPool.free, p)
	}
	vmPool.mu.Unlock()
}

// reset puts the JS world back as it was after install; false if it couldn't (the VM is then dropped).
func (p *pooledVM) reset() (ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	v, err := p.restore(goja.Undefined())
	return err == nil && v.ToBoolean()
}

// runnerPerVM / runnerPerRun: every field of runner is one or the other (pool_test.go checks it, so a new field has to
// be placed). Per-run fields are set by begin and zeroed by end.
var (
	runnerPerVM  = []string{"vm"}
	runnerPerRun = []string{"ctx", "r", "project", "dir", "emit", "depth", "bodies"}
)

func (r *runner) begin(ctx context.Context, rn *Runner, project, dir string, emit func(Event), depth int) {
	r.ctx, r.r, r.project, r.dir, r.emit, r.depth, r.bodies = ctx, rn, project, dir, emit, depth, nil
}

func (r *runner) end() {
	r.cleanup()
	vm := r.vm
	*r = runner{vm: vm}
}

// snapshotJS records the shared JS world and returns the function that restores it (true = all put back).
const snapshotJS = `(function () {
  'use strict';
  const R = Reflect, ownKeys = R.ownKeys, getDesc = R.getOwnPropertyDescriptor, defProp = R.defineProperty,
    delProp = R.deleteProperty, isExt = R.isExtensible, getProto = R.getPrototypeOf, setProto = R.setPrototypeOf,
    is = Object.is;
  const own = Function.prototype.call.bind(Object.prototype.hasOwnProperty);
  const targets = [];
  const add = (o) => {
    if (!((typeof o === 'object' && o !== null) || typeof o === 'function')) return;
    for (let i = 0; i < targets.length; i++) if (targets[i] === o) return;
    targets[targets.length] = o;
  };
  const addWithProto = (v) => {
    add(v);
    if (typeof v === 'function') {
      const d = getDesc(v, 'prototype');
      if (d && own(d, 'value')) add(d.value);
    }
  };
  add(globalThis);
  const gk = ownKeys(globalThis);
  for (let i = 0; i < gk.length; i++) {
    const d = getDesc(globalThis, gk[i]);
    if (d && own(d, 'value')) addWithProto(d.value);
  }
  // Intrinsics reached through syntax, not by name
  const gen = function* () {}; // (goja has no async generators)
  addWithProto(getProto(gen)); add(getProto(gen.prototype));
  addWithProto(getProto(async function () {}));
  const arrIt = getProto([][Symbol.iterator]());
  add(arrIt); add(getProto(arrIt));
  add(getProto(new Map()[Symbol.iterator]())); add(getProto(new Set()[Symbol.iterator]())); add(getProto(''[Symbol.iterator]()));
  addWithProto(getProto(Uint8Array));
  const snap = [];
  for (let i = 0; i < targets.length; i++) {
    const t = targets[i], keys = ownKeys(t), descs = [];
    for (let j = 0; j < keys.length; j++) {
      const d = getDesc(t, keys[j]);
      setProto(d, null);
      descs[j] = d;
    }
    snap[i] = { t: t, keys: keys, descs: descs, proto: getProto(t), ext: isExt(t) };
  }
  // Descriptors are compared with no prototype (the snapshot's, and the current one once read): what a run adds to
  // Object.prototype (a 'get', a 'value') can't change how they read. One call per property: this runs for every
  // property on every release.
  const same = (c, d) => (setProto(c, null), is(c.value, d.value) && c.get === d.get && c.set === d.set &&
    c.writable === d.writable && c.enumerable === d.enumerable && c.configurable === d.configurable);
  return function restore() {
    let ok = true;
    try {
      for (let i = 0; i < snap.length; i++) {
        const s = snap[i], t = s.t;
        if (isExt(t) !== s.ext) ok = false; // frozen / sealed / preventExtensions: can't be undone
        if (getProto(t) !== s.proto && !setProto(t, s.proto)) ok = false;
        const cur = ownKeys(t);
        for (let j = 0; j < cur.length; j++) {
          const k = cur[j];
          let known = false;
          for (let m = 0; m < s.keys.length; m++) if (s.keys[m] === k) { known = true; break; }
          if (!known && !delProp(t, k)) ok = false;
        }
        for (let j = 0; j < s.keys.length; j++) {
          const k = s.keys[j], d = s.descs[j], c = getDesc(t, k);
          if ((c === undefined || !same(c, d)) && !defProp(t, k, d)) ok = false;
        }
      }
    } catch (e) {
      ok = false;
    }
    return ok;
  };
})()`
