//go:build !windows

package native

import (
	"os"
	"os/exec"
	"syscall"
)

// 命令放进自己的进程组，kill 时整组一起杀
func setProcGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killProcGroup(p *os.Process, force bool) {
	sig := syscall.SIGTERM
	if force {
		sig = syscall.SIGKILL
	}
	_ = syscall.Kill(-p.Pid, sig)
}
