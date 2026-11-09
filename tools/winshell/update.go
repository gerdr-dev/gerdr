//go:build windows

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/gerdr-dev/gerdr/internal/selfupdate"
)

// The backend, and keeping it current: tools/chromeshell's app.go for Windows. The difference: Windows has no
// background service (docs/dev/SERVICE.md, macOS only), so the backend is this shell's own child, and an update takes
// effect by restarting that child, which cuts off whatever runs in it. Hence the same rule as everywhere: only when
// /api/busy says nothing is running, or the person said "now".

// version: this build's (package.sh: -X main.version), compared with releases by internal/selfupdate.
var version = "0.1.0-dev"

var readyRe = regexp.MustCompile(`^GERDR_READY (\S+)`)

type backend struct {
	cmd      *exec.Cmd
	ready    chan string
	exited   chan struct{}
	replaced atomic.Bool // the shell stopped it to run another (an update): not an exit to react to
	mu       sync.Mutex
	errTail  string
}

func startBackend(bin string) (*backend, error) {
	if _, err := os.Stat(bin); err != nil {
		return nil, fmt.Errorf("Gerdr backend not found: %s", bin)
	}
	b := &backend{ready: make(chan string, 4), exited: make(chan struct{})}
	b.cmd = exec.Command(bin, "--app", "--parent-pid", strconv.Itoa(os.Getpid()))
	b.cmd.Env = append(os.Environ(), "GERDR_FROM_APP=1")
	// gerdr.exe is a console program: without this a console window would open next to ours.
	b.cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000 /* CREATE_NO_WINDOW */}
	out, _ := b.cmd.StdoutPipe()
	errOut, _ := b.cmd.StderrPipe()
	if err := b.cmd.Start(); err != nil {
		return nil, err
	}
	go func() {
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			if m := readyRe.FindStringSubmatch(strings.TrimSpace(sc.Text())); m != nil {
				b.ready <- m[1]
			}
		}
	}()
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := errOut.Read(buf)
			b.mu.Lock()
			b.errTail += string(buf[:n])
			if len(b.errTail) > 4000 {
				b.errTail = b.errTail[len(b.errTail)-4000:]
			}
			b.mu.Unlock()
			if err == io.EOF || err != nil {
				return
			}
		}
	}()
	go func() { _ = b.cmd.Wait(); close(b.exited) }()
	return b, nil
}

func (b *backend) stderr() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.errTail
}

func (b *backend) waitReady(d time.Duration) (string, error) {
	select {
	case u := <-b.ready:
		return u, nil
	case <-b.exited:
		return "", fmt.Errorf("Gerdr backend exited (%v)\n%s", b.cmd.ProcessState, b.stderr())
	case <-time.After(d):
		return "", fmt.Errorf("Gerdr backend wasn't ready after %v", d)
	}
}

// kill: Windows has no SIGTERM (a kill is a hard stop: the session logs may end mid-line), as the Electron shell's
// restartBackend on Windows. Only used to swap in an update, when nothing is running.
func (b *backend) kill() {
	select {
	case <-b.exited:
	default:
		_ = b.cmd.Process.Kill()
		select {
		case <-b.exited:
		case <-time.After(5 * time.Second):
		}
	}
}

type upgradeMark struct {
	version string
	at      time.Time
}

// updates: the shell's state around the backend and its updates.
type updates struct {
	up       *selfupdate.Updater
	mu       sync.Mutex
	be       *backend
	lastBusy *selfupdate.Busy
	applying bool
	marked   upgradeMark
	idleOK   string
}

func (s *shell) backendNow() *backend {
	s.u.mu.Lock()
	defer s.u.mu.Unlock()
	return s.u.be
}

