package awake

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// Gerdr and the lid-awake add-on share disablesleep: Gerdr may only turn it off when no other holder is alive.
func TestOthersHold(t *testing.T) {
	dir := t.TempDir()
	hd := holdersDir(dir)
	if othersHold(dir) {
		t.Fatal("no holders dir: nobody holds")
	}
	_ = os.MkdirAll(hd, 0o700)
	write := func(name, v string) { _ = os.WriteFile(filepath.Join(hd, name), []byte(v), 0o600) }
	write(holderName, strconv.Itoa(os.Getpid())) // our own file never counts
	write("dead", "999999")
	write("junk", "not a pid")
	if othersHold(dir) {
		t.Fatal("only our own file and dead / junk holders")
	}
	for _, n := range []string{"dead", "junk"} {
		if _, err := os.Stat(filepath.Join(hd, n)); err == nil {
			t.Fatalf("stale holder %s not removed", n)
		}
	}
	write("lid-awake", strconv.Itoa(os.Getppid()))
	if !othersHold(dir) {
		t.Fatal("a live holder must keep disablesleep on")
	}
}
