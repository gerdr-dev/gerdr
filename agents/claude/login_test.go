package claude

import (
	"testing"

	"github.com/gerdr-dev/gerdr/acpx"
)

func TestAuthFailed(t *testing.T) {
	for _, m := range []string{
		"Not logged in · Please run /login",
		"Failed to authenticate: OAuth session expired and could not be refreshed",
		`API Error: 401 {"type":"error","error":{"type":"authentication_error","message":"OAuth token has expired."}}`,
	} {
		if !authFailed(m) {
			t.Errorf("authFailed(%q) = false", m)
		}
	}
	if authFailed("API Error: 529 overloaded") {
		t.Error("an overload is not a sign-in failure")
	}
}

func TestLoginError(t *testing.T) {
	if got := loginError("Not logged in · Please run /login"); got != "Not logged in" {
		t.Errorf("loginError = %q", got)
	}
	m := "Failed to authenticate: OAuth session expired and could not be refreshed"
	if got := loginError(m); got != m {
		t.Errorf("loginError = %q", got)
	}
}

func TestLoginCommand(t *testing.T) {
	a := &Agent{opts: Options{Env: []string{"FOO=1", "CLAUDE_CONFIG_DIR=/Users/me/.claude work"}}}
	if got, want := a.loginCommand(), "CLAUDE_CONFIG_DIR='/Users/me/.claude work' claude auth login"; got != want {
		t.Errorf("loginCommand = %q, want %q", got, want)
	}
	if got := (&Agent{}).loginCommand(); got != "claude auth login" {
		t.Errorf("loginCommand = %q", got)
	}
	err := acpx.AuthRequired("expired", "claude auth login")
	if acpx.LoginCommandOf(err) != "claude auth login" {
		t.Error("LoginCommandOf lost the command")
	}
}
