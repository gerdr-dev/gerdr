package codex

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/coder/acp-go-sdk"

	"github.com/gerdr-dev/gerdr/rpcline"
	"github.com/gerdr-dev/gerdr/sysenv"
)

// threadConfig is a thread's config override: Options.Ext's, then the client's MCP servers, StrictMCP and
// SessionEnv. sid is "" for a thread being created.
func (a *Agent) threadConfig(ctx context.Context, rpc *rpcline.Conn, sid, cwd string, meta map[string]any, servers []acp.McpServer) (map[string]any, string) {
	cfg, fp := a.extConfig(ctx, rpc, cwd)
	set := func(k string, v any) {
		if cfg == nil {
			cfg = map[string]any{}
		}
		cfg[k] = v
	}
	mine := map[string]bool{}
	for _, x := range servers {
		switch {
		case x.Stdio != nil:
			env := map[string]string{}
			for _, e := range x.Stdio.Env {
				env[e.Name] = e.Value
			}
			args := x.Stdio.Args
			if args == nil {
				args = []string{}
			}
			t := map[string]any{"command": x.Stdio.Command, "args": args}
			if len(env) > 0 {
				t["env"] = env
			}
			set("mcp_servers."+x.Stdio.Name, t)
			mine[x.Stdio.Name] = true
		case x.Http != nil:
			t := map[string]any{"url": x.Http.Url}
			if len(x.Http.Headers) > 0 {
				h := map[string]string{}
				for _, kv := range x.Http.Headers {
					h[kv.Name] = kv.Value
				}
				t["http_headers"] = h
			}
			set("mcp_servers."+x.Http.Name, t)
			mine[x.Http.Name] = true
		case x.Sse != nil:
			a.log.Warn("codex has no legacy SSE MCP transport; skipping", "server", x.Sse.Name)
		}
	}
	if a.opts.StrictMCP {
		for _, name := range ownMCPNames() {
			if !mine[name] {
				set("mcp_servers."+name+".enabled", false)
			}
		}
	}
	if f := a.opts.SessionEnv; f != nil {
		env := map[string]string{}
		for _, kv := range f(sid, meta) {
			if k, v, ok := strings.Cut(kv, "="); ok {
				env[k] = v
			}
		}
		if len(env) > 0 {
			set("shell_environment_policy.set", env)
		}
	}
	return cfg, fp
}

// ownMCPNames: the MCP servers in the user's ~/.codex/config.toml ($CODEX_HOME).
func ownMCPNames() []string {
	var cfg struct {
		MCPServers map[string]toml.Primitive `toml:"mcp_servers"`
	}
	dir := os.Getenv("CODEX_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".codex")
	}
	if _, err := toml.DecodeFile(filepath.Join(dir, "config.toml"), &cfg); err != nil {
		return nil
	}
	var out []string
	for name := range cfg.MCPServers {
		out = append(out, name)
	}
	return out
}

// ErrNotLoggedIn: codex is installed but not logged in (`codex login`).
var ErrNotLoggedIn = errors.New("Codex is not logged in: run `codex login`")

// Status is what Check found.
type Status struct {
	Bin      string `json:"bin"`
	LoggedIn bool   `json:"loggedIn"`
	Detail   string `json:"detail,omitempty"` // codex's own line, e.g. "Logged in using ChatGPT"
}

// Check reports whether this instance's codex (Options.Bin, Options.Env) can run: a *sysenv.NotFoundError when it
// isn't installed, ErrNotLoggedIn when `codex login status` says so.
func Check(ctx context.Context, opts Options) (Status, error) {
	bin, err := sysenv.FindCodex()
	if opts.Bin != "" {
		bin, err = sysenv.FindBin(opts.Bin)
	}
	if err != nil {
		return Status{}, err
	}
	st := Status{Bin: bin}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "login", "status")
	cmd.Dir = os.TempDir()
	cmd.Env = append(sysenv.ChildEnv(), opts.Env...)
	out, err := cmd.CombinedOutput()
	st.Detail = strings.TrimSpace(string(out))
	var ee *exec.ExitError
	switch {
	case err == nil:
		st.LoggedIn = true
		return st, nil
	case errors.As(err, &ee):
		return st, ErrNotLoggedIn
	}
	return st, fmt.Errorf("codex login status: %w", err)
}
