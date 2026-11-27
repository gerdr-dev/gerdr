package desktop

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gerdr-dev/gerdr/internal/i18n"
	"github.com/gerdr-dev/gerdr/internal/selfupdate"
)

// The shell's side of updating and of keeping the backend up: desktop/main.js's startBackend / restartBackend /
// fetchBusy / watchUpdates / applyUpdate / pushUpdate, over backend.go and internal/selfupdate.

// shellStarted: when this App process started (pushUpdate).
var shellStarted = time.Now()

type upgradeMark struct {
	version string
	at      time.Time
}

func (s *shell) setBackend(be *backend) {
	s.beMu.Lock()
	s.be = be
	s.beMu.Unlock()
	// A second GERDR_READY: the gerdr it attached to went away and it took over (appAttach). Same address: the
	// page's WebSocket just reconnects. Another port: the browser's mapping of the pages' host (main.go pageHost) is
	// fixed at its start, so the windows would keep reaching the old one until Gerdr is opened again.
	go func() {
		for u := range be.ready {
			if s.backendEntry != "" && originOf(u) != originOf(s.backendEntry) {
				log.Printf("backend moved to %s: the windows follow once Gerdr is opened again", originOf(u))
			}
			if s.backendEntry != "" {
				s.backendEntry = u
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
		bin = s.up.Place(s.up.Backend())
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
	u, err := url.Parse(s.backendEntry)
	if err != nil {
		return nil
	}
	req, _ := http.NewRequest("GET", originOf(s.backendEntry)+"/api/busy", nil)
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
					s.applyUpdate(false, false)
				}
			}
		}
		time.Sleep(15 * time.Second)
	}
}

// applyUpdate restarts the backend onto the pending update. ask: the person clicked "update"; if something would be
// cut off, say what and let them pick (now / when idle / cancel). confirmed: they already agreed to cut it off, on
// a device without this dialog (a phone over remote access, server/appupdate.go), so don't ask on the computer.
func (s *shell) applyUpdate(ask, confirmed bool) {
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
	// A new App with the background service on cuts nothing off: the agents run in the service, which carries on while
	// the windows close and reopen, and appAttach moves it to the new backend once idle (docs/dev/UPDATE.md). The page
	// already said so on the button that got us here; asking again here said the opposite.
	app := p.Kind == "app"
	if ask && !confirmed && !(app && serviceInstalled()) {
		if b := s.fetchBusy(); !b.Idle() {
			switch s.askUpdate(p.Version, b, !app) {
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
	if app {
		// A new shell: swapped in after we quit (desktop/updater.js installApp). Only on a click: it closes the windows.
		if !ask || !s.up.AppStillNewer() {
			return
		}
		log.Printf("update: installing the %s App", p.Version)
		if s.installApp(p.File) {
			s.app.Quit()
		}
		return
	}
	// A backend whose core or shell script differ from this one's (HotID) takes effect only in a new App process
	// (docs/dev/SHELL.md): the App starts again, its windows reopen, the agents in the background service run on. Not
	// on its own while a window of the App is in front: the person may be typing in it; the next idle poll tries again.
	if next := hotIDOf(s.up.Backend().Path); next != "" && next != HotID() {
		if !ask && s.app.Focused() {
			return
		}
		log.Printf("update: %s has another core / shell script (%s, running %s): starting the App again", p.Version, next, HotID())
		s.upMu.Lock()
		s.upgrading = upgradeMark{p.Version, time.Now()}
		s.upMu.Unlock()
		// The new App process has to carry on with the "now": otherwise it only attaches to the busy service, which
		// waits for idle, and the click looked like it did nothing (user report: three taps on a phone's "Restart now").
		if ask {
			s.markUpgradeNow(p.Version)
		}
		if err := s.app.Relaunch(); err == nil {
			return
		}
		s.takeUpgradeNow()
		log.Printf("update: relaunch failed: restarting the backend only")
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

// upgradeNowFile: a clicked update's "now", across the App's relaunch (applyUpdate → startBackend of the next process).
func (s *shell) upgradeNowFile() string { return filepath.Join(s.dataDir, "upgrade-now") }

func (s *shell) markUpgradeNow(version string) {
	if s.dataDir == "" {
		return
	}
	if err := os.WriteFile(s.upgradeNowFile(), []byte(version), 0o600); err != nil {
		log.Printf("update: %v", err)
	}
}

// takeUpgradeNow: the version a relaunch was asked to update to now, "" if none; read once. Older than a couple
// of minutes it is left over from a relaunch that didn't happen, not a click the person is waiting on.
func (s *shell) takeUpgradeNow() string {
	if s.dataDir == "" {
		return ""
	}
	f := s.upgradeNowFile()
	st, err := os.Stat(f)
	if err != nil {
		return ""
	}
	b, _ := os.ReadFile(f)
	_ = os.Remove(f)
	if time.Since(st.ModTime()) > 2*time.Minute {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// askUpdate: "now", "later" (when idle) or "" (cancelled). later: offer "when idle"; only a backend update has it, a
// new App waits for a click (pollBusy applies backends only), so "when idle" would do nothing.
func (s *shell) askUpdate(version string, b *selfupdate.Busy, later bool) string {
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
	title := fmt.Sprintf(i18n.S("更新到 %s", "Update to %s"), version)
	now, idle, cancel := i18n.S("现在更新", "Update now"), i18n.S("等空闲时自动更新", "Update when idle"), i18n.S("取消", "Cancel")
	if later {
		switch s.app.Dialog("question", title, detail, []string{now, idle, cancel}, 1, 2) {
		case 0:
			return "now"
		case 1:
			return "later"
		}
		return ""
	}
	if s.app.Dialog("question", title, detail, []string{now, cancel}, 0, 1) == 0 {
		return "now"
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
	// Which App process this is: after a relaunch the old one's last report stays fresh on the backend for a while,
	// and buttons pressed on a phone went to it, to nobody (server/appupdate.go better).
	st["shellStartedAt"] = shellStarted.UnixMilli()
	raw, _ := json.Marshal(st)
	s.mu.Lock()
	a := s.app
	s.mu.Unlock()
	if a == nil {
		return
	}
	for _, w := range a.Windows() {
		_ = w.Eval("window.gerdr && window.gerdr.appUpdate && window.gerdr.appUpdate(" + string(raw) + ")")
	}
	s.refreshMenu()
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
		go s.applyUpdate(true, m["force"] == true)
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
		s.applyUpdate(true, false)
		return
	}
	msg := s.up.Check()
	if s.up.Pending() != nil { // found one (or the automatic check had, a moment ago): this click is the go-ahead
		s.applyUpdate(true, false)
		return
	}
	if s.up.Pending() == nil {
		s.app.Dialog("info", i18n.S("检查更新", "Check for Updates"), msg, []string{"OK"}, 0, 0)
	}
}

var _ atomic.Bool // (sync/atomic: backend.replaced, shell.quitting)
