package projbrowser

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/gerdr-dev/gerdr/internal/i18n"
)

// Where a project's profile lives (docs/dev/BROWSER-PANE.md, "One profile, one place").
//
// With the desktop App (macOS): a real directory of the App's browser data directory (Lightron's BrowserDataDir),
// named profileName(project). The App shows the project's pages there (Lightron views), and this package's headless
// browser runs the same directory and profile while the App is closed: --user-data-dir=<App's> --profile-directory=…,
// the App's own browser program. Measured: a profile linked into a data directory from elsewhere never got its cookies
// on disk (even with the network service in process), so it can't live in two data directories; and a data directory
// is one process's at a time (the browser's lock: a second launch exits), so there is one headless browser for all
// the projects then, on one project's profile at a time.
//
// Without the App: <data dir>/projects/<project>/browser, a data directory of the project's own, profile Default.

// appInfoFile (in Gerdr's data dir): the App's browser program and data directory, from its hello, kept for when the
// App is closed.
const appInfoFile = "browser-engine"

// appKey: the one headless browser on the App's data directory.
const appKey = "@app"

type appInfo struct {
	Exe     string   `json:"exe"`
	DataDir string   `json:"dataDir"`
	Kind    string   `json:"kind,omitempty"` // "electron": the projects' browser is that program run with --gerdr-browser (electron.go)
	Args    []string `json:"args,omitempty"`
}

func (m *Manager) saveAppInfo(exe, dataDir, kind string, args []string) {
	if exe == "" || dataDir == "" {
		return
	}
	b, _ := json.Marshal(appInfo{Exe: exe, DataDir: dataDir, Kind: kind, Args: args})
	_ = os.WriteFile(filepath.Join(m.Dir, appInfoFile), b, 0o600)
}

// app: the App's browser, if one ever connected and is still there.
func (m *Manager) app() (appInfo, bool) {
	var a appInfo
	b, err := os.ReadFile(filepath.Join(m.Dir, appInfoFile))
	if err != nil || json.Unmarshal(b, &a) != nil || a.Exe == "" || a.DataDir == "" {
		return appInfo{}, false
	}
	if st, err := os.Stat(a.Exe); err != nil || st.IsDir() {
		return appInfo{}, false
	}
	if st, err := os.Stat(a.DataDir); err != nil || !st.IsDir() {
		return appInfo{}, false
	}
	return a, true
}

type location struct {
	key     string // the headless browser it runs in: appKey, or the project
	dataDir string // --user-data-dir
	profile string // --profile-directory
	exe     string // "" = findBrowser
	shared  bool   // the App's data directory
	// electron: the App is the Electron one: its profiles are partitions (Partitions/<profile>) only Electron opens,
	// never a headless Chrome; a Chrome profile copied in couldn't be read (another cookie key), so none is adopted
	electron bool
}

func (m *Manager) location(project string) location {
	if a, ok := m.app(); ok {
		return location{key: appKey, dataDir: a.DataDir, profile: profileName(project), exe: a.Exe, shared: true, electron: a.Kind == "electron"}
	}
	return location{key: project, dataDir: m.ProfileDir(project), profile: "Default"}
}

// profileName: a project's profile in the App's data directory (Lightron's names: letters, digits, - and _).
func profileName(project string) string {
	sum := sha256.Sum256([]byte(project))
	return "gerdr-" + hex.EncodeToString(sum[:6])
}

// adopt brings a project's profile from its own data directory (before the App had one) into the App's, once, so the
// sites signed in to stay signed in. Only while neither browser has it open.
func (m *Manager) adopt(project string, loc location) {
	if !loc.shared || loc.electron {
		return
	}
	dst := filepath.Join(loc.dataDir, loc.profile)
	if fi, err := os.Lstat(dst); err == nil {
		if fi.Mode()&os.ModeSymlink == 0 {
			return // already its own
		}
		_ = os.Remove(dst) // a link from the first try: its cookies never reached the disk
	}
	src := filepath.Join(m.ProfileDir(project), "Default")
	if st, err := os.Stat(src); err != nil || !st.IsDir() {
		return
	}
	if out, err := exec.Command("cp", "-cR", src, dst).CombinedOutput(); err != nil {
		m.Log.Warn("project browser: copy the profile into the App's", "project", project, "err", err, "out", string(out))
		_ = os.RemoveAll(dst)
		return
	}
	m.Log.Info("project browser: profile moved into the App's data directory", "project", project, "to", dst)
}

// lockHeld: a live browser holds the data directory (its SingletonLock is "<host>-<pid>").
func lockHeld(dataDir string) bool {
	target, err := os.Readlink(filepath.Join(dataDir, "SingletonLock"))
	if err != nil {
		return false
	}
	i := strings.LastIndex(target, "-")
	if i < 0 {
		return false
	}
	pid, err := strconv.Atoi(target[i+1:])
	if err != nil || pid <= 1 {
		return false
	}
	p, err := os.FindProcess(pid)
	return err == nil && p.Signal(syscall.Signal(0)) == nil
}

var errAppHasIt = errors.New(i18n.S("App 的浏览器正开着这些页面的 profile：在 App 里打开", "The App's browser has this profile open: open it in the App"))
