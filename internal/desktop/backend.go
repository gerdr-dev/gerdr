package desktop

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// backend runs `gerdr --app` the way desktop/main.js does: it prints `GERDR_READY <url>` once serving (the url is a
// login link, /auth?token=…), attaches to a gerdr already using the data dir, and exits with us (--parent-pid).
type backend struct {
	cmd     *exec.Cmd
	ready   chan string // every GERDR_READY url (a second one = the gerdr it attached to went away and it took over)
	mu      sync.Mutex
	errTail string
	exited  chan struct{}
	// replaced: the shell stopped it to run another (an update); not an exit to react to
	replaced atomic.Bool
}

var readyRe = regexp.MustCompile(`^GERDR_READY (\S+)`)

func startBackend(bin string, env []string) (*backend, error) {
	if _, err := os.Stat(bin); err != nil {
		return nil, fmt.Errorf("gerdr backend not found: %s", bin)
	}
	b := &backend{ready: make(chan string, 4), exited: make(chan struct{})}
	b.cmd = exec.Command(bin, "--app", "--parent-pid", strconv.Itoa(os.Getpid()))
	b.cmd.Env = append(append(os.Environ(), "GERDR_FROM_APP=1"), env...)
	hideConsole(b.cmd)
	stdout, _ := b.cmd.StdoutPipe()
	stderr, _ := b.cmd.StderrPipe()
	if err := b.cmd.Start(); err != nil {
		return nil, err
	}
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			if m := readyRe.FindStringSubmatch(strings.TrimSpace(sc.Text())); m != nil {
				b.ready <- m[1]
			}
		}
	}()
	go func() {
		r := bufio.NewReader(stderr)
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			b.mu.Lock()
			b.errTail = tail(b.errTail+string(buf[:n]), 4000)
			b.mu.Unlock()
			if err == io.EOF || err != nil {
				return
			}
		}
	}()
	go func() { _ = b.cmd.Wait(); close(b.exited) }()
	return b, nil
}

func (b *backend) waitReady(d time.Duration) (string, error) {
	select {
	case u := <-b.ready:
		return u, nil
	case <-b.exited:
		// Exit code 0 before any GERDR_READY can't happen with --app (it attaches instead); anything here is a failure.
		return "", fmt.Errorf("backend exited (%v)\n%s", b.cmd.ProcessState, b.stderr())
	case <-time.After(d):
		return "", errors.New("backend not ready after " + d.String())
	}
}

func (b *backend) stderr() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.errTail
}

func tail(s string, n int) string {
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}
