//go:build !windows

package selfupdate

import "os/exec"

func hideConsole(*exec.Cmd) {}
