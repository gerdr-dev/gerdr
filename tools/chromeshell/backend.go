package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/gerdr-dev/gerdr/internal/config"
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

// stop: SIGTERM and up to 3s to write its session logs (shutdown itself is < 0.1s), as desktop/main.js stopBackend.
func (b *backend) stop() {
	select {
	case <-b.exited:
		return
	default:
	}
	_ = b.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-b.exited:
	case <-time.After(3 * time.Second):
		log.Print("backend didn't stop in 3s, killing it")
		_ = b.cmd.Process.Kill()
	}
}

// pageURL: the page, optionally on one project. The ready url is the login link (/auth?token=…): the page goes in its
// `next`, never appended to the path (that made a wrong token once, "bad token"; desktop/main.js pageURL).
func pageURL(entry, space string) string {
	u, err := url.Parse(entry)
	if err != nil {
		return entry
	}
	page := "/"
	if space != "" {
		page = "/" + url.PathEscape(space)
	}
	if u.Path == "/auth" {
		q := u.Query()
		q.Set("next", page)
		u.RawQuery = q.Encode()
	} else {
		u.Path, u.RawQuery = page, ""
	}
	return u.String()
}

func tail(s string, n int) string {
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}

// runningGerdr: the login link of a gerdr already serving this data dir (server.json, alive, answering), the way
// `gerdr` itself finds one (cmd/gerdr running). The shell then is only a window on it: it starts no backend of its
// own, so it can't trigger appAttach's "different binary → restart the service when idle".
func runningGerdr(env config.Env) (string, bool) {
	b, err := os.ReadFile(filepath.Join(env.Dir, "server.json"))
	if err != nil {
		return "", false
	}
	var si struct {
		PID  int    `json:"pid"`
		Port int    `json:"port"`
		Host string `json:"host"`
	}
	if json.Unmarshal(b, &si) != nil || si.Port == 0 {
		return "", false
	}
	if p, err := os.FindProcess(si.PID); err != nil || p.Signal(syscall.Signal(0)) != nil {
		return "", false
	}
	host := si.Host
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	addr := net.JoinHostPort(host, strconv.Itoa(si.Port))
	c := http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get("http://" + addr + "/api/hello")
	if err != nil {
		return "", false
	}
	resp.Body.Close()
	tok := env.Token
	if tok == "" {
		tok, _ = config.LoadToken(env.Dir)
	}
	return "http://" + addr + "/auth?token=" + url.QueryEscape(tok), true
}

// installedBackend: the gerdr the background service runs (its plist), else the one in Gerdr.app. Started with --app
// when no gerdr is running, exactly as Gerdr.app would.
func installedBackend() string {
	home, _ := os.UserHomeDir()
	if out, err := exec.Command("plutil", "-extract", "ProgramArguments.0", "raw", "-o", "-",
		filepath.Join(home, "Library/LaunchAgents/io.github.zbysir.gerdr.plist")).Output(); err == nil {
		if p := strings.TrimSpace(string(out)); p != "" {
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	return "/Applications/Gerdr.app/Contents/Resources/gerdr"
}
