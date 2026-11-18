//go:build windows

package projbrowser

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// pipeArgs wires the DevTools pipe on Windows: inheritable handles named on the command line
// (--remote-debugging-io-pipes=<read>,<write>), since there are no numbered fds. Not tried on a Windows machine yet.
func pipeArgs(cmd *exec.Cmd) (w *os.File, r *os.File, after func(), err error) {
	rIn, wIn, err := os.Pipe() // the browser reads rIn
	if err != nil {
		return nil, nil, nil, err
	}
	rOut, wOut, err := os.Pipe() // and writes wOut
	if err != nil {
		rIn.Close()
		wIn.Close()
		return nil, nil, nil, err
	}
	for _, f := range []*os.File{rIn, wOut} {
		if err := syscall.SetHandleInformation(syscall.Handle(f.Fd()), syscall.HANDLE_FLAG_INHERIT, syscall.HANDLE_FLAG_INHERIT); err != nil {
			return nil, nil, nil, err
		}
	}
	cmd.Args = append(cmd.Args, "--remote-debugging-pipe", fmt.Sprintf("--remote-debugging-io-pipes=%d,%d", rIn.Fd(), wOut.Fd()))
	cmd.SysProcAttr = &syscall.SysProcAttr{AdditionalInheritedHandles: []syscall.Handle{syscall.Handle(rIn.Fd()), syscall.Handle(wOut.Fd())}}
	return wIn, rOut, func() { rIn.Close(); wOut.Close() }, nil
}

// killStale: Windows locks the profile with a file the browser holds open; nothing to read a pid from.
func killStale(string) {}

// detach: nothing to do on Windows (a child isn't killed with its parent's console here).
func detach(cmd *exec.Cmd) {}
