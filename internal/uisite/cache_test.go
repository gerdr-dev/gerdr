package uisite

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestModuleCachePrune(t *testing.T) {
	now := time.Now()
	c := &moduleCache{dir: t.TempDir()}
	write := func(name string, age time.Duration) string {
		p := filepath.Join(c.dir, name[:2], name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(`{}`), 0o644); err != nil {
			t.Fatal(err)
		}
		at := now.Add(-age)
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
		return p
	}
	fresh := write("aa_fresh.json", time.Hour)
	stale := write("bb_stale.json", moduleCacheMaxAge+time.Hour)
	tmp := write("cc_crash.json.tmp", 2*time.Hour)
	writing := write("dd_writing.json.tmp", time.Minute)
	// Read 10 days ago and read again now: the read re-stamps it, so it survives.
	readNow := write("ee_read.json", moduleCacheMaxAge-time.Hour)
	touchCacheFile(readNow, now)
	if fi, _ := os.Stat(readNow); now.Sub(fi.ModTime()) > time.Minute {
		t.Fatalf("read entry not re-stamped: %v", fi.ModTime())
	}

	if n, _ := c.prune(now); n != 2 {
		t.Fatalf("removed %d, want 2", n)
	}
	for p, want := range map[string]bool{fresh: true, stale: false, tmp: false, writing: true, readNow: true} {
		if _, err := os.Stat(p); (err == nil) != want {
			t.Errorf("%s: exists=%v, want %v", filepath.Base(p), err == nil, want)
		}
	}
}
