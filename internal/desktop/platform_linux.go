package desktop

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

// Linux has no desktop App (no Electron build, no installer): gerdr runs there as a headless backend and CLI, e.g. on
// a server or an Android phone's chroot. These keep the package building so cmd/gerdr compiles for linux; the shell
// itself (shell-core) is not shipped for it.

func hideConsole(*exec.Cmd) {}

// stop: SIGTERM and up to 3s to write its session logs, as on macOS.
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

func installedBackend() string { return "" }

// serviceInstalled: the background service is launchd, macOS only (docs/dev/SERVICE.md).
func serviceInstalled() bool { return false }

func logPath(name string) string {
	dir := os.Getenv("XDG_STATE_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(dir, name, "log.txt")
}

func alert(title, msg string) { log.Printf("%s: %s", title, msg) }

// installApp: there is no App package to install on Linux.
func (s *shell) installApp(file string) bool {
	log.Printf("update: no App install on linux (%s)", file)
	return false
}

// handoff runs bin's core in this process's place (same pid, same stdin / stdout). Returns only if it couldn't.
func handoff(bin string, args []string) {
	err := syscall.Exec(bin, append([]string{bin, "shell-core"}, args...), os.Environ())
	log.Printf("handoff to %s: %v", bin, err)
}
