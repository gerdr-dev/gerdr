// Package desktop is the desktop App's core: Gerdr's own logic beside Electron, run as `gerdr shell-core` by
// desktop/main.js (the frozen loader). It ships inside the gerdr binary, so it updates with the backend (~10MB)
// instead of with the whole App: docs/dev/SHELL.md. Moved from tools/chromeshell, the Lightron shell
// (docs/dev/WHY-ELECTRON-COMEBACK.md), then from desktop/core (its own binary in the App, part of the shell id): the
// backend (`gerdr --app`, backend.go), its updates (app.go, internal/selfupdate), the window.gerdrNative messages
// (gerdr.go), the menus (menu.go), the projects' pages for the Browser pane (views.go), and the main-process script
// Electron runs (shell/main.js, sent to the loader first: el.Options.ShellJS).
//
// Two modes, as the Electron process it runs under:
//
//   - the App: starts / attaches to the backend, everything above;
//
//   - -browser <backend login link>: the projects' browser while the App is closed (internal/projbrowser starts
//     Electron with --gerdr-browser): only views.go, connected to that backend.
package desktop

import (
	"flag"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/gerdr-dev/gerdr/internal/desktop/el"
	"github.com/gerdr-dev/gerdr/internal/selfupdate"
)

var (
	appName string
	alerts  bool // packaged: errors as an alert too, nobody reads the log
	// resources: the App bundle's resource directory (Electron's process.resourcesPath, GERDR_SHELL_RESOURCES), where
	// the bundled backend is; not this binary's directory, which after a handoff is a downloaded backend's
	resources string
)

// Main runs the core (args: what follows `gerdr shell-core`). It returns only in browser-only mode or when the App
// quits; errors end the process.
func Main(args []string) {
	fs := flag.NewFlagSet("shell-core", flag.ExitOnError)
	packaged := os.Getenv("GERDR_SHELL_PACKAGED") == "1"
	exePath, _ := os.Executable()
	resources = os.Getenv("GERDR_SHELL_RESOURCES")
	if resources == "" {
		resources = filepath.Dir(exePath)
	}
	gerdrBin := fs.String("gerdr", "", "run this gerdr backend (--app); default: the bundle's (or the installed one)")
	dataDir := fs.String("data", filepath.Join(os.TempDir(), "gerdr-desktop"), "the shell's directory: downloaded backends, update prefs")
	name := fs.String("name", "Gerdr dev shell", "the App's name")
	browser := fs.String("browser", "", "browser-only: the backend's login link to serve the projects' pages to")
	noHandoff := fs.Bool("no-handoff", false, "run this binary's core even when a newer backend is downloaded (the loader's fallback, or after a handoff)")
	hotID := fs.Bool("hot-id", false, "print what this binary's core and shell script are (HotID) and exit")
	browserMode := false
	_ = fs.Parse(args)
	if *hotID {
		os.Stdout.WriteString(HotID() + "\n")
		return
	}
	fs.Visit(func(f *flag.Flag) {
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
	log.Printf("---- %s starting (%s, browser-only %v, hot %s)", appName, exePath, browserMode, HotID())
	_ = os.MkdirAll(*dataDir, 0o755)

	// The App's version and shell id (tools/shell-id.sh) come from Electron (desktop/main.js reads the bundle's
	// gerdr-app.json, pack.js writes it); the bundled backend is in the bundle's resources.
	bundledBin, appVersion, shellID := *gerdrBin, "", ""
	if packaged {
		bundledBin = filepath.Join(resources, backendName)
		appVersion, shellID = os.Getenv("GERDR_APP_VERSION"), os.Getenv("GERDR_SHELL_ID")
	}

	// The loader always starts the bundle's binary. A newer backend downloaded since carries a newer core and shell
	// script too: it runs them instead (docs/dev/SHELL.md). The loader starts this one again with -no-handoff if that
	// one fails before its script runs.
	if packaged && !*noHandoff && *gerdrBin == "" {
		if c := selfupdate.New(*dataDir, bundledBin, appVersion, shellID, true).Backend(); !c.Bundled && !samePath(c.Path, exePath) {
			log.Printf("handing the core over to %s (%s)", c.Path, c.Version)
			handoff(c.Path, append(append([]string{}, args...), "-no-handoff"))
			log.Printf("handoff failed: running this binary's core")
		}
	}
	shellJS := shellScript(*dataDir)

	sh := &shell{spaces: map[string]string{}, notes: map[string]time.Time{}, bwins: browserWins{tabs: map[string]int{}}}

	if browserMode {
		sh.backendEntry = *browser
		err := el.Run(el.Options{
			Name:        appName,
			ShellJS:     shellJS,
			Hot:         HotID(),
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
	sh.up = selfupdate.New(*dataDir, bundledBin, appVersion, shellID, packaged && *gerdrBin == "")
	sh.dataDir = *dataDir
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
		ShellJS:   shellJS,
		Hot:       HotID(),
		Version:   appVersion,
		URLFunc:   sh.startBackend,
		Menu:      sh.menu(),
		DockMenu:  sh.dockMenu(),
		OnMessage: sh.message,
		OnWindowClose: func(w *el.Window) {
			if sh.views != nil {
				sh.views.windowClosed(w)
			}
			sh.browserWindowClosed(w)
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
