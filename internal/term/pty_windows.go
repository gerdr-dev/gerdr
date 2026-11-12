package term

import (
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/gerdr-dev/gerdr/agents/native"
)

// Windows terminals run on ConPTY (Windows 10 1809+): a pseudo console fed by two pipes, which turns the console
// API calls of the programs in it into VT sequences xterm.js draws, and keys back into input. The shell is picked
// the way the native agent's bash tool picks it (native.Shell): Git for Windows' bash when installed, else
// PowerShell — so the terminal and the agent's commands run in the same shell.

type ptySys struct {
	// hpc is the pseudo console and proc the shell, both 0 once the shell has exited and they are closed. An HPCON
	// is a pointer into our process, so resizing a closed one would touch freed memory: mu guards them.
	mu   sync.Mutex
	hpc  windows.Handle
	proc windows.Handle
	// job holds the shell and everything it starts (KILL_ON_JOB_CLOSE): Windows has no process group to signal, and
	// killing only the shell would leave a dev server it started running where nobody sees it. Closing the handle
	// (kill, or Gerdr exiting) ends them all, like SIGHUP to the process group on Unix.
	job      windows.Handle
	inJob    bool
	cols     atomic.Int32
	killOnce sync.Once
}

func defaultShell() string { return native.Shell() }

func (t *Term) start(env []string, cols, rows uint16) (err error) {
	var inR, inW, outR, outW windows.Handle
	if err := windows.CreatePipe(&inR, &inW, nil, 0); err != nil {
		return fmt.Errorf("pipe: %w", err)
	}
	if err := windows.CreatePipe(&outR, &outW, nil, 0); err != nil {
		windows.CloseHandle(inR)
		windows.CloseHandle(inW)
		return fmt.Errorf("pipe: %w", err)
	}
	// The pseudo console keeps its own copies of inR / outW; ours are closed once the shell has started (or failed
	// to), else reading outR would never see EOF.
	defer windows.CloseHandle(inR)
	defer windows.CloseHandle(outW)
	in, out := os.NewFile(uintptr(inW), "conpty-in"), os.NewFile(uintptr(outR), "conpty-out")
	defer func() {
		if err != nil {
			in.Close()
			out.Close()
		}
	}()

	var hpc windows.Handle
	if err := windows.CreatePseudoConsole(windows.Coord{X: int16(cols), Y: int16(rows)}, inR, outW, 0, &hpc); err != nil {
		return fmt.Errorf("CreatePseudoConsole (needs Windows 10 1809+): %w", err)
	}
	defer func() {
		if err != nil {
			windows.ClosePseudoConsole(hpc)
		}
	}()

	attrs, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return err
	}
	defer attrs.Delete()
	// The attribute's value is the HPCON itself, not a pointer to it.
	if err := attrs.Update(windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, *(*unsafe.Pointer)(unsafe.Pointer(&hpc)), unsafe.Sizeof(hpc)); err != nil {
		return err
	}
	si := &windows.StartupInfoEx{ProcThreadAttributeList: attrs.List()}
	si.Cb = uint32(unsafe.Sizeof(*si))
	// STARTF_USESTDHANDLES with no handles: without it a Gerdr whose own stdout / stderr are redirected (the
	// background service writes them to a log) hands those to the shell, which then writes there instead of the
	// pseudo console and the pane stays blank.
	si.Flags |= windows.STARTF_USESTDHANDLES

	args := []string{t.shell}
	if native.IsPowerShell(t.shell) {
		args = append(args, "-NoLogo")
	} else {
		// login shell for the profile's PATH, as on Unix; -i because bash on a pipe-backed console may not see a tty
		args = append(args, "-l", "-i")
		// Git Bash's /etc/profile cd's to $HOME unless this is set, which would drop the cwd we were asked for.
		env = append(env, "CHERE_INVOKING=1")
	}
	cmdline, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(args))
	if err != nil {
		return err
	}
	dir, err := windows.UTF16PtrFromString(t.cwd)
	if err != nil {
		return err
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return fmt.Errorf("job object: %w", err)
	}
	limit := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limit.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limit)), uint32(unsafe.Sizeof(limit))); err != nil {
		windows.CloseHandle(job)
		return fmt.Errorf("job object: %w", err)
	}

	var pi windows.ProcessInformation
	// Suspended until it is in the job, so nothing it starts slips out.
	flags := uint32(windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_UNICODE_ENVIRONMENT | windows.CREATE_SUSPENDED)
	if err := windows.CreateProcess(nil, cmdline, nil, nil, false, flags, envBlock(env), dir, &si.StartupInfo, &pi); err != nil {
		windows.CloseHandle(job)
		return fmt.Errorf("start %s: %w", t.shell, err)
	}
	// Assigning fails only when Gerdr's own job forbids nesting; kill then ends just the shell (inJob).
	inJob := windows.AssignProcessToJobObject(job, pi.Process) == nil
	windows.ResumeThread(pi.Thread)
	windows.CloseHandle(pi.Thread)

	t.in, t.out = in, out
	t.pid = int(pi.ProcessId)
	t.sys.hpc, t.sys.proc, t.sys.job, t.sys.inJob = hpc, pi.Process, job, inJob
	t.sys.cols.Store(int32(cols))
	pumped := make(chan struct{})
	go func() {
		t.pump()
		_ = out.Close()
		close(pumped)
	}()
	go func() {
		windows.WaitForSingleObject(pi.Process, windows.INFINITE)
		var code uint32
		_ = windows.GetExitCodeProcess(pi.Process, &code)
		t.sys.mu.Lock()
		t.sys.hpc, t.sys.proc = 0, 0
		t.sys.mu.Unlock()
		windows.CloseHandle(pi.Process)
		// Closing the pseudo console makes its end of the output pipe go away, so pump reads what is left and then
		// EOF. (Before Windows 11 this call blocks until that output is read — pump is reading it.) Waiting for pump
		// puts the shell's last words in the replay before the exit is announced.
		windows.ClosePseudoConsole(hpc)
		select {
		case <-pumped:
		case <-time.After(2 * time.Second):
		}
		_ = in.Close()
		t.finish(int(int32(code))) // TerminateJobObject's ^0 reads as -1: "killed", as a signal is on Unix
	}()
	return nil
}

