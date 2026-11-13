package work

import (
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/MoeForge/moejs"
)

// compile bundles a local/ file (esbuild) and compiles it for moejs, reusing the last result for that file while
// none of the files it was built from changed (size and modification time of each). Panels' pages poll functions
// (tasks.list, a log…) every few seconds: bundling and compiling on every call was ~1 run a second, a third of the
// service's allocations, and the GC that came with them kept it busy. A moejs Script isn't tied to a runtime, so
// each run's own runtime can run the shared one.
func compile(path string) (*moejs.Script, error) {
	compiled.mu.Lock()
	c := compiled.m[path]
	compiled.mu.Unlock()
	if c != nil && c.fresh() {
		return c.prog, nil
	}
	code, inputs, err := bundle(path)
	if err != nil {
		return nil, err
	}
	// Wrapped in a function, as Node does, with module.exports for the bundle's CommonJS. Strict, as the ES module
	// it was written as: assigning to a frozen builtin (moejs shares them between runtimes) throws instead of doing
	// nothing. Same line numbers: the wrapper opens on the first line.
	prog, err := moejs.CompileScript(filepath.Base(path), "(function (module, exports, require) {'use strict';"+code+"\n})")
	if err != nil {
		return nil, err
	}
	c = &compiledFile{prog: prog, stamps: map[string]stamp{}}
	for _, in := range append(inputs, path) {
		c.stamps[in] = stampOf(in)
	}
	compiled.mu.Lock()
	if len(compiled.m) >= 256 { // projects × local/ files; past that, start over rather than track use
		compiled.m = map[string]*compiledFile{}
	}
	compiled.m[path] = c
	compiled.mu.Unlock()
	return prog, nil
}

var compiled = struct {
	mu sync.Mutex
	m  map[string]*compiledFile
}{m: map[string]*compiledFile{}}

type compiledFile struct {
	prog   *moejs.Script
	stamps map[string]stamp
}

type stamp struct {
	size int64
	mod  time.Time
	ok   bool
}

func stampOf(path string) stamp {
	fi, err := os.Stat(path)
	if err != nil {
		return stamp{}
	}
	return stamp{size: fi.Size(), mod: fi.ModTime(), ok: true}
}

func (c *compiledFile) fresh() bool {
	for p, s := range c.stamps {
		if stampOf(p) != s {
			return false
		}
	}
	return true
}
