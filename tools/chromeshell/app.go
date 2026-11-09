package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/gerdr-dev/gerdr/internal/i18n"
	"github.com/gerdr-dev/gerdr/internal/selfupdate"
)

// The shell's side of updating and of keeping the backend up: desktop/main.js's startBackend / restartBackend /
// fetchBusy / watchUpdates / applyUpdate / pushUpdate, over our backend.go and updater.go.

type upgradeMark struct {
	version string
	at      time.Time
}

func (s *shell) setBackend(be *backend) {
	s.beMu.Lock()
	s.be = be
	s.beMu.Unlock()
	// A second GERDR_READY: the gerdr it attached to went away and it took over (appAttach). Same address: the
	// page's WebSocket just reconnects. Another one: load the windows there.
	go func() {
		for u := range be.ready {
			if originOf(u) != s.origin {
				log.Printf("backend moved to %s: reloading the windows", originOf(u))
				s.setEntry(u)
				s.forWins(func(w *win) {
					_, _ = s.c.call(w.session, "Page.navigate", map[string]any{"url": pageURL(s.entry, w.space)})
				})
			}
		}
	}()
}

func (s *shell) currentBackend() *backend {
	s.beMu.Lock()
	defer s.beMu.Unlock()
	return s.be
}

// superviseBackend: a backend that exits on its own is started again. Exit 75 = it just handed over to the
// background service (cmd/gerdr exitHandover): start one that only attaches. Anything else: logged, started again,
// and after a few in a row the person is told.
func (s *shell) superviseBackend() {
	var fails []time.Time
	for {
		be := s.currentBackend()
		<-be.exited
		if s.quitting.Load() {
			return
		}
		if be.replaced.Load() { // restartBackend swapped it: wait for the new one
			for s.currentBackend() == be && !s.quitting.Load() {
				time.Sleep(100 * time.Millisecond)
			}
			continue
		}
		code := be.cmd.ProcessState.ExitCode()
		log.Printf("backend exited (%d): %s", code, tail(be.stderr(), 1500))
		if code != 75 {
			now := time.Now()
			fails = append(fails, now)
			for len(fails) > 0 && now.Sub(fails[0]) > 5*time.Minute {
				fails = fails[1:]
			}
			if len(fails) >= 5 {
				fatal(i18n.S("后端反复退出：\n\n", "The backend keeps exiting:\n\n") + tail(be.stderr(), 1500))
			}
			time.Sleep(time.Second)
		}
		s.startNewBackend(false)
	}
}

// startNewBackend runs the backend the updater picks; now: GERDR_UPGRADE_NOW, so its appAttach restarts an outdated
// service without waiting for it to go idle (the person said "now", or we just saw it idle).
func (s *shell) startNewBackend(now bool) {
	bin := s.bin
	if s.up != nil && s.up.Enabled() {
		bin = s.up.Backend().Path
	}
	var env []string
	if now {
		env = []string{"GERDR_UPGRADE_NOW=1"}
	}
	log.Printf("starting backend %s (now=%v)", bin, now)
	be, err := startBackend(bin, env)
	if err != nil {
		fatal(err.Error())
	}
	s.setBackend(be)
}

// restartBackend: the running backend for the one the updater picks now. Windows-free here: SIGTERM, up to 3s.
func (s *shell) restartBackend(now bool) {
	if old := s.currentBackend(); old != nil {
		old.replaced.Store(true)
		old.stop()
	}
	s.startNewBackend(now)
}

func (s *shell) stopBackend() {
	s.quitting.Store(true)
	if be := s.currentBackend(); be != nil {
		be.stop()
	}
}

// fetchBusy: what a restart would cut off, from the gerdr the windows show. nil = couldn't ask.
func (s *shell) fetchBusy() *selfupdate.Busy {
	u, err := url.Parse(s.entry)
	if err != nil {
		return nil
	}
	req, _ := http.NewRequest("GET", s.origin+"/api/busy", nil)
	if tok := u.Query().Get("token"); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	c := http.Client{Timeout: 5 * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil
	}
	var b selfupdate.Busy
	if json.NewDecoder(resp.Body).Decode(&b) != nil {
		return nil
	}
	return &b
}

// upgradingNow: the update being swapped in, until the running gerdr reports it (Settings shows "applying"); also keeps
// the poll from restarting again and again when a swap didn't take.
func (s *shell) upgradingNow(p *selfupdate.Pending) bool {
	s.upMu.Lock()
	defer s.upMu.Unlock()
	return p != nil && s.upgrading.version == p.Version && time.Since(s.upgrading.at) < 2*time.Minute
}

