//go:build !windows

package hub

import (
	"os/exec"
	"syscall"
)

// ownGroup: the command in a process group of its own, so killGroup reaches what it started.
func ownGroup(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

// killGroup: SIGTERM the command's whole group, or SIGKILL when force.
func killGroup(pid int, force bool) {
	sig := syscall.SIGTERM
	if force {
		sig = syscall.SIGKILL
	}
	_ = syscall.Kill(-pid, sig)
}