// envBlock is CreateProcess's environment: UTF-16 "k=v\0" entries ending in an empty one.
func envBlock(env []string) *uint16 {
	var b []uint16
	for _, kv := range env {
		b = append(b, utf16.Encode([]rune(kv))...)
		b = append(b, 0)
	}
	b = append(b, 0)
	if len(env) == 0 {
		b = append(b, 0)
	}
	return &b[0]
}

func (t *Term) Resize(cols, rows uint16) error {
	if cols == 0 || rows == 0 {
		return nil
	}
	t.sys.mu.Lock()
	defer t.sys.mu.Unlock()
	if t.sys.hpc == 0 {
		return nil
	}
	t.sys.cols.Store(int32(cols))
	return windows.ResizePseudoConsole(t.sys.hpc, windows.Coord{X: int16(cols), Y: int16(rows)})
}

func (t *Term) width() int { return int(t.sys.cols.Load()) }

// kill ends the shell and everything it started (closing the job, KILL_ON_JOB_CLOSE). Windows has no SIGHUP to ask
// first.
func (t *Term) kill() {
	t.sys.killOnce.Do(func() {
		if t.sys.inJob {
			_ = windows.TerminateJobObject(t.sys.job, ^uint32(0))
		} else {
			t.sys.mu.Lock()
			if t.sys.proc != 0 {
				_ = windows.TerminateProcess(t.sys.proc, ^uint32(0))
			}
			t.sys.mu.Unlock()
		}
		// Closing the job also ends what the shell left running after it exited, as closing a pane does on Unix.
		windows.CloseHandle(t.sys.job)
	})
}

// LiveCwd: not known on Windows (another process's cwd is only in its PEB), so a split opens where the first
// terminal started.
func (t *Term) LiveCwd() string { return "" }
