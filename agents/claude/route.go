package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/gerdr-dev/gerdr/sysenv"
)

// Routes hands a session's model API traffic to someone serving its address locally (in gerdr: an add-on's service,
// internal/addons/routes.go).
type Routes interface {
	// Rewrite returns the address to use instead of base (the ANTHROPIC_BASE_URL claude would use), "" to keep it.
	Rewrite(base string) string
}

// routeTarget is where this session's process should send its API requests instead of where it is configured to:
// "" when no route takes its address (or it has none: signed in to Anthropic itself, which is never routed).
func (s *session) routeTarget(env []string) string {
	r := s.a.opts.Routes
	if r == nil {
		return ""
	}
	base := apiBase(env, s.a.opts.Args, filepath.Dir(s.a.projectsDir()), s.cwd)
	if base == "" {
		return ""
	}
	return r.Rewrite(base)
}

func routeFP(to string) string {
	if to == "" {
		return ""
	}
	return "\x00route:" + to
}

// childEnv is the environment a session's process starts with, as far as the API address is concerned: what it
// inherits, then the agent's own, then the session's (spawn adds a few of its own, none about the API).
func (s *session) childEnv(meta map[string]any) []string {
	env := append(sysenv.ChildEnv(), s.a.opts.Env...)
	if f := s.a.opts.SessionEnv; f != nil {
		env = append(env, f(s.id, meta)...)
	}
	return env
}

// apiBase is the ANTHROPIC_BASE_URL a claude process would use, in Claude Code's own order (each later one wins):
// its environment, then the env of user settings (the config dir's settings.json), the project's
// .claude/settings.json and .claude/settings.local.json, then the last --settings argument. Settings files beat the
// environment: tested with claude 2.1.327, an env in ~/.claude/settings.json overrides the process's.
func apiBase(env, args []string, configDir, cwd string) string {
	base := ""
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "ANTHROPIC_BASE_URL="); ok {
			base = v
		}
	}
	files := []string{filepath.Join(configDir, "settings.json")}
	if cwd != "" {
		files = append(files, filepath.Join(cwd, ".claude", "settings.json"), filepath.Join(cwd, ".claude", "settings.local.json"))
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		if v := settingsBaseURL(b); v != "" {
			base = v
		}
	}
	if i := lastSettingsArg(args); i >= 0 {
		if v := settingsBaseURL([]byte(args[i])); v != "" {
			base = v
		}
	}
	return base
}

func settingsBaseURL(b []byte) string {
	var st struct {
		Env map[string]string `json:"env"`
	}
	if json.Unmarshal(b, &st) != nil {
		return ""
	}
	return st.Env["ANTHROPIC_BASE_URL"]
}

// lastSettingsArg is the index of the value of the last --settings in args, -1 when there is none or it names a file.
func lastSettingsArg(args []string) int {
	at := -1
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--settings" {
			at = i + 1
		}
	}
	if at >= 0 && !strings.HasPrefix(strings.TrimSpace(args[at]), "{") {
		return -1
	}
	return at
}

// withBaseURL sets env.ANTHROPIC_BASE_URL in the last --settings of args, adding one when there is none. Only the
// last --settings counts (tested with claude 2.1.327: of two, the first one's env was ignored), so it has to go into
// the one gerdr already passes (skill overrides, ext.ClaudeArgs) rather than a second one. A --settings naming a file
// is left alone and a JSON one appended after it.
func withBaseURL(args []string, url string) []string {
	out := append([]string(nil), args...)
	i := lastSettingsArg(out)
	st := map[string]any{}
	if i >= 0 {
		if json.Unmarshal([]byte(out[i]), &st) != nil {
			st = map[string]any{}
			i = -1
		}
	}
	env, _ := st["env"].(map[string]any)
	if env == nil {
		env = map[string]any{}
	}
	env["ANTHROPIC_BASE_URL"] = url
	st["env"] = env
	b, _ := json.Marshal(st)
	if i >= 0 {
		out[i] = string(b)
		return out
	}
	return append(out, "--settings", string(b))
}
