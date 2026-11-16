//go:build !windows

package tailscale

import "os/exec"

func hideConsole(*exec.Cmd) {}
