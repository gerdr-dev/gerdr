//go:build windows

package selfupdate

import (
	"os/exec"
	"syscall"
)

// hideConsole: gerdr.exe is a console program; asking it its version from a GUI shell would flash a console window.
func hideConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000 /* CREATE_NO_WINDOW */}
}
