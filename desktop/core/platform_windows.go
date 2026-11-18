package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// What is Windows' in the core (from tools/winshell): no SIGTERM, no background service, the installer as the update.

// hideConsole: gerdr.exe is a console program; without this a console window opens next to ours.
func hideConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000 /* CREATE_NO_WINDOW */}
}

// stop: Windows has no SIGTERM (a kill is a hard stop: the session logs may end mid-line). Quitting, the backend sees
// its parent go and exits by itself (cmd/gerdr waitParent); only a swap (an update, nothing running) kills it.
func (b *backend) stop() {
	select {
	case <-b.exited:
		return
	default:
	}
	if !b.replaced.Load() {
		return
	}
	_ = b.cmd.Process.Kill()
	select {
	case <-b.exited:
	case <-time.After(5 * time.Second):
	}
}

func installedBackend() string { return "" }

// serviceInstalled: Windows has no background service (docs/dev/SERVICE.md is macOS only).
func serviceInstalled() bool { return false }

func logPath(name string) string {
	return filepath.Join(os.Getenv("LOCALAPPDATA"), name, "log.txt")
}

func alert(title, msg string) {} // Electron shows its own box when this process exits (desktop/main.js)

// installApp runs a downloaded installer silently once the App is gone (Electron and this process): it installs over
// this one (same directory) and starts the new one. PowerShell (Windows 10 and later) does the waiting, hidden.
func (s *shell) installApp(file string) bool {
	log.Printf("update: installing %s", file)
	if be := s.currentBackend(); be != nil {
		be.replaced.Store(true)
		be.stop() // its exe has to be replaceable
	}
	quote := func(v string) string { return "'" + strings.ReplaceAll(v, "'", "''") + "'" }
	script := fmt.Sprintf("Wait-Process -Id %d,%d -ErrorAction SilentlyContinue; Start-Process -FilePath %s -ArgumentList '/S'",
		os.Getpid(), shellPid(), quote(file))
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command", script)
	// CREATE_NO_WINDOW only: with DETACHED_PROCESS PowerShell exits at once without running the script (Lightron 0.4.0)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000 /* CREATE_NO_WINDOW */}
	if err := cmd.Start(); err != nil {
		log.Printf("update: %v; opening the installer instead", err)
		if s.app != nil {
			s.app.OpenPath(file, false)
		}
		return false
	}
	return true
}
