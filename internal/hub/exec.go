package hub

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gerdr-dev/gerdr/internal/i18n"
	"github.com/gerdr-dev/gerdr/sysenv"
)

// RunCommand runs one command line on this computer and hands back what it printed: `gerdr host exec`, so an agent on
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
	// Stdin: what arrived of the stdin the caller sent (all of it, also the part the command didn't read), so the
	// caller can tell a cut-off or mangled upload from a command that failed. Absent when none was given.
	Stdin *StdinInfo `json:"stdin,omitempty"`
}

type StdinInfo struct {
	Bytes  int64  `json:"bytes"`
	Sha256 string `json:"sha256"`
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

// RunCommand: stdin nil = none; else it is fed to the command as raw bytes and read to its end even when the command
// stops reading early (ExecResult.Stdin counts and hashes all of it).
func RunCommand(ctx context.Context, command, dir string, stdin io.Reader, timeout time.Duration) (ExecResult, error) {
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
	var pipe io.WriteCloser
	if stdin != nil {
		// Our own copy rather than cmd.Stdin = stdin: exec's copier can still be blocked in a Read after Wait gives up
		// (WaitDelay), and the rest of the body has to be read (and hashed) by one goroutine only
		if pipe, err = cmd.StdinPipe(); err != nil {
			return ExecResult{}, err
		}
	}
	ownGroup(cmd)
	// Something it left running in the background (`server &`) keeps the output pipe open: stop waiting for it 2s
	// after the command itself exits, as `!command` does
	cmd.WaitDelay = 2 * time.Second
	start := time.Now()
	if err := cmd.Start(); err != nil {
		return ExecResult{}, err
	}
	var fed chan StdinInfo
	if pipe != nil {
		fed = make(chan StdinInfo, 1)
		go func() {
			h := &hashCount{h: sha256.New()}
			_, _ = io.Copy(pipe, io.TeeReader(stdin, h)) // counted as read, also a chunk the pipe refused
			pipe.Close()
			// The command closed its stdin (exited, or doesn't want more): still take the rest in, so the count and
			// hash say what was sent, not what was read
			_, _ = io.Copy(h, stdin)
			fed <- StdinInfo{Bytes: h.n, Sha256: hex.EncodeToString(h.h.Sum(nil))}
		}()
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
	if fed != nil && !res.TimedOut && ctx.Err() == nil {
		select {
		case in := <-fed:
			res.Stdin = &in
		case <-ctx.Done(): // the caller gave up while sending
		}
	}
	res.Ms = time.Since(start).Milliseconds()
	res.Code = cmd.ProcessState.ExitCode()
	res.Output, res.Truncated = out.text()
	if werr != nil && res.Code == 0 && !errors.Is(werr, os.ErrProcessDone) {
		// Exited 0 but Wait gave up on the pipe (WaitDelay): the output is complete as far as the command goes
		res.Output += "\n[" + werr.Error() + "]"
	}
	return res, nil
}

type hashCount struct {
	h hash.Hash
	n int64
}

func (c *hashCount) Write(p []byte) (int, error) {
	c.n += int64(len(p))
	return c.h.Write(p)
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
