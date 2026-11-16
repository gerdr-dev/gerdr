//go:build windows

package tailscale

import (
	"os/exec"
	"syscall"
)

// hideConsole: tailscale.exe is a console program; running it from the GUI would flash a console window.
func hideConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000 /* CREATE_NO_WINDOW */}
}
