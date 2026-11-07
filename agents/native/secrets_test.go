package native

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTouchesSecret(t *testing.T) {
	home, _ := os.UserHomeDir()
	cwd := filepath.Join(home, "dev", "proj")
	read := &tool{name: "read", readOnly: true}
	bash := &tool{name: "bash"}
	grep := &tool{name: "grep", readOnly: true}
	for _, c := range []struct {
		t    *tool
		args map[string]any
		want bool
	}{
		{read, map[string]any{"path": "~/.gerdr/remote/remote.json"}, true},
		{read, map[string]any{"path": filepath.Join(home, ".ssh", "id_ed25519")}, true},
		{read, map[string]any{"path": "src/main.go"}, false},
		{read, map[string]any{"path": "~/.gerdr/builtin-skills/gerdr/SKILL.md"}, false},
		{read, map[string]any{"path": filepath.Join(home, ".gerdr/panels/团队/user/data/team.json")}, true},
		{bash, map[string]any{"command": "ls -la /Users/x/.gerdr/remote; cat ~/.gerdr/remote/*.json 2>/dev/null | head -60"}, true},
		{bash, map[string]any{"command": `cat "$HOME/.gerdr/secrets.json"`}, true},
		{bash, map[string]any{"command": "cat ~/.gerdr/*/*.json"}, true},
		{bash, map[string]any{"command": "cd ~ && cat .netrc"}, true},
		{bash, map[string]any{"command": "go test ./... && git status"}, false},
		{bash, map[string]any{"command": "cat README.md | head"}, false},
		{bash, map[string]any{"command": "ls ~/.gerdr/builtin-skills"}, false},
		{grep, map[string]any{"pattern": "token", "path": "~/.gerdr"}, true},
		{grep, map[string]any{"pattern": "token"}, false},
	} {
		if got := touchesSecret(c.t, c.args, cwd); got != c.want {
			t.Errorf("%s %v: %v, want %v", c.t.name, c.args, got, c.want)
		}
	}
	// Full access still asks; read-only mode refuses
	if v, rule := decide(modeBypass, read, map[string]any{"path": "~/.ssh/config"}, cwd, memoryDirs{}, func(string) bool { return true }, nil); v != ask || rule != "" {
		t.Errorf("bypass on a secret: %v %q", v, rule)
	}
	if v, _ := decide(modeReadOnly, bash, map[string]any{"command": "cat ~/.aws/credentials"}, cwd, memoryDirs{}, func(string) bool { return false }, nil); v != deny {
		t.Errorf("read-only on a secret: %v", v)
	}
	if v, _ := decide(modeDefault, read, map[string]any{"path": "src/main.go"}, cwd, memoryDirs{}, func(string) bool { return false }, nil); v != allow {
		t.Errorf("an ordinary read: %v", v)
	}
}
