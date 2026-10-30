package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/gerdr-dev/gerdr/sysenv"
)

// ErrNotLoggedIn: claude is installed but has no credentials (`claude auth login`, or ANTHROPIC_API_KEY).
var ErrNotLoggedIn = errors.New("Claude Code is not logged in: run `claude auth login`")

// Status is what Check found.
type Status struct {
	Bin        string `json:"bin"`
	LoggedIn   bool   `json:"loggedIn"`
	AuthMethod string `json:"authMethod,omitempty"` // claude.ai | api_key | …, as claude reports it
	Email      string `json:"email,omitempty"`
}

// Check reports whether this instance's claude (Options.Bin, Options.Env) can run: a *sysenv.NotFoundError when
// it isn't installed, ErrNotLoggedIn when it has no credentials. Runs `claude auth status --json` (fast, offline).
func Check(ctx context.Context, opts Options) (Status, error) {
	a := &Agent{opts: opts}
	bin, err := a.bin()
	if err != nil {
		return Status{}, err
	}
	st := Status{Bin: bin}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "auth", "status", "--json")
	cmd.Dir = os.TempDir()
	cmd.Env = append(sysenv.ChildEnv(), opts.Env...)
	out, err := cmd.Output()
	var res struct {
		LoggedIn   bool   `json:"loggedIn"`
		AuthMethod string `json:"authMethod"`
		Email      string `json:"email"`
	}
	if jerr := json.Unmarshal(out, &res); jerr != nil {
		if err == nil {
			err = jerr
		}
		return st, fmt.Errorf("claude auth status: %v: %s", err, strings.TrimSpace(string(out)))
	}
	st.LoggedIn, st.AuthMethod, st.Email = res.LoggedIn, res.AuthMethod, res.Email
	if !st.LoggedIn {
		return st, ErrNotLoggedIn
	}
	return st, nil
}
