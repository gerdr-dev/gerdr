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
// App 更新过的话，后台服务跑的还是旧版（plist 指着 App 包里的文件，文件换了、进程没换）：
// 版本或二进制（buildID）对不上就 kickstart -k 重启它，等新版起来再连（最多 15 秒，过了就先连着旧的）。
func appAttach(env config.Env, parentPID int, svc *service.Manager) *os.File {
	gone := make(chan struct{})
	if parentPID > 0 {
		go func() { waitParent(parentPID); close(gone) }()
	}
	if svc.Installed() && svc.PID() == 0 {
		_ = svc.Install() // 装着但没加载：重新 bootstrap 一次
	}
	self := buildID()
	var announced string
	var restarted time.Time
	for {
		if !svc.Installed() {
			if l, err := lockFile(lockPath(env)); err == nil {
				return l
			}
		}
		if si, u, ok := running(env); ok {
			stale := si.Version != version.Version || si.Build != self
			switch {
			case stale && si.Service && restarted.IsZero():
				_ = svc.Restart()
				restarted = time.Now()
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
