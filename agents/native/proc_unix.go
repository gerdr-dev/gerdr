//go:build !windows

package native

import (
	"os/exec"
	"syscall"
)

// setProcGroup puts the command in its own process group, so stopping it stops what it started too.
func setProcGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// procTree is a started command and everything it starts: its process group.
type procTree struct{ pid int }

func startedTree(cmd *exec.Cmd) (*procTree, error) { return &procTree{pid: cmd.Process.Pid}, nil }

func (t *procTree) kill(force bool) {
	sig := syscall.SIGTERM
	if force {
		sig = syscall.SIGKILL
	}
	_ = syscall.Kill(-t.pid, sig)
}

// close: the command has exited. Nothing to release; what it left running stays in the group.
func (t *procTree) close() {}
