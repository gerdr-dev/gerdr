package projbrowser

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/gerdr-dev/gerdr/internal/i18n"
)

// The Electron App's program as the projects' browser while the App is closed (docs/dev/WHY-ELECTRON-COMEBACK.md):
// started with --gerdr-browser, it has no window of the UI and no Dock icon, connects back like the App does
// (/api/browser/ext, chromeext/app.go, "browserOnly" in its hello) and opens the pages in the same partitions, so a
// site signed in to in the App is signed in here. It quits after ten minutes with no page; the App starting stops it
// (desktop/main.js asks it to go before taking the data directory, and Release asks too).

var electronMu sync.Mutex

func (m *Manager) startElectron(ctx context.Context, a appInfo) error {
	electronMu.Lock()
	defer electronMu.Unlock()
	if m.Bridge.AppConnected() {
		return nil
	}
	if m.BackendLink == "" {
		return errors.New("project browser: the backend's address isn't known yet")
	}
	args := append(append([]string{}, a.Args...), "--gerdr-browser")
	cmd := exec.Command(a.Exe, args...)
	// the link carries the token: in the environment, not on a command line every process can read
	cmd.Env = append(os.Environ(), "GERDR_BROWSER_BACKEND="+m.BackendLink)
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	m.Log.Info("project browser: started the App's program as the projects' browser", "exe", a.Exe)
	until := time.Now().Add(30 * time.Second)
	for !m.Bridge.AppConnected() {
		if time.Now().After(until) {
			return errors.New(i18n.S("App 的浏览器 30 秒没连上", "The App's browser didn't connect within 30 seconds"))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return nil
}
