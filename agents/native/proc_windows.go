//go:build windows

package native

import (
	"fmt"
	"os/exec"
	"strconv"
	"sync"
	"syscall"

	"golang.org/x/sys/windows"
)

// Windows has no process groups: a command and everything it starts go into a Job object, and stopping it ends the
// Job. taskkill /T (what this did before) walks parent ids, so it misses a grandchild whose parent already exited
// (npm → cmd → node) and can hit an unrelated process that took over a dead one's id. The shell starts suspended
// and only runs once it is in the Job, so nothing it starts can slip out in between. No kill-on-close: as with a
// process group on Unix, what a finished command left running (`server &`) keeps running.

func setProcGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED}
}

type procTree struct {
	pid  int
	mu   sync.Mutex
	job  windows.Handle // 0: no Job (creating or joining it failed), taskkill /T instead
	done bool           // exited: its id may belong to another process now
}

var ntResumeProcess = windows.NewLazySystemDLL("ntdll.dll").NewProc("NtResumeProcess")

// startedTree puts the suspended command into a new Job and lets it run. When it cannot be resumed it is killed
// and the error returned: left suspended it would hang until the timeout.
func startedTree(cmd *exec.Cmd) (*procTree, error) {
	t := &procTree{pid: cmd.Process.Pid}
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_SUSPEND_RESUME, false, uint32(t.pid))
	if err != nil {
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("open the started process: %w", err)
	}
	defer windows.CloseHandle(h)
	if job, err := windows.CreateJobObject(nil, nil); err == nil {
		// fails when the job Gerdr itself runs in forbids nesting (before Windows 8): taskkill /T then
		if windows.AssignProcessToJobObject(job, h) == nil {
			t.job = job
		} else {
			windows.CloseHandle(job)
		}
	}
	if st, _, _ := ntResumeProcess.Call(uintptr(h)); st != 0 {
		_ = cmd.Process.Kill()
		t.close()
		return nil, fmt.Errorf("resume the started process: NTSTATUS 0x%x", st)
	}
	return t, nil
}

// kill ends the whole tree. There is no gentle step on Windows; force is the same.
func (t *procTree) kill(force bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.done {
		return
	}
	if t.job != 0 && windows.TerminateJobObject(t.job, 1) == nil {
		return
	}
	_ = exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(t.pid)).Run()
}

func (t *procTree) close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.done = true
	if t.job != 0 {
		windows.CloseHandle(t.job)
		t.job = 0
	}
}
