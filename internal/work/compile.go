package work

import (
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/dop251/goja"
)

// compile bundles a local/ file (esbuild) and compiles it for goja, reusing the last result for that file while
// none of the files it was built from changed (size and modification time of each). Panels' pages poll functions
// (tasks.list, a log…) every few seconds: bundling and compiling on every call was ~1 run a second, a third of the
// service's allocations, and the GC that came with them kept it busy. A goja Program isn't tied to a runtime, so
// each run's own VM can run the shared one.
func compile(path string) (*goja.Program, error) {
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
	prog, err := goja.Compile(filepath.Base(path), code, false)
	if err != nil {
		return nil, jsError(err)
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
	prog   *goja.Program
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