// runBackend starts the backend the updater picks and waits for its address.
func (s *shell) runBackend() (string, error) {
	bin := s.u.up.Backend().Path
	log.Printf("starting backend %s", bin)
	be, err := startBackend(bin)
	if err != nil {
		return "", err
	}
	s.u.mu.Lock()
	s.u.be = be
	s.u.mu.Unlock()
	return be.waitReady(30 * time.Second)
}

// supervise: a backend that exits on its own is started again (after five in five minutes, the person is told).
func (s *shell) supervise() {
	var fails []time.Time
	for {
		be := s.backendNow()
		<-be.exited
		if be.replaced.Load() {
			for s.backendNow() == be {
				time.Sleep(100 * time.Millisecond)
			}
			continue
		}
		log.Printf("backend exited (%v): %s", be.cmd.ProcessState, be.stderr())
		now := time.Now()
		fails = append(fails, now)
		for len(fails) > 0 && now.Sub(fails[0]) > 5*time.Minute {
			fails = fails[1:]
		}
		if len(fails) >= 5 {
			fail("后端反复退出 / The backend keeps exiting:\n\n" + be.stderr())
		}
		time.Sleep(time.Second)
		s.restart(false)
	}
}

// restart swaps the backend for the one the updater picks now, then reloads the page on its address.
func (s *shell) restart(swap bool) {
	if old := s.backendNow(); old != nil && swap {
		old.replaced.Store(true)
		old.kill()
	}
	u, err := s.runBackend()
	if err != nil {
		fail(err.Error())
	}
	s.setEntry(u)
	b, _ := json.Marshal(u)
	s.eval("location.replace(" + string(b) + ")")
}

