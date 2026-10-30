package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"0.165.0", "0.128.0", true},
		{"0.128.0", "0.165.0", false},
		{"2.1.10", "2.1.9", true},
		{"1.0", "1.0.0", false},
		{"1.0.0", "", true},
		{"", "1.0.0", false},
	}
	for _, c := range cases {
		if got := newer(c.a, c.b); got != c.want {
			t.Errorf("newer(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestReadable(t *testing.T) {
	raw := `{"code":-32603,"message":"Internal error","data":{"error":"context deadline exceeded"}}`
	if got := readable(raw); got != "context deadline exceeded" {
		t.Errorf("got %q", got)
	}
	if got := readable("plain"); got != "plain" {
		t.Errorf("got %q", got)
	}
}

func exe(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// A login-PATH dir that is missing from gerdr's PATH only counts when it brings commands nothing else provides:
// version-fox puts a fresh per-shell temp dir on PATH every time, with the same node as its stable dirs.
func TestPathCheckIgnoresDirsWithNothingNew(t *testing.T) {
	stable, temp, extra := t.TempDir(), t.TempDir(), t.TempDir()
	exe(t, stable, "node")
	exe(t, temp, "node")
	exe(t, extra, "ffmpeg")
	cur := stable
	if c := pathCheck(cur, stable+":"+temp, nil); c.Level != OK {
		t.Fatalf("temp dir with only a duplicate node: level %s, %s", c.Level, c.Detail)
	}
	c := pathCheck(cur, stable+":"+temp+":"+extra, nil)
	if c.Level != Bad || !strings.Contains(c.Detail, "ffmpeg") || len(c.Fixes) == 0 || c.Fixes[0].Kind != "reloadPath" {
		t.Fatalf("missing ffmpeg dir: %+v", c)
	}
}

func TestToolCheck(t *testing.T) {
	dir := t.TempDir()
	exe(t, dir, "go")
	if c := toolCheck("go", "/nonexistent", dir, true); c.Level != Bad {
		t.Errorf("in terminal but not in gerdr: %+v", c)
	}
	if c := toolCheck("go", dir, dir, true); c.Level != OK {
		t.Errorf("found: %+v", c)
	}
	if c := toolCheck("ffmpeg", dir, dir, true); c.Level != Info {
		t.Errorf("not installed: %+v", c)
	}
}

func TestVersionBlocked(t *testing.T) {
	dir := t.TempDir()
	// Stands in for a binary the system SIGKILLs on start.
	p := filepath.Join(dir, "killed")
	if err := os.WriteFile(p, []byte("#!/bin/sh\nkill -9 $$\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, blocked := version(t.Context(), p); !blocked {
		t.Error("SIGKILL on start should read as blocked")
	}
	ok := filepath.Join(dir, "ok")
	if err := os.WriteFile(ok, []byte("#!/bin/sh\necho 'codex-cli 0.165.0'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if v, e, b := version(t.Context(), ok); v != "0.165.0" || e != "" || b {
		t.Errorf("got %q %q %v", v, e, b)
	}
}

// A working current install is never offered a downgrade; a newer copy is offered; a blocked current one
// gets every working alternative.
func TestAgentFixes(t *testing.T) {
	bin := func(dir, ver string, killed bool) string {
		p := filepath.Join(dir, "codex")
		body := "#!/bin/sh\necho 'codex-cli " + ver + "'\n"
		if killed {
			body = "#!/bin/sh\nkill -9 $$\n"
		}
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	run := func(cur string, dirs ...string) Check {
		return agentCheck(t.Context(), "codex", "Codex", func() (string, error) { return cur, nil }, dirs, true)
	}
	newDir, oldDir, blockedDir := t.TempDir(), t.TempDir(), t.TempDir()
	newBin, oldBin, blockedBin := bin(newDir, "0.166.0", false), bin(oldDir, "0.128.0", false), bin(blockedDir, "", true)

	if c := run(newBin, newDir, oldDir); c.Level != OK || len(c.Fixes) != 0 {
		t.Errorf("newest in use: %+v", c)
	}
	if c := run(oldBin, newDir, oldDir); c.Level != Warn || len(c.Fixes) != 1 || c.Fixes[0].Path != newBin {
		t.Errorf("older in use: %+v", c)
	}
	c := run(blockedBin, blockedDir, newDir, oldDir)
	if c.Level != Bad || len(c.Fixes) != 2 {
		t.Errorf("blocked in use: %+v", c)
	}
	if c := run(newBin, newDir, blockedDir); c.Level != Warn || !strings.Contains(c.Detail, blockedBin) {
		t.Errorf("blocked copy lying around: %+v", c)
	}
}
