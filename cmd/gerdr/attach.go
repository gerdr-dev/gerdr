package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gerdr-dev/gerdr/internal/config"
	"github.com/gerdr-dev/gerdr/internal/service"
	"github.com/gerdr-dev/gerdr/internal/version"
)

func lockPath(env config.Env) string { return filepath.Join(env.Dir, "server.lock") }

// newService：这台机器上的后台服务。plist 里写的是**本进程的二进制**（解过 symlink）：
// 从 App 起的就是 App 包里那个，App 更新了文件跟着换。
func newService(env config.Env, self bool) *service.Manager {
	exe, _ := os.Executable()
	if p, err := filepath.EvalSymlinks(exe); err == nil {
		exe = p
	}
	return service.New(exe, env.Dir, service.EnvSnapshot(), self)
}

// appAttach：App 起的后端不自己干活，连上在跑的那个 gerdr（打印它的 GERDR_READY）。两种情况走到这里：
//   - 开了后台常驻：**始终由后台服务跑**，App 只是个窗口。服务没在跑（被 bootout 过）就让 launchd 起它。
//   - 没开，但数据目录被别的 gerdr 占着（终端里起的）：连过去，同时等着接班 —— 它退了我们拿到锁就自己干，
//     再打一次 GERDR_READY，App 不用重开。
//
// 关掉后台常驻（plist 没了）之后也走「等着接班」那条：服务停了，我们接上。返回 nil = App 没了。
//
// After an update the service still runs the old version: the plist points at the bundle's file (replaced, but the
// process wasn't), or at a backend the App downloaded and has since replaced with a newer one (desktop/updater.js,
// a new path: the plist gets rewritten). A version or binary (buildID) mismatch restarts it, but only once it is
// idle (server busy(): no turn running, nobody asked, no background task, no terminal) — a restart cuts all of those
// off. Until then we connect to the old one and keep checking. GERDR_UPGRADE_NOW=1 (the person said "update now"
// in the App, or the shell just saw it idle) skips the wait, once: for the update this process was started for. After restarting, wait up to 15s for the new one before connecting to the old again.
func appAttach(env config.Env, parentPID int, svc *service.Manager) *os.File {
	gone := make(chan struct{})
	if parentPID > 0 {
		go func() { waitParent(parentPID); close(gone) }()
	}
	self := buildID()
	now := os.Getenv("GERDR_UPGRADE_NOW") == "1"
	var announced string
	var restarted, checked, healed time.Time
	for {
		// Installed but not loaded (booted out, or a bootstrap that failed — e.g. right after an update rewrote the
		// plist): load it again. Not only at start: a failed restart used to leave the App with no backend at all
		// until something started a new attacher. Rate-limited so a service that keeps dying isn't hammered.
		if svc.Installed() && time.Since(healed) >= 15*time.Second && svc.PID() == 0 {
			healed = time.Now()
			if err := svc.Install(); err != nil {
				fmt.Fprintln(os.Stderr, "load background service:", err)
			}
		}
		if !svc.Installed() {
			if l, err := lockFile(lockPath(env)); err == nil {
				return l
			}
		}
		// Re-read every round: `make install` swaps the bundle under a running App, and the service should follow
		// (once idle) without the App being reopened. A new file is a new chance to restart.
		if b := buildID(); b != self {
			// GERDR_UPGRADE_NOW was the person's (or the idle shell's) go-ahead for the update this process was
			// started for, not for every later swap: kept for the whole life of the process, each `make install`
			// after it restarted the service at once, cutting off running turns (2026-10-30). A new file waits for idle.
			self, restarted, now = b, time.Time{}, false
		}
		if si, u, ok := running(env); ok {
			moved := si.Service && svc.InstalledExec() != svc.Exec
			// Build (the file's mtime + size) is what tells: our compiled-in version stays the old one after the
			// file under us was replaced. Version only for a gerdr too old to report Build.
			stale := moved || (si.Build != "" && si.Build != self) || (si.Build == "" && si.Version != version.Version)
			idle := false
			if stale && si.Service && restarted.IsZero() && (now || time.Since(checked) >= 10*time.Second) {
				checked = time.Now()
				idle = now || serviceIdle()
			}
			switch {
			case idle && moved:
				// Rewrites the plist to our binary; bootout + bootstrap restarts it.
				if err := svc.Install(); err != nil {
					fmt.Fprintln(os.Stderr, "restart background service:", err)
				}
				restarted, healed, now = time.Now(), time.Now(), false
			case idle:
				_ = svc.Restart()
				restarted, healed, now = time.Now(), time.Now(), false
			case u != announced && (!stale || restarted.IsZero() || time.Since(restarted) > 15*time.Second):
				fmt.Println("GERDR_READY " + u)
				announced = u
			}
		}
		select {
		case <-gone:
			return nil
		case <-time.After(time.Second):
		}
	}
}

// serviceIdle: nothing a restart of the running gerdr would cut off (server Busy). A gerdr from before /api/busy
// existed answers 404: fall back to the session list it does have (no terminal count there).
func serviceIdle() bool {
	c, err := newAPIClient()
	if err != nil {
		return false
	}
	c.http.Timeout = 3 * time.Second
	var b struct{ Turns, Waiting, Tasks, Terms int }
	if err := c.do("GET", "/api/busy", nil, &b); err == nil {
		return b.Turns+b.Waiting+b.Tasks+b.Terms == 0
	}
	var list []struct {
		Turn    any `json:"turn"`
		Pending int `json:"pending"`
		Tasks   []struct {
			Status string `json:"status"`
		} `json:"tasks"`
	}
	if c.do("GET", "/api/sessions", nil, &list) != nil {
		return false
	}
	for _, x := range list {
		if x.Turn != nil || x.Pending > 0 {
			return false
		}
		for _, t := range x.Tasks {
			if t.Status == "running" {
				return false
			}
		}
	}
	return true
}
