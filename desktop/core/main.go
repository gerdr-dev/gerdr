// The desktop App's core: Gerdr's own logic beside Electron (desktop/main.js starts it, el.go is the protocol). Moved
// from tools/chromeshell, the Lightron shell (docs/dev/WHY-ELECTRON-COMEBACK.md): the backend (`gerdr --app`,
// backend.go), its updates (app.go, internal/selfupdate), the window.gerdrNative messages (gerdr.go), the menus
// (menu.go), the projects' pages for the Browser pane (views.go).
//
// Two modes, as the Electron process it runs under:
//
//   - the App: starts / attaches to the backend, everything above;
//
//   - -browser <backend login link>: the projects' browser while the App is closed (internal/projbrowser starts
//     Electron with --gerdr-browser): only views.go, connected to that backend.
//
//     built by `make desktop-core` into desktop/core/gerdr-shell (development) or the bundle's Resources (packaging)
package main

import (
	"flag"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/gerdr-dev/gerdr/desktop/core/el"
	"github.com/gerdr-dev/gerdr/internal/selfupdate"
)

var (
	appName string
	alerts  bool // packaged: errors as an alert too, nobody reads the log
)

func main() {
	// Electron says whether it is the packaged App (GERDR_SHELL_PACKAGED); then this binary is Contents/Resources/gerdr-shell
	exePath, _ := os.Executable()
	packaged := os.Getenv("GERDR_SHELL_PACKAGED") == "1"
	resources := filepath.Dir(exePath)
	gerdrBin := flag.String("gerdr", "", "run this gerdr backend (--app); default: the bundle's (or the installed one)")
	dataDir := flag.String("data", filepath.Join(os.TempDir(), "gerdr-desktop"), "the shell's directory: downloaded backends, update prefs")
	name := flag.String("name", "Gerdr dev shell", "the App's name")
	browser := flag.String("browser", "", "browser-only: the backend's login link to serve the projects' pages to")
	browserMode := false
	flag.Parse()
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "browser" {
			browserMode = true
		}
	})
	appName = *name
	log.SetFlags(log.Ltime | log.Lmicroseconds)
	if packaged {
		// Opened from Finder nothing reads stderr: a log file (the same one the Lightron shell wrote)
		logName := appName
		if browserMode {
			logName += " browser"
		}
		logPath := logPath(logName)
		_ = os.MkdirAll(filepath.Dir(logPath), 0o755)
		if st, err := os.Stat(logPath); err == nil && st.Size() > 5<<20 {
			_ = os.Rename(logPath, logPath+".old")
		}
		if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
			log.SetOutput(f)
		}
		alerts = !browserMode
	}
	log.Printf("---- %s starting (%s, browser-only %v)", appName, exePath, browserMode)
	_ = os.MkdirAll(*dataDir, 0o755)

	sh := &shell{spaces: map[string]string{}, notes: map[string]time.Time{}}

	if browserMode {
		sh.backendEntry = *browser
		err := el.Run(el.Options{
			Name:        appName,
			BrowserOnly: true,
			DataDir:     *dataDir,
			Logf:        log.Printf,
			OnReady: func(a *el.App) {
				sh.setApp(a)
				sh.views = newAppBrowser(sh)
				sh.views.browserOnly = true
				go sh.views.run()
			},
		})
		if err != nil {
			log.Print(err)
		}
		sh.quitting.Store(true)
		return
	}

	// The backend, always as Gerdr.app runs it: `gerdr --app` (cmd/gerdr). It attaches to the gerdr already serving
	// (the background service) and, when that runs another binary than this one, restarts it on this one once nothing
	// is running (appAttach): that is how an update, downloaded or installed with the App, reaches the service.
	// The binary: the newer of the bundle's and a downloaded one (internal/selfupdate); -gerdr picks one, for debugging.
	// The App's version and shell id (tools/shell-id.sh) come from Electron (desktop/main.js reads the bundle's
	// gerdr-app.json, pack.js writes it); the backend beside this binary is the bundle's.
	bundledBin, appVersion, shellID := *gerdrBin, "", ""
	if packaged {
		bundledBin = filepath.Join(resources, backendName)
		appVersion, shellID = os.Getenv("GERDR_APP_VERSION"), os.Getenv("GERDR_SHELL_ID")
	}
	sh.up = selfupdate.New(*dataDir, bundledBin, appVersion, shellID, packaged && *gerdrBin == "")
	switch {
	case *gerdrBin != "":
		sh.bin = *gerdrBin
	case fileExists(bundledBin):
		sh.bin = sh.up.Backend().Path
	default:
		sh.bin = installedBackend()
	}

	err := el.Run(el.Options{
		Name:      appName,
		Version:   appVersion,
		URLFunc:   sh.startBackend,
		Menu:      sh.menu(),
		DockMenu:  sh.dockMenu(),
		OnMessage: sh.message,
		OnWindowClose: func(w *el.Window) {
			if sh.views != nil {
				sh.views.windowClosed(w)
			}
		},
		OnNoteClick: sh.noteClick,
		DataDir:     *dataDir,
		Logf:        log.Printf,
		OnReady: func(a *el.App) {
			sh.setApp(a)
			sh.views = newAppBrowser(sh)
			go sh.views.run()
			go sh.superviseBackend()
			sh.up.Start(sh.pushUpdate)
			go sh.pollBusy()
		},
	})
	sh.stopBackend()
	if err != nil {
		fatal(err.Error())
	}
}

func fatal(msg string) {
	log.Print("fatal: ", msg)
	if alerts {
		alert(appName, msg)
	}
	os.Exit(1)
}

// shellPid: the Electron process this one runs under (an update waits for it to go too).
func shellPid() int {
	n, _ := strconv.Atoi(os.Getenv("GERDR_SHELL_PID"))
	return n
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