func (s *shell) fetchBusy() *selfupdate.Busy {
	pu, err := url.Parse(s.getEntry())
	if err != nil {
		return nil
	}
	req, _ := http.NewRequest("GET", pu.Scheme+"://"+pu.Host+"/api/busy", nil)
	if tok := pu.Query().Get("token"); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	c := http.Client{Timeout: 5 * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	var b selfupdate.Busy
	if resp.StatusCode != 200 || json.NewDecoder(resp.Body).Decode(&b) != nil {
		return nil
	}
	return &b
}

func (s *shell) upgradingNow(p *selfupdate.Pending) bool {
	s.u.mu.Lock()
	defer s.u.mu.Unlock()
	return p != nil && s.u.marked.version == p.Version && time.Since(s.u.marked.at) < 2*time.Minute
}

// pollBusy: every 15s, desktop/main.js watchUpdates: is the backend behind what we'd run, and if an update waits and
// nothing is running, apply it.
func (s *shell) pollBusy() {
	time.Sleep(5 * time.Second)
	for {
		if b := s.fetchBusy(); b != nil {
			s.u.mu.Lock()
			prev := s.u.lastBusy
			s.u.lastBusy = b
			idleOK := s.u.idleOK
			s.u.mu.Unlock()
			before := s.u.up.Pending()
			s.u.up.NoteRunning(b.Version)
			p := s.u.up.Pending()
			if (prev == nil || *prev != *b) && p == before {
				s.pushUpdate()
			}
			if p != nil && b.Idle() && !s.upgradingNow(p) && (s.u.up.Auto() || s.u.up.Backend().Bundled || idleOK == p.Version) {
				s.applyUpdate(false)
			}
		}
		time.Sleep(15 * time.Second)
	}
}

func (s *shell) applyUpdate(ask bool) {
	p := s.u.up.Pending()
	if p == nil {
		return
	}
	s.u.mu.Lock()
	if s.u.applying {
		s.u.mu.Unlock()
		return
	}
	s.u.mu.Unlock()
	if ask {
		if b := s.fetchBusy(); !b.Idle() {
			switch askUpdate(p.Version, b) {
			case "later":
				s.u.mu.Lock()
				s.u.idleOK = p.Version
				s.u.mu.Unlock()
				return
			case "now":
			default:
				return
			}
		}
	}
	if p.Kind == "app" {
		// A new shell: its installer, after we quit. Only on a click: it closes the window.
		if ask {
			s.installApp(p.File)
		}
		return
	}
	s.u.mu.Lock()
	s.u.applying = true
	s.u.marked = upgradeMark{p.Version, time.Now()}
	s.u.mu.Unlock()
	s.pushUpdate()
	log.Printf("update: applying %s", p.Version)
	s.restart(true)
	s.u.mu.Lock()
	s.u.applying = false
	s.u.mu.Unlock()
	s.pushUpdate()
}

// askUpdate: Yes = now, No = when idle, Cancel.
func askUpdate(v string, b *selfupdate.Busy) string {
	what := "正在跑的 agent 和终端会被打断。\nRunning agents and terminals will be cut off."
	if b != nil {
		what = fmt.Sprintf("重启会打断 / Restarting cuts off: %d agent, %d 等批准 / waiting, %d 后台任务 / tasks, %d 终端 / terminals.",
			b.Turns, b.Waiting, b.Tasks, b.Terms)
	}
	text := fmt.Sprintf("更新到 %s / Update to %s\n\n%s\n\n是 = 现在更新 / Yes = update now\n否 = 等空闲时自动更新 / No = update when idle", v, v, what)
	t, _ := windows.UTF16PtrFromString(text)
	c, _ := windows.UTF16PtrFromString(appName)
	r, _, _ := procMessageBox.Call(0, uintptr(unsafe.Pointer(t)), uintptr(unsafe.Pointer(c)), 0x3 /* MB_YESNOCANCEL */ |0x20 /* MB_ICONQUESTION */)
	switch r {
	case 6: // IDYES
		return "now"
	case 7: // IDNO
		return "later"
	}
	return ""
}

// pushUpdate: the status to the page (window.gerdr.appUpdate, Settings → 版本).
func (s *shell) pushUpdate() {
	s.u.mu.Lock()
	b := s.u.lastBusy
	s.u.mu.Unlock()
	raw, _ := json.Marshal(s.u.up.Status(b, s.upgradingNow(s.u.up.Pending())))
	s.eval("window.gerdr && window.gerdr.appUpdate && window.gerdr.appUpdate(" + string(raw) + ")")
}

// updateMessage: the page's "update": status asked for, "check now", "update now", the automatic updates switch.
func (s *shell) updateMessage(m map[string]any) {
	switch {
	case m["auto"] != nil:
		on, _ := m["auto"].(bool)
		go s.u.up.SetAuto(on)
	case m["apply"] == true:
		go s.applyUpdate(true)
	case m["check"] == true:
		go func() { log.Printf("update: %s", s.u.up.Check()) }()
	}
	go s.pushUpdate()
}

// installApp runs a downloaded installer (installer.nsi) silently once we're gone, and quits: it installs over this
// one (same directory) and starts the new one. The backend goes first: its exe has to be replaceable. PowerShell (in
// Windows 10 and later) does the waiting, hidden, so nothing is left for us to do after exiting.
func (s *shell) installApp(file string) {
	log.Printf("update: installing %s", file)
	if be := s.backendNow(); be != nil {
		be.replaced.Store(true)
		be.kill()
	}
	quote := func(v string) string { return "'" + strings.ReplaceAll(v, "'", "''") + "'" }
	script := fmt.Sprintf("Wait-Process -Id %d -ErrorAction SilentlyContinue; Start-Process -FilePath %s -ArgumentList '/S'",
		os.Getpid(), quote(file))
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command", script)
	// CREATE_NO_WINDOW only: with DETACHED_PROCESS (no console at all) PowerShell exits at once without running the
	// script, so the installer never ran (found and fixed in Lightron's update.Install, 0.4.0). It outlives us either way.
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000 /* CREATE_NO_WINDOW */}
	if err := cmd.Start(); err != nil {
		log.Printf("update: %v; opening the installer instead", err)
		_ = shellExecute(file)
	}
	os.Exit(0)
}
