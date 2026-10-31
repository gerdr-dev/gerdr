//go:build !windows

package plugins

import (
	"os/exec"
	"syscall"
)

// A service gets its own process group, so stopping it also stops what it started (a helper it spawned).
func setProcGroup(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

func terminate(cmd *exec.Cmd) { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM) }

func killGroup(cmd *exec.Cmd) { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
