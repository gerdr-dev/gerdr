//go:build windows

package hub

import (
	"os/exec"
	"strconv"
	"syscall"
)

// ownGroup: a process group of its own (and no console window popping up for it).
func ownGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | 0x08000000 /* CREATE_NO_WINDOW */}
}

// killGroup: Windows has no process groups to signal; taskkill /T ends the process and every process it started.
// Without force it asks them to close (WM_CLOSE: windowed programs only), with force it terminates them.
func killGroup(pid int, force bool) {
	args := []string{"/T", "/PID", strconv.Itoa(pid)}
	if force {
		args = append([]string{"/F"}, args...)
	}
	c := exec.Command("taskkill", args...)
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	_ = c.Run()
}
