// The macOS desktop shell: Gerdr on the user's own Chrome (or Edge, Brave), through Lightron
// (github.com/zbysir/lightron), which makes the browser copy that is Gerdr to macOS, opens the --app windows, the menu
// bar and the Dock. What is Gerdr's lives here: the backend (`gerdr --app`, backend.go), its updates (app.go,
// internal/selfupdate), the window.gerdrNative bridge the UI knows (gerdr.go, bridge.js), the menus (menu.go).
//
//	go run ./tools/chromeshell -gerdr bin/gerdr    # with GERDR_DIR / GERDR_PORT for a debug instance
package main

import (
	_ "embed"
	"flag"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/zbysir/lightron"

	"github.com/gerdr-dev/gerdr/internal/selfupdate"
)

//go:embed bridge.js
var bridgeJS string

var (
	appName string
	alerts  bool // opened as an App: errors as an alert, nobody reads the log
)

func main() {
	// Opened as an App (package.sh): no flags, so the defaults are the App's, named after it (its Info.plist).
	exePath, _ := os.Executable()
	packaged := strings.Contains(exePath, ".app/Contents/MacOS/")
	home, _ := os.UserHomeDir()
	resources := filepath.Join(filepath.Dir(exePath), "../Resources")
	plist := filepath.Join(filepath.Dir(exePath), "../Info.plist")
	defData, defName, bundleID := filepath.Join(os.TempDir(), "gerdr-chromeshell"), "Gerdr dev shell", "io.github.zbysir.gerdr.devshell"
	if packaged {
		defName, _ = plistString(plist, "CFBundleName")
		bundleID, _ = plistString(plist, "CFBundleIdentifier")
		if defName == "" {
			defName = "Gerdr"
		}
		// "<name> Shell", not "<name>": ~/Library/Application Support/Gerdr is the Electron App's (window.json, its
		// downloaded backends), left as it was.
		defData = filepath.Join(home, "Library/Application Support", defName+" Shell")
	}
	gerdrBin := flag.String("gerdr", "", "run this gerdr backend (--app); default: the bundle's (or the installed one)")
	dataDir := flag.String("data", defData, "the shell's directory: the browser profile, the engine, downloaded backends")
	name := flag.String("name", defName, "the app's name (Dock, ⌘Tab, notifications)")
	flag.Parse()
	log.SetFlags(log.Ltime | log.Lmicroseconds)
	if packaged {
		// Opened from Finder nothing reads stderr: a log file, and failures as an alert.
		logPath := filepath.Join(home, "Library/Logs", defName+".log")
		if st, err := os.Stat(logPath); err == nil && st.Size() > 5<<20 {
			_ = os.Rename(logPath, logPath+".old")
		}
		if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
			log.SetOutput(f)
		}
		alerts = true
	}
	appName = *name
	log.Printf("---- %s starting (%s)", appName, exePath)
	_ = os.MkdirAll(*dataDir, 0o755)
	moveLayout(*dataDir)

	icon, err := os.ReadFile(filepath.Join(resources, "icon.png"))
	if !packaged {
		icon, err = os.ReadFile(filepath.Join(repoRoot(), "desktop/res/icon.png"))
	}
	if err != nil {
		log.Printf("icon: %v", err)
	}

	// The backend, always as Gerdr.app runs it: `gerdr --app` (cmd/gerdr). It attaches to the gerdr already serving
	// (the background service) and, when that runs another binary than this one, restarts it on this one once nothing
	// is running (appAttach): that is how an update, downloaded or installed with the App, reaches the service.
	// The binary: the newer of the bundle's and a downloaded one (internal/selfupdate); -gerdr picks one, for debugging.
	bundledBin, appVersion, shellID := *gerdrBin, "", ""
	if packaged {
		bundledBin = filepath.Join(resources, "gerdr")
		appVersion, _ = plistString(plist, "CFBundleShortVersionString")
		shellID, _ = plistString(plist, "GerdrShell")
	}
	sh := &shell{spaces: map[string]string{}, notes: map[string]time.Time{}}
	sh.up = selfupdate.New(*dataDir, bundledBin, appVersion, shellID, packaged && *gerdrBin == "")
	switch {
	case *gerdrBin != "":
		sh.bin = *gerdrBin
	case fileExists(bundledBin):
		sh.bin = sh.up.Backend().Path
	default:
		sh.bin = installedBackend()
	}

	err = lightron.Run(lightron.Options{
		Name:    appName,
		ID:      bundleID,
		Version: appVersion,
		Icon:    icon,
		// Started only once this is the one instance (a second launch just brings the first one's window up).
		URLFunc: sh.startBackend,
		// The pages live at http://gerdr/, which the browser maps to the backend's address: a notification says where
		// it comes from, and "gerdr" reads better there than 127.0.0.1:7870. Also the origin the UI's localStorage
		// (per-device settings) has been kept under since 0.2.23: changing it resets them.
		Host:      "gerdr",
		Window:    lightron.WindowOptions{Width: 1280, Height: 820},
		Script:    bridgeJS,
		Menu:      sh.menu(),
		DockMenu:  sh.dockMenu(),
		OnMessage: sh.message,
		DataDir:   *dataDir,
		Logf:      log.Printf,
		OnReady: func(a *lightron.App) {
			sh.setApp(a)
			go sh.superviseBackend()
			sh.up.Start(sh.pushUpdate)
			go sh.pollBusy()
		},
	})
	sh.stopBackend()
	switch {
	case err == lightron.ErrNoBrowser:
		os.Exit(1) // Lightron said so (an alert offering Chrome's download page)
	case err != nil:
		fatal(err.Error())
	}
}

// moveLayout: the shell before Lightron kept the browser profile in Chrome/ and the engine in Chrome-engine/;
// Lightron's are profile/ and engine/. The profile moves (the UI's settings, notification and clipboard grants, window
// sizes); the engine is made again (an APFS clone, a second).
func moveLayout(dir string) {
	old, now := filepath.Join(dir, "Chrome"), filepath.Join(dir, "profile")
	if _, err := os.Stat(now); os.IsNotExist(err) {
		if _, err := os.Stat(old); err == nil {
			if err := os.Rename(old, now); err != nil {
				log.Printf("profile: %v", err)
			} else {
				log.Print("profile: moved Chrome/ to profile/")
			}
		}
	}
	_ = os.RemoveAll(filepath.Join(dir, "Chrome-engine"))
}

func fatal(msg string) {
	log.Print("fatal: ", msg)
	if alerts {
		// argv, never spliced into the script
		_ = exec.Command("osascript", "-e", "on run argv\ndisplay alert (item 1 of argv) message (item 2 of argv) as critical\nend run",
			appName, msg).Run()
	}
	os.Exit(1)
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func plistString(path, key string) (string, error) {
	out, err := exec.Command("plutil", "-extract", key, "raw", "-o", "-", path).Output()
	return strings.TrimSpace(string(out)), err
}

// repoRoot: the repository, for running from it (go run ./tools/chromeshell).
func repoRoot() string {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "."
	}
	return strings.TrimSpace(string(out))
}