// pollBusy: every 15s, what a restart would cut off; whether the running gerdr is behind what we'd run; and if an
// update waits and nothing would be cut off, apply it (with automatic updates on, or always when it is just catching the
// service up with the App's own backend).
func (s *shell) pollBusy() {
	time.Sleep(5 * time.Second)
	for !s.quitting.Load() {
		if b := s.fetchBusy(); b != nil {
			s.upMu.Lock()
			prev := s.lastBusy
			s.lastBusy = b
			s.upMu.Unlock()
			changed := prev == nil || *prev != *b
			before := s.up.Pending()
			s.up.NoteRunning(b.Version)
			p := s.up.Pending()
			if changed && p == before {
				s.pushUpdate()
			}
			if p != nil && p.Kind == "backend" && b.Idle() && !s.upgradingNow(p) {
				s.upMu.Lock()
				idleOK := s.idleOK == p.Version
				s.upMu.Unlock()
				if s.up.Auto() || s.up.Backend().Bundled || idleOK {
					s.applyUpdate(false)
				}
			}
		}
		time.Sleep(15 * time.Second)
	}
}

// applyUpdate restarts the backend onto the pending update. ask: the person clicked "update"; if something would be
// cut off, say what and let them pick (now / when idle / cancel).
func (s *shell) applyUpdate(ask bool) {
	p := s.up.Pending()
	if p == nil {
		return
	}
	s.upMu.Lock()
	if s.applying {
		s.upMu.Unlock()
		return
	}
	s.upMu.Unlock()
	if ask {
		if b := s.fetchBusy(); !b.Idle() {
			switch askUpdate(p.Version, b) {
			case "later":
				s.upMu.Lock()
				s.idleOK = p.Version
				s.upMu.Unlock()
				return
			case "now":
			default:
				return
			}
		}
	}
	if p.Kind == "app" {
		// A new shell: swapped in after we quit (desktop/updater.js installApp). Only on a click: it closes the windows.
		if !ask {
			return
		}
		log.Printf("update: installing the %s App", p.Version)
		if installApp(p.File) {
			s.quitForUpdate <- struct{}{}
		}
		return
	}
	s.upMu.Lock()
	s.applying = true
	s.upgrading = upgradeMark{p.Version, time.Now()}
	s.upMu.Unlock()
	s.pushUpdate()
	log.Printf("update: applying %s", p.Version)
	s.restartBackend(true)
	s.upMu.Lock()
	s.applying = false
	s.upMu.Unlock()
	s.pushUpdate()
}

// askUpdate: a dialog like desktop/main.js's. "now", "later" (when idle) or "" (cancelled).
func askUpdate(version string, b *selfupdate.Busy) string {
	detail := i18n.S("正在跑的 agent 和终端会被打断。", "Running agents and terminals will be cut off.")
	if b != nil {
		var parts []string
		add := func(n int, zh, en string) {
			if n > 0 {
				parts = append(parts, fmt.Sprintf(i18n.S("%d %s", "%d %s"), n, i18n.S(zh, en)))
			}
		}
		add(b.Turns, "个 agent 正在跑", "running agent(s)")
		add(b.Waiting, "个在等你批准", "waiting for your approval")
		add(b.Tasks, "个后台任务", "background task(s)")
		add(b.Terms, "个终端", "terminal(s)")
		detail = i18n.S("重启会打断：", "Restarting cuts off: ") + strings.Join(parts, i18n.S("、", ", ")) +
			i18n.S("。空闲的会话不受影响，下次发话自己接上。", ". Idle sessions aren't affected and pick up on your next message.")
	}
	now, later, cancel := i18n.S("现在更新", "Update now"), i18n.S("等空闲时自动更新", "Update when idle"), i18n.S("取消", "Cancel")
	title := fmt.Sprintf(i18n.S("更新到 %s", "Update to %s"), version)
	// argv, never spliced into the script
	script := `on run argv
display dialog (item 2 of argv) with title (item 1 of argv) buttons {(item 5 of argv), (item 4 of argv), (item 3 of argv)} default button 2 cancel button 1
return button returned of result
end run`
	out, err := exec.Command("osascript", "-e", script, title, detail, now, later, cancel).Output()
	if err != nil {
		return ""
	}
	switch strings.TrimSpace(string(out)) {
	case now:
		return "now"
	case later:
		return "later"
	}
	return ""
}

// pushUpdate tells every page (window.gerdr.appUpdate, ui/src/lib/native.ts onAppUpdate) and the menu.
func (s *shell) pushUpdate() {
	if s.up == nil {
		return
	}
	s.upMu.Lock()
	b := s.lastBusy
	s.upMu.Unlock()
	st := s.up.Status(b, s.upgradingNow(s.up.Pending()))
	raw, _ := json.Marshal(st)
	s.forWins(func(w *win) {
		_, _ = s.c.eval(w.session, "window.gerdr && window.gerdr.appUpdate && window.gerdr.appUpdate("+string(raw)+")")
	})
	if s.menu != nil {
		s.menu.update(nil, "")
	}
}

