package hub

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gerdr-dev/gerdr/internal/i18n"
	"github.com/gerdr-dev/gerdr/sysenv"
)

// RunCommand runs one command line on this computer and hands back what it printed: `gerdr exec --host`, so an agent on
// another computer can run a command here without asking an agent here to run it for it (a turn, tokens, a
// minute per command). Same shell as `!command` (shellCommand: the login shell, so PATH is the profile's), its own
// process group (stopping it stops what it started), no stdin unless given. Nothing is logged in any conversation:
// the caller shows it.

const (
	ExecMaxTimeout     = time.Hour
	ExecDefaultTimeout = 2 * time.Minute
	execKeep           = 512 << 10 // bytes kept from each end of a long output
)

type ExecResult struct {
	Output    string `json:"output"` // stdout and stderr as they came, interleaved like a terminal shows them
	Code      int    `json:"code"`   // -1: killed (timed out, or the caller went away)
	TimedOut  bool   `json:"timedOut,omitempty"`
	Truncated bool   `json:"truncated,omitempty"` // the middle was dropped: Output is its first and last execKeep bytes
	Cwd       string `json:"cwd"`
	Ms        int64  `json:"ms"`
}

// ExecDir is the directory a command runs in: "" or "~" = home, "~/x" under home, else as given (must exist).
func ExecDir(dir string) (string, error) {
	home, _ := os.UserHomeDir()
	switch {
	case dir == "" || dir == "~":
		return home, nil
	case strings.HasPrefix(dir, "~/"):
		dir = filepath.Join(home, dir[2:])
	case !filepath.IsAbs(dir):
		return "", fmt.Errorf(i18n.S("目录要写绝对路径或 ~/…：%s", "the directory must be absolute or ~/…: %s"), dir)
	}
	st, err := os.Stat(dir)
	if err != nil || !st.IsDir() {
		return "", fmt.Errorf(i18n.S("这台电脑上没有这个目录：%s", "no such directory on this computer: %s"), dir)
	}
	return dir, nil
}

func RunCommand(ctx context.Context, command, dir, stdin string, timeout time.Duration) (ExecResult, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return ExecResult{}, errors.New(i18n.S("命令是空的", "The command is empty"))
	}
	cwd, err := ExecDir(dir)
	if err != nil {
		return ExecResult{}, err
	}
	if timeout <= 0 {
		timeout = ExecDefaultTimeout
	}
	timeout = min(timeout, ExecMaxTimeout)
	out := &headTail{}
	cmd, flush := shellCommand(command, out)
	cmd.Env = sysenv.ChildEnv()
	cmd.Dir = cwd
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	ownGroup(cmd)
	// Something it left running in the background (`server &`) keeps the output pipe open: stop waiting for it 2s
	// after the command itself exits, as `!command` does
	cmd.WaitDelay = 2 * time.Second
	start := time.Now()
	if err := cmd.Start(); err != nil {
		return ExecResult{}, err
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	res := ExecResult{Cwd: cwd}
	var werr error
	select {
	case werr = <-exited:
	case <-timer.C:
		res.TimedOut = true
		werr = stopGroup(cmd.Process.Pid, exited)
	case <-ctx.Done():
		werr = stopGroup(cmd.Process.Pid, exited)
	}
	flush()
	res.Ms = time.Since(start).Milliseconds()
	res.Code = cmd.ProcessState.ExitCode()
	res.Output, res.Truncated = out.text()
	if werr != nil && res.Code == 0 && !errors.Is(werr, os.ErrProcessDone) {
		// Exited 0 but Wait gave up on the pipe (WaitDelay): the output is complete as far as the command goes
		res.Output += "\n[" + werr.Error() + "]"
	}
	return res, nil
}

// stopGroup: SIGTERM the command's group, SIGKILL after 2s, and wait for it.
func stopGroup(pid int, exited <-chan error) error {
	killGroup(pid, false)
	select {
	case err := <-exited:
		return err
	case <-time.After(2 * time.Second):
		killGroup(pid, true)
		return <-exited
	}
}

// headTail keeps the first and last execKeep bytes of what's written: a build's first error and its summary both
// survive a huge output.
type headTail struct {
	mu      sync.Mutex
	head    []byte
	tail    []byte
	dropped bool
}

func (h *headTail) Write(p []byte) (int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := len(p)
	if room := execKeep - len(h.head); room > 0 {
		k := min(room, len(p))
		h.head = append(h.head, p[:k]...)
		p = p[k:]
	}
	if len(p) == 0 {
		return n, nil
	}
	h.tail = append(h.tail, p...)
	if over := len(h.tail) - execKeep; over > 0 {
		h.tail = append(h.tail[:0], h.tail[over:]...)
		h.dropped = true
	}
	return n, nil
}

func (h *headTail) text() (string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.dropped {
		return strings.ToValidUTF8(string(h.head)+string(h.tail), "�"), false
	}
	var b bytes.Buffer
	b.Write(h.head)
	b.WriteString(i18n.S("\n…（中间的输出太长，省掉了）…\n", "\n…(the middle of the output was too long and is left out)…\n"))
	b.Write(h.tail)
	return strings.ToValidUTF8(b.String(), "�"), true
}
