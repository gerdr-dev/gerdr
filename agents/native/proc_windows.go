//go:build windows

package native

import (
	"os"
	"os/exec"
	"strconv"
)

func setProcGroup(cmd *exec.Cmd) {}

// Windows 没有进程组信号：taskkill /T 连子进程一起结束（没有温和的一步，force 都一样）
func killProcGroup(p *os.Process, force bool) {
	if err := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(p.Pid)).Run(); err != nil {
		_ = p.Kill()
	}
}