// updateMessage: the page's "update" message: asking for the status (it just loaded), "check now", "update now",
// the automatic updates switch (desktop/main.js ipc "update").
func (s *shell) updateMessage(m map[string]any) {
	if s.up == nil {
		return
	}
	switch {
	case m["auto"] != nil:
		on, _ := m["auto"].(bool)
		s.up.SetAuto(on)
	case m["apply"] == true:
		go s.applyUpdate(true)
	case m["check"] == true:
		go func() { log.Printf("update: %s", s.up.Check()) }()
	}
	s.pushUpdate()
}

// checkFromMenu: the menu's Check for Updates / Update to x…
func (s *shell) checkFromMenu() {
	if s.up == nil {
		return
	}
	if s.up.Pending() != nil {
		s.applyUpdate(true)
		return
	}
	msg := s.up.Check()
	if s.up.Pending() != nil { // found one (or the automatic check had, a moment ago): this click is the go-ahead
		s.applyUpdate(true)
		return
	}
	if s.up.Pending() == nil {
		script := `on run argv
display dialog (item 2 of argv) with title (item 1 of argv) buttons {"OK"} default button 1
end run`
		_ = exec.Command("osascript", "-e", script, i18n.S("检查更新", "Check for Updates"), msg).Run()
	}
}

func (s *shell) forWins(f func(w *win)) {
	s.mu.Lock()
	ws := make([]*win, 0, len(s.wins))
	for _, w := range s.wins {
		ws = append(ws, w)
	}
	s.mu.Unlock()
	for _, w := range ws {
		f(w)
	}
}

var _ atomic.Bool // (sync/atomic: backend.replaced, shell.quitting)

// installApp swaps in a downloaded App (a dmg), the way desktop/updater.js does: copy the new bundle out of the dmg
// next to ours (same volume, so the swap is two renames; copying into /Applications in place can be stopped halfway,
// Makefile install), then a detached script waits for this process to go, swaps, and opens the new one. Off the
// happy path (translocated, run off a dmg, a directory we can't write, not a dmg): open the dmg for the person to drag
// it over. True: the caller should quit now.
func installApp(file string) bool {
	exe, _ := os.Executable()
	bundle, _ := filepath.Abs(filepath.Join(filepath.Dir(exe), "..", ".."))
	ok := strings.HasSuffix(bundle, ".app") && !strings.Contains(bundle, "/AppTranslocation/") && !strings.HasPrefix(bundle, "/Volumes/") &&
		strings.HasSuffix(file, ".dmg") && writable(filepath.Dir(bundle))
	if !ok {
		_ = exec.Command("open", file).Start()
		return false
	}
	staged := filepath.Join(filepath.Dir(bundle), ".Gerdr-update.app")
	mnt := filepath.Join(filepath.Dir(file), "mnt")
	_ = os.RemoveAll(staged)
	_ = os.MkdirAll(mnt, 0o755)
	err := func() error {
		if out, err := exec.Command("hdiutil", "attach", "-nobrowse", "-readonly", "-noautoopen", "-mountpoint", mnt, file).CombinedOutput(); err != nil {
			return fmt.Errorf("hdiutil attach: %v %s", err, out)
		}
		defer exec.Command("hdiutil", "detach", "-quiet", mnt).Run()
		names, _ := os.ReadDir(mnt)
		for _, n := range names {
			if strings.HasSuffix(n.Name(), ".app") {
				if out, err := exec.Command("ditto", filepath.Join(mnt, n.Name()), staged).CombinedOutput(); err != nil {
					return fmt.Errorf("ditto: %v %s", err, out)
				}
				return nil
			}
		}
		return fmt.Errorf("no .app in %s", file)
	}()
	if err != nil {
		log.Printf("update: %v; opening the dmg instead", err)
		_ = os.RemoveAll(staged)
		_ = exec.Command("open", file).Start()
		return false
	}
	// Waits for this process to go, swaps, opens the new one; if a rename fails, puts the old one back and opens the
	// dmg instead.
	script := `
while kill -0 "$1" 2>/dev/null; do sleep 0.2; done
old="$2.old"; rm -rf "$old"
if mv "$2" "$old" && mv "$3" "$2"; then rm -rf "$old" "$4"; open "$2"
else [ -d "$2" ] || mv "$old" "$2"; rm -rf "$3"; open "$2"; open "$4"; fi`
	cmd := exec.Command("/bin/sh", "-c", script, "sh", strconv.Itoa(os.Getpid()), bundle, staged, file)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // outlives us
	if err := cmd.Start(); err != nil {
		log.Printf("update: %v", err)
		_ = os.RemoveAll(staged)
		return false
	}
	return true
}

func writable(dir string) bool {
	f, err := os.CreateTemp(dir, ".gerdr-write-test-")
	if err != nil {
		return false
	}
	f.Close()
	os.Remove(f.Name())
	return true
}
