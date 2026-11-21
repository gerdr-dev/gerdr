package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeSettings(t *testing.T, path, base string) {
	t.Helper()
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	b, _ := json.Marshal(map[string]any{"env": map[string]string{"ANTHROPIC_BASE_URL": base}})
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// The address claude ends up using, in its own order: environment < user settings < project < local < --settings.
func TestAPIBase(t *testing.T) {
	cfg, cwd := t.TempDir(), t.TempDir()
	env := []string{"ANTHROPIC_BASE_URL=https://env.example", "X=1"}
	if got := apiBase(env, nil, cfg, cwd); got != "https://env.example" {
		t.Fatalf("env only: %q", got)
	}
	writeSettings(t, filepath.Join(cfg, "settings.json"), "https://user.example")
	if got := apiBase(env, nil, cfg, cwd); got != "https://user.example" {
		t.Fatalf("user settings: %q", got)
	}
	writeSettings(t, filepath.Join(cwd, ".claude", "settings.json"), "https://project.example")
	if got := apiBase(env, nil, cfg, cwd); got != "https://project.example" {
		t.Fatalf("project settings: %q", got)
	}
	writeSettings(t, filepath.Join(cwd, ".claude", "settings.local.json"), "https://local.example")
	if got := apiBase(env, nil, cfg, cwd); got != "https://local.example" {
		t.Fatalf("local settings: %q", got)
	}
	args := []string{"--settings", `{"env":{"ANTHROPIC_BASE_URL":"https://first.example"}}`, "--settings", `{"env":{"ANTHROPIC_BASE_URL":"https://flag.example"}}`}
	if got := apiBase(env, args, cfg, cwd); got != "https://flag.example" {
		t.Fatalf("--settings: %q", got)
	}
	if got := apiBase(nil, nil, t.TempDir(), ""); got != "" {
		t.Fatalf("nothing configured: %q", got)
	}
}

// The address goes into the last --settings (only that one counts), keeping what it already says.
func TestWithBaseURL(t *testing.T) {
	got := withBaseURL([]string{"--mcp-config", "{}", "--settings", `{"skillOverrides":{"a":"off"},"env":{"K":"v"}}`}, "http://127.0.0.1:7861")
	if len(got) != 4 || got[2] != "--settings" {
		t.Fatalf("args = %q", got)
	}
	var st struct {
		SkillOverrides map[string]string `json:"skillOverrides"`
		Env            map[string]string `json:"env"`
	}
	if err := json.Unmarshal([]byte(got[3]), &st); err != nil || st.SkillOverrides["a"] != "off" || st.Env["K"] != "v" || st.Env["ANTHROPIC_BASE_URL"] != "http://127.0.0.1:7861" {
		t.Fatalf("merged settings = %s (%v)", got[3], err)
	}

	got = withBaseURL([]string{"--add-dir", "/x"}, "http://127.0.0.1:1")
	if len(got) != 4 || got[2] != "--settings" || got[3] != `{"env":{"ANTHROPIC_BASE_URL":"http://127.0.0.1:1"}}` {
		t.Fatalf("no --settings yet: %q", got)
	}

	// A --settings naming a file is left alone; the JSON one goes after it and wins
	got = withBaseURL([]string{"--settings", "/path/settings.json"}, "http://127.0.0.1:1")
	if len(got) != 4 || got[1] != "/path/settings.json" || got[2] != "--settings" {
		t.Fatalf("file --settings: %q", got)
	}
}

type fakeRoutes map[string]string

func (f fakeRoutes) Rewrite(base string) string { return f[base] }

// A session is routed only when its own address is one a route takes.
func TestRouteTarget(t *testing.T) {
	cfg := t.TempDir()
	a := &Agent{opts: Options{Env: []string{"CLAUDE_CONFIG_DIR=" + cfg}, Routes: fakeRoutes{"https://relay.example": "http://127.0.0.1:7861"}}}
	s := &session{a: a, cwd: t.TempDir()}
	if got := s.routeTarget(s.childEnv(nil)); got != "" {
		t.Fatalf("no address configured, routed to %q", got)
	}
	writeSettings(t, filepath.Join(cfg, "settings.json"), "https://relay.example")
	if got := s.routeTarget(s.childEnv(nil)); got != "http://127.0.0.1:7861" {
		t.Fatalf("relay address routed to %q", got)
	}
	writeSettings(t, filepath.Join(cfg, "settings.json"), "https://elsewhere.example")
	if got := s.routeTarget(s.childEnv(nil)); got != "" {
		t.Fatalf("other address routed to %q", got)
	}
}
