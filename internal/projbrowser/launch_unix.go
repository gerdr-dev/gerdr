//go:build !windows

package projbrowser

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// pipeArgs wires the DevTools pipe as fds 3 (the browser reads) and 4 (it writes).
func pipeArgs(cmd *exec.Cmd) (w *os.File, r *os.File, after func(), err error) {
	r3, w3, err := os.Pipe()
	if err != nil {
		return nil, nil, nil, err
	}
	r4, w4, err := os.Pipe()
	if err != nil {
		r3.Close()
		w3.Close()
		return nil, nil, nil, err
	}
	cmd.Args = append(cmd.Args, "--remote-debugging-pipe")
	cmd.ExtraFiles = []*os.File{r3, w4}
	return w3, r4, func() { r3.Close(); w4.Close() }, nil
}

// killStale stops a browser still holding the profile from an earlier Gerdr (one that crashed, or was killed, before
// it closed its browser): a second browser on the same profile would hand itself over to that one and exit, and the
// pipe with it. The lock is a symlink to "<host>-<pid>".
func killStale(profile string) {
	target, err := os.Readlink(filepath.Join(profile, "SingletonLock"))
	if err != nil {
		return
	}
	i := strings.LastIndex(target, "-")
	if i < 0 {
		return
	}
	pid, err := strconv.Atoi(target[i+1:])
	if err != nil || pid <= 1 {
		return
	}
	host, _ := os.Hostname()
	if target[:i] != host {
		return
	}
	p, err := os.FindProcess(pid)
	if err != nil || p.Signal(syscall.Signal(0)) != nil {
		return
	}
	p.Signal(syscall.SIGTERM)
	for i := 0; i < 30; i++ {
		time.Sleep(100 * time.Millisecond)
		if p.Signal(syscall.Signal(0)) != nil {
			return
		}
	}
	p.Kill()
}

// detach: a process of its own session, so it outlives this one's terminal / process group (the browser-only App
// quits by itself when idle).
func detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
