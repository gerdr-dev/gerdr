// Prototype: a desktop shell for Gerdr that drives the user's own Chrome instead of bundling Chromium (Electron).
//
// It connects to the running Gerdr (or starts the backend like desktop/main.js), launches an installed Chrome-family
// browser with a profile of its own (on macOS a copy of it that is Gerdr to the system: name, icon, Dock, notifications;
// engine_darwin.go), opens the UI in --app windows, talks CDP over a pipe and injects the window.gerdrNative bridge the
// UI already knows. Findings and what's left: README.md.
//
//	go run ./tools/chromeshell -gerdr bin/gerdr              # with GERDR_DIR / GERDR_PORT for a debug instance
//	go run ./tools/chromeshell -test                         # a test page instead of Gerdr
//	go run ./tools/chromeshell -gerdr bin/gerdr -selftest    # click through it over CDP, print, quit
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	_ "embed"

	"github.com/gerdr-dev/gerdr/internal/i18n"
	"github.com/gerdr-dev/gerdr/internal/selfupdate"
)

//go:embed bridge.js
var bridgeJS string

func main() {
	// Opened as an App (package.sh): no flags, so the defaults are the App's, named after it (its Info.plist: NAME and
	// ID in package.sh).
	exePath, _ := os.Executable()
	packaged := strings.Contains(exePath, ".app/Contents/MacOS/")
	home, _ := os.UserHomeDir()
	defProfile, defName, bundleID := filepath.Join(os.TempDir(), "gerdr-chromeshell"), "Gerdr dev shell", ""
	if packaged {
		plist := filepath.Join(filepath.Dir(exePath), "../Info.plist")
		defName, _ = plistString(plist, "CFBundleName")
		bundleID, _ = plistString(plist, "CFBundleIdentifier")
		if defName == "" {
			defName = "Gerdr"
		}
		// "<name> Shell", not "<name>": ~/Library/Application Support/Gerdr is the Electron App's (window.json, its
		// downloaded backends), left as it was.
		defProfile = filepath.Join(home, "Library/Application Support", defName+" Shell", "Chrome")
	}
	gerdrBin := flag.String("gerdr", "", "run this gerdr backend (--app); default: connect to the gerdr already running, else start the installed one")
	test := flag.Bool("test", false, "open the built-in test page instead of Gerdr")
	profile := flag.String("profile", defProfile, "Chrome user-data-dir (also holds the shell's lock)")
	browser := flag.String("browser", "", "browser executable (default: first Chrome-family browser found)")
	engine := flag.Bool("engine", true, "macOS: run a Dock-less clone of the browser (engine_darwin.go)")
	selftest := flag.Bool("selftest", false, "exercise the bridge over CDP, print the results, quit")
	name := flag.String("name", defName, "the app's name (Dock, ⌘Tab, notifications): the manifest the shell serves")
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
	start := time.Now()
	since := func() time.Duration { return time.Since(start).Round(time.Millisecond) }
	log.Printf("---- %s starting (%s)", appName, exePath)

	_ = os.MkdirAll(*profile, 0o755)
	// One shell per profile (Chrome refuses a second browser on it anyway): a second launch asks the first for a window.
	lock, ok := lockOrForward(*profile)
	if !ok {
		log.Print("already running: asked it for a window")
		return
	}
	defer lock.Close()

	exe := *browser
	if exe == "" {
		exe = findBrowser()
	}
	if exe == "" {
		// The engine is the user's own browser (no Chromium shipped). None: say so and offer Chrome's download page.
		noBrowser()
	}
	b := brand{name: appName, id: "io.github.zbysir.gerdr.devshell.engine"}
	if packaged {
		b.id = bundleID + ".engine"
		b.icns = filepath.Join(filepath.Dir(exePath), "../Resources/icon.icns")
	} else if runtime.GOOS == "darwin" {
		b.icns = filepath.Join(filepath.Dir(*profile), "gerdr-chromeshell-icon.icns")
		if _, err := os.Stat(b.icns); err != nil {
			if err := makeIcns(filepath.Join(repoRoot(), "desktop/res/icon.png"), b.icns); err != nil {
				log.Printf("icon: %v", err)
				b.icns = ""
			}
		}
	}
	if *engine {
		if e, err := prepareEngine(exe, filepath.Join(filepath.Dir(*profile), filepath.Base(*profile)+"-engine"), b); err != nil {
			log.Printf("engine: %v; running the browser as installed (it shows as Chrome in the Dock)", err)
		} else {
			exe = e
		}
	}
	log.Printf("browser: %s (%v)", exe, since())

	// The backend, always as Gerdr.app runs it: `gerdr --app` (cmd/gerdr). It attaches to the gerdr already serving
	// (the background service) and, when that runs another binary than this one, restarts it on this one once nothing
	// is running (appAttach): that is how an update, downloaded or installed with the App, reaches the service.
	// The binary: the newer of the bundle's and a downloaded one (updater.go); -gerdr picks one, for debugging.
	dataDir := filepath.Dir(*profile)
	bundledBin, appVersion, shellID := *gerdrBin, "", ""
	if packaged {
		bundledBin = filepath.Join(filepath.Dir(exePath), "../Resources/gerdr")
		appVersion, _ = plistString(filepath.Join(filepath.Dir(exePath), "../Info.plist"), "CFBundleShortVersionString")
		shellID, _ = plistString(filepath.Join(filepath.Dir(exePath), "../Info.plist"), "GerdrShell")
	}
	up := selfupdate.New(dataDir, bundledBin, appVersion, shellID, packaged && *gerdrBin == "")
	var be *backend
	var entry string
	bin := ""
	if !*test {
		switch {
		case *gerdrBin != "":
			bin = *gerdrBin
		case fileExists(bundledBin):
			bin = up.Backend().Path
		default:
			bin = installedBackend()
		}
		log.Printf("starting backend %s", bin)
		var err error
		if be, err = startBackend(bin, nil); err != nil {
			fatal(err.Error())
		}
	}

	// Gerdr's menu bar instead of Chrome's (menu.go): a library loaded into the engine. Only into our re-signed copy:
	// the browser as installed has library validation and won't load it.
	var mn *menu
	var inject []string
	if *engine && runtime.GOOS == "darwin" && strings.Contains(exe, "-engine/") {
		if lib, err := menuLibrary(packaged, exePath, *profile); err != nil {
			log.Printf("menu: %v; keeping Chrome's", err)
		} else {
			mn = &menu{}
			sock := filepath.Join(*profile, "menu.sock")
			if err := mn.serve(sock); err != nil {
				log.Printf("menu: %v; keeping Chrome's", err)
				mn = nil
			} else {
				inject = []string{"DYLD_INSERT_LIBRARIES=" + lib, "GERDR_MENU_SOCK=" + sock}
				if os.Getenv("CHROMESHELL_NAV") != "" {
					inject = append(inject, "GERDR_MENU_LOG="+filepath.Join(filepath.Dir(*profile), "gerdrmenu.log"))
				}
			}
		}
	}

	chrome, proc, c, sh, err := launchBrowser(exe, *profile, inject)
	if err != nil {
		fatal(err.Error())
	}
	log.Printf("browser up (%v)", since())

	if *test {
		entry = serveTestPage()
	} else if be != nil {
		if entry, err = be.waitReady(30 * time.Second); err != nil {
			fatal(err.Error())
		}
	}
	log.Printf("page: %s (%v)", originOf(entry), since())
	sh.setEntry(entry)

	if _, err := c.call("", "Browser.grantPermissions", map[string]any{"origin": sh.origin, "permissions": []string{"notifications"}}); err != nil {
		log.Printf("grant notifications: %v", err)
	}
	// An earlier Gerdr exp installed the page as a web app (its windows had Chrome's "⋮" and translate buttons in the
	// title bar, which no setting removes): uninstall it, Chrome removes its app shim from ~/Applications with it.
	if _, err := c.call("", "PWA.uninstall", map[string]any{"manifestId": sh.origin + "/"}); err == nil {
		log.Print("removed the web app an earlier version installed")
	}
	sh.engine, sh.profile, sh.engineID = exe, *profile, b.id
	if mn != nil {
		mn.sh = sh
		sh.menu = mn
	}
	if !*engine {
		sh.engineID = ""
	}
	sh.launch("")
	log.Printf("window launched (%v)", since())
	if be != nil {
		sh.up, sh.bin = up, bin
		sh.setBackend(be)
		go sh.superviseBackend()
		up.Start(sh.pushUpdate)
		go sh.pollBusy()
	}
	go serveForwarded(*profile, sh)

	if *selftest {
		dryOpen = true
		go func() {
			time.Sleep(4 * time.Second)
			if *test {
				sh.selftestPage()
				_, _ = c.call("", "Browser.close", nil)
			} else {
				sh.selftestGerdr(*profile) // ends by closing the window: the shell should quit by itself
			}
		}()
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	select {
	case <-sig:
		log.Print("signal: quitting")
	case <-sh.quitForUpdate:
		log.Print("quitting to install an update")
	case <-sh.allClosed:
		log.Print("last window closed: quitting")
	case <-chrome:
		log.Print("browser exited")
	}
	sh.stopBackend()
	_, _ = c.call("", "Browser.close", nil)
	select {
	case <-chrome:
	case <-time.After(5 * time.Second):
		// Browser.close didn't take (the connection already broken): a browser left running with nobody to drive it
		// would be an orphan Gerdr in the Dock.
		log.Print("browser didn't close: killing it")
		_ = proc.Kill()
	}
}

// launchBrowser starts the browser on our profile with a CDP pipe, no window yet (launch() opens --app ones once we
// know the page's address), and the shell attached to every page before it runs.
func launchBrowser(exe, profile string, env []string) (<-chan struct{}, *os.Process, *cdp, *shell, error) {
	args := []string{
		"--user-data-dir=" + profile,
		"--remote-debugging-pipe",
		"--no-startup-window",
		"--no-first-run",
		"--no-default-browser-check",
		// The engine clone is re-signed: the user's "Chrome Safe Storage" keychain item would prompt for it.
		"--use-mock-keychain",
		// A new profile says en-US whatever the system's language; the UI follows navigator.languages, and Electron
		// gave it the system's (app.getLocale). Same detection the backend uses for its own texts.
		"--accept-lang=" + acceptLang(),
	}
	setPrefs(profile)
	cmd := exec.Command(exe, args...)
	cmd.Env = append(os.Environ(), env...)
	r3, w3, _ := os.Pipe() // Chrome reads commands from fd 3
	r4, w4, _ := os.Pipe() // and writes replies to fd 4
	cmd.ExtraFiles = []*os.File{r3, w4}
	if err := cmd.Start(); err != nil {
		return nil, nil, nil, nil, err
	}
	r3.Close()
	w4.Close()
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()

	sh := newShell("")
	c := pipeConn(w3, r4, sh.event)
	sh.c = c
	if _, err := c.call("", "Target.setAutoAttach", map[string]any{"autoAttach": true, "waitForDebuggerOnStart": true, "flatten": true}); err != nil {
		return nil, nil, nil, nil, err
	}
	return exited, cmd.Process, c, sh, nil
}

var (
	appName string
	alerts  bool // opened as an App: errors as an alert, nobody reads the log
)

func fatal(msg string) {
	log.Print("fatal: ", msg)
	if alerts {
		// argv, never spliced into the script
		_ = exec.Command("osascript", "-e", "on run argv\ndisplay alert (item 1 of argv) message (item 2 of argv) as critical\nend run",
			appName, msg).Run()
	}
	os.Exit(1)
}

// setPrefs: profile preferences that have no command-line switch, written while Chrome isn't running (we hold the
// profile's lock) into Default/Preferences, which Chrome keeps across runs.
//   - translate.enabled: Chrome guesses the page's language (code-heavy Chinese pages included) and puts its translate
//     button in the app's title bar. --disable-features=Translate doesn't remove it; this does.
func setPrefs(profile string) {
	p := filepath.Join(profile, "Default", "Preferences")
	prefs := map[string]any{}
	if b, err := os.ReadFile(p); err == nil {
		if json.Unmarshal(b, &prefs) != nil {
			return // unreadable: leave Chrome's file alone
		}
	}
	tr, _ := prefs["translate"].(map[string]any)
	if tr == nil {
		tr = map[string]any{}
	}
	if tr["enabled"] == false {
		return
	}
	tr["enabled"] = false
	prefs["translate"] = tr
	b, err := json.Marshal(prefs)
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(p), 0o700)
	_ = os.WriteFile(p, b, 0o600)
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// noBrowser: no Chrome-family browser on this computer. The prompt offers Chrome's download page; then quit.
func noBrowser() {
	msg := i18n.S(appName+" 需要电脑上装有 Chrome（或 Edge、Brave）：它用这个浏览器的内核来显示界面，自己不带一份。\n\n装好 Chrome 后重新打开 "+appName+"。",
		appName+" needs Chrome (or Edge, Brave) on this computer: it shows its interface with that browser's engine instead of carrying one of its own.\n\nInstall Chrome, then open "+appName+" again.")
	log.Print("fatal: no Chrome-family browser")
	get, quit := i18n.S("下载 Chrome", "Download Chrome"), i18n.S("退出", "Quit")
	script := `on run argv
display alert (item 1 of argv) message (item 2 of argv) buttons {(item 4 of argv), (item 3 of argv)} default button 2
return button returned of result
end run`
	out, _ := exec.Command("osascript", "-e", script, appName, msg, get, quit).Output()
	if strings.TrimSpace(string(out)) == get {
		_ = exec.Command("open", "https://www.google.com/chrome/").Run()
	}
	os.Exit(1)
}

func acceptLang() string {
	if i18n.Current() == i18n.ZH {
		return "zh-CN,zh,en"
	}
	return "en-US,en"
}

func (s *shell) setEntry(entry string) {
	n := newShell(entry)
	s.entry, s.origin = n.entry, n.origin
}

func findBrowser() string {
	var cands []string
	switch runtime.GOOS {
	case "darwin":
		for _, app := range []string{
			"Google Chrome.app/Contents/MacOS/Google Chrome",
			"Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			"Brave Browser.app/Contents/MacOS/Brave Browser",
			"Chromium.app/Contents/MacOS/Chromium",
		} {
			cands = append(cands, "/Applications/"+app, filepath.Join(os.Getenv("HOME"), "Applications", app))
		}
	default:
		for _, n := range []string{"google-chrome", "google-chrome-stable", "chromium", "microsoft-edge"} {
			if p, err := exec.LookPath(n); err == nil {
				cands = append(cands, p)
			}
		}
	}
	for _, p := range cands {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

// lockOrForward: the first shell holds a lock in the profile and listens on a socket next to it; a second one finds
// the lock taken, says "focus" on the socket (desktop/main.js: second-instance → focusWindow) and exits.
func lockOrForward(profile string) (*os.File, bool) {
	f, err := os.OpenFile(filepath.Join(profile, "shell.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		log.Fatal(err)
	}
	if syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil {
		return f, true
	}
	f.Close()
	if conn, err := net.Dial("unix", filepath.Join(profile, "shell.sock")); err == nil {
		_, _ = conn.Write([]byte("focus\n"))
		conn.Close()
	}
	return nil, false
}

func serveForwarded(profile string, sh *shell) {
	sock := filepath.Join(profile, "shell.sock")
	_ = os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		log.Printf("second-instance socket: %v", err)
		return
	}
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		line, _ := bufio.NewReader(conn).ReadString('\n')
		conn.Close()
		if line == "focus\n" {
			sh.focusAny()
		}
	}
}
