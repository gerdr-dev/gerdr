package desktop

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// What is macOS's in the core: signals, the background service (launchd), swapping the .app bundle, the log's place.

func hideConsole(*exec.Cmd) {}

// stop: SIGTERM and up to 3s to write its session logs (shutdown itself is < 0.1s).
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

// serviceInstalled: the background service is on (its plist, like installedBackend and service.Manager.Installed).
func serviceInstalled() bool {
	home, _ := os.UserHomeDir()
	_, err := os.Stat(filepath.Join(home, "Library/LaunchAgents/io.github.zbysir.gerdr.plist"))
	return err == nil
}

func logPath(name string) string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library/Logs", name+".log")
}

func alert(title, msg string) {
	// argv, never spliced into the script
	_ = exec.Command("osascript", "-e", "on run argv\ndisplay alert (item 1 of argv) message (item 2 of argv) as critical\nend run",
		title, msg).Run()
}

// installApp swaps in a downloaded App (a dmg): copy the new bundle out of the dmg next to ours (same volume, so the
// swap is two renames; copying into /Applications in place can be stopped halfway), then a detached script waits for
// this App to go (Electron and this process), swaps, and opens the new one. Off the happy path (translocated, run off
// a dmg, a directory we can't write, not a dmg): open the dmg for the person to drag it over. True: quit now.
func (s *shell) installApp(file string) bool {
	bundle := appBundle()
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
	// Waits for both processes to go, swaps, opens the new one; if a rename fails, puts the old one back and opens the
	// dmg instead.
	script := `
while kill -0 "$1" 2>/dev/null || kill -0 "$5" 2>/dev/null; do sleep 0.2; done
old="$2.old"; rm -rf "$old"
if mv "$2" "$old" && mv "$3" "$2"; then rm -rf "$old" "$4"; open "$2"
else [ -d "$2" ] || mv "$old" "$2"; rm -rf "$3"; open "$2"; open "$4"; fi`
	cmd := exec.Command("/bin/sh", "-c", script, "sh", strconv.Itoa(os.Getpid()), bundle, staged, file, strconv.Itoa(shellPid()))
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // outlives us
	if err := cmd.Start(); err != nil {
		log.Printf("update: %v", err)
		_ = os.RemoveAll(staged)
		return false
	}
	return true
}

// appBundle: Gerdr.app (resources is its Contents/Resources; this binary may be a downloaded backend's).
func appBundle() string {
	b, _ := filepath.Abs(filepath.Join(resources, "..", ".."))
	return b
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

// handoff runs bin's core in this process's place (same pid, same stdin / stdout to Electron). Returns only if it
// couldn't.
func handoff(bin string, args []string) {
	err := syscall.Exec(bin, append([]string{bin, "shell-core"}, args...), os.Environ())
	log.Printf("handoff to %s: %v", bin, err)
}
