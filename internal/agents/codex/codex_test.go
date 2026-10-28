package codex

import "testing"

func TestStripShell(t *testing.T) {
	for in, want := range map[string]string{
		`/bin/zsh -lc 'echo hi > a.txt'`:  "echo hi > a.txt",
		`/bin/bash -lc "ls -la"`:          "ls -la",
		`/bin/zsh -lc 'echo '\''q'\'' x'`: "echo 'q' x",
		`git status`:                      "git status",
	} {
		if got := stripShell(in); got != want {
			t.Errorf("stripShell(%q) = %q, want %q", in, got, want)
		}
	}
}
