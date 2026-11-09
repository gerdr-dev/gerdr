package selfupdate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/andybalholm/brotli"

	"github.com/gerdr-dev/gerdr/internal/i18n"
)

// Package selfupdate keeps a desktop shell's backend up to date (tools/chromeshell on macOS, tools/winshell on
// Windows). Ported from desktop/updater.js (docs/dev/UPDATE.md): the backend-only half. A release publishes the
// brotli-compressed backend alone (~10MB) next to the Electron dmgs; it is downloaded into <data>/backend/<version>/
// and run instead of the bundle's (never written into the bundle: its signature, and App Management). Restarting the
// backend is how it takes effect: our `gerdr --app` attacher, started on the new binary, moves the background service
// over (cmd/gerdr appAttach), and is only restarted when /api/busy says nothing would be cut off, or the person said
// "now".
//
// The other half, the whole package (a new shell): when the release's shell id (tools/shell-id.sh) isn't the one
// this shell was built with, its package for this platform (update.json "app": the dmg / the installer) is downloaded
// into <data>/update/ and waits as a Pending of kind "app"; the shell installs it on a click (InstallApp).

const (
	updateRepo  = "gerdr-dev/gerdr"
	defaultFeed = "https://github.com/" + updateRepo + "/releases/latest/download/update.json"
)

var verRe = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)(?:-(\d+)-g[0-9a-f]+)?(?:-dirty)?$`)

// ParseVer: a release (x.y.z) or a local build (git describe: x.y.z-N-gabc[-dirty]) as [x, y, z, N]. A local build is
// behind the next release, ahead of the one it was built on. Anything else (0.1.0-dev) doesn't update.
func ParseVer(v string) []int {
	m := verRe.FindStringSubmatch(strings.TrimSpace(v))
	if m == nil {
		return nil
	}
	out := make([]int, 4)
	for i := 0; i < 4; i++ {
		out[i], _ = strconv.Atoi(m[i+1])
	}
	return out
}

func Newer(a, b string) bool {
	x, y := ParseVer(a), ParseVer(b)
	if x == nil || y == nil {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return x[i] > y[i]
		}
	}
	return false
}

// PlatKey: the key in update.json, Node's naming (desktop/updater.js process.platform-process.arch): darwin-arm64,
// darwin-x64, win32-x64.
func PlatKey() string {
	arch, osName := runtime.GOARCH, runtime.GOOS
	if arch == "amd64" {
		arch = "x64"
	}
	if osName == "windows" {
		osName = "win32"
	}
	return osName + "-" + arch
}

// exeName: the backend binary's name in a download directory.
func exeName() string {
	if runtime.GOOS == "windows" {
		return "gerdr.exe"
	}
	return "gerdr"
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// Enabled: whether this build updates itself (a packaged release or local build, not 0.1.0-dev, no GERDR_BIN /
// GERDR_NO_UPDATE).
func (u *Updater) Enabled() bool { return u.enabled }

type Pending struct {
	Kind    string `json:"kind"` // backend | app
	Version string `json:"version"`
	File    string `json:"-"` // kind app: the downloaded package
}

// asset: a file of the release, as update.json lists it.
type asset struct {
	File   string `json:"file"`
	Size   int64  `json:"size"`
	Sha256 string `json:"sha256"`
}

type Updater struct {
	mu       sync.Mutex
	data     string // <data>: backend/, update-prefs.json
	bundled  string // the bundle's backend
	app      string // the shell's version (its Info.plist)
	shell    string // the shell's id (tools/shell-id.sh, put in by package.sh); "" = backend updates only
	enabled  bool
	feed     string
	onChange func()

	phase       string // idle | checking | downloading
	progress    int
	downloading string
	errMsg      string
	checkedAt   int64
	pending     *Pending

	seenKey, seenVersion string // the bundle's backend version, by its file's mtime + size
}

// New: data is where downloads and the automatic-updates switch go; bundled the backend shipped with the shell;
// appVersion and shellID the shell's version and id; packaged whether this is an installed App (else nothing updates).
func New(data, bundled, appVersion, shellID string, packaged bool) *Updater {
	u := &Updater{data: data, bundled: bundled, app: appVersion, shell: shellID, phase: "idle", feed: defaultFeed, onChange: func() {}}
	if f := os.Getenv("GERDR_UPDATE_FEED"); f != "" {
		u.feed = f
	}
	u.enabled = packaged && ParseVer(appVersion) != nil && os.Getenv("GERDR_BIN") == "" && os.Getenv("GERDR_NO_UPDATE") == ""
	return u
}

func (u *Updater) root() string { return filepath.Join(u.data, "backend") }

// bundledVersion asks the bundle's binary (`gerdr version`), again only when the file changed: a bundle swapped under
// the running shell (make install, an App installed by hand) has to win over an older download.
func (u *Updater) bundledVersion() string {
	st, err := os.Stat(u.bundled)
	if err != nil {
		return u.seenVersion
	}
	key := fmt.Sprintf("%d:%d", st.ModTime().UnixNano(), st.Size())
	if key != u.seenKey {
		cmd := exec.Command(u.bundled, "version")
		hideConsole(cmd)
		out, err := cmd.Output()
		v := strings.TrimPrefix(strings.TrimSpace(string(out)), "v")
		if err == nil && ParseVer(v) != nil {
			u.seenKey, u.seenVersion = key, v
		}
	}
	return u.seenVersion
}

type Choice struct {
	Path, Version string
	Bundled       bool
}

// backend: the binary to run: a downloaded one if Newer than the bundle's, else the bundle's.
func (u *Updater) Backend() Choice {
	b := Choice{Path: u.bundled, Version: u.bundledVersion(), Bundled: true}
	if !u.enabled {
		return b
	}
	var cur struct{ Version string }
	if raw, err := os.ReadFile(filepath.Join(u.root(), "current.json")); err != nil || json.Unmarshal(raw, &cur) != nil {
		return b
	}
	if !Newer(cur.Version, b.Version) {
		return b
	}
	p := filepath.Join(u.root(), cur.Version, exeName())
	if fileExists(p) {
		return Choice{Path: p, Version: cur.Version}
	}
	return b
}

// cleanup: drop downloaded backends other than the one in use. (Windows can't delete a running exe: that one stays
// until the next start.)
func (u *Updater) cleanup() {
	keep := u.Backend().Version
	names, _ := os.ReadDir(u.root())
	for _, n := range names {
		if ParseVer(n.Name()) != nil && n.Name() != keep {
			_ = os.RemoveAll(filepath.Join(u.root(), n.Name()))
		}
	}
}

func (u *Updater) emit() { go u.onChange() }

// check looks for a release and downloads its backend. Returns a line for the "Check for Updates" menu item.
func (u *Updater) Check() string {
	if !u.enabled {
		return i18n.S("这个版本不自动更新（本地构建）。", "This build doesn't update itself (local build).")
	}
	u.mu.Lock()
	if u.phase != "idle" {
		u.mu.Unlock()
		return i18n.S("正在检查。", "Already checking.")
	}
	u.phase, u.errMsg = "checking", ""
	u.mu.Unlock()
	u.emit()
	defer func() {
		u.mu.Lock()
		u.phase, u.progress, u.downloading = "idle", 0, ""
		u.mu.Unlock()
		u.emit()
	}()

	var feed struct {
		Version string           `json:"version"`
		Shell   string           `json:"shell"`
		Backend map[string]asset `json:"backend"`
		App     map[string]asset `json:"app"`
	}
	if err := getJSON(u.feed, &feed); err != nil {
		return u.failed(err)
	}
	u.mu.Lock()
	u.checkedAt = time.Now().UnixMilli()
	have := u.Backend().Version
	waiting := u.pending != nil && !Newer(feed.Version, u.pending.Version)
	pv := ""
	if u.pending != nil {
		pv = u.pending.Version
	}
	u.mu.Unlock()
	if waiting { // downloaded before (maybe by the automatic check a moment ago), not applied yet: not "up to date"
		return fmt.Sprintf(i18n.S("%s 已下载好，等待更新。", "%s is downloaded, waiting to be applied."), pv)
	}
	if !Newer(feed.Version, have) {
		return fmt.Sprintf(i18n.S("已经是最新版（%s）。", "Up to date (%s)."), have)
	}
	b, hasBackend := feed.Backend[PlatKey()]
	a, hasApp := feed.App[PlatKey()]
	sameShell := u.shell == "" || feed.Shell == u.shell
	if !(sameShell && hasBackend) && !hasApp {
		return fmt.Sprintf(i18n.S("%s 没有 %s 的安装包。", "%s has no package for %s."), feed.Version, PlatKey())
	}
	u.mu.Lock()
	u.phase, u.progress, u.downloading = "downloading", 0, feed.Version
	u.mu.Unlock()
	u.emit()
	var p *Pending
	if sameShell && hasBackend {
		if err := u.fetchBackend(feed.Version, b); err != nil {
			return u.failed(err)
		}
		p = &Pending{Kind: "backend", Version: feed.Version}
	} else {
		file, err := u.fetchApp(feed.Version, a)
		if err != nil {
			return u.failed(err)
		}
		p = &Pending{Kind: "app", Version: feed.Version, File: file}
	}
	u.mu.Lock()
	u.pending = p
	u.mu.Unlock()
	u.emit()
	return fmt.Sprintf(i18n.S("%s 已下载好。", "Downloaded %s."), feed.Version)
}

func (u *Updater) failed(err error) string {
	u.mu.Lock()
	u.errMsg = err.Error()
	u.mu.Unlock()
	log.Printf("update: %v", err)
	return i18n.S("检查更新失败：", "Update check failed: ") + err.Error()
}

func getJSON(url string, v any) error {
	c := http.Client{Timeout: 30 * time.Second}
	resp, err := c.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("%s: %s", url, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

func (u *Updater) releaseURL(version, file string) string {
	if os.Getenv("GERDR_UPDATE_FEED") != "" {
		return u.feed[:strings.LastIndex(u.feed, "/")+1] + file
	}
	return fmt.Sprintf("https://github.com/%s/releases/download/v%s/%s", updateRepo, version, file)
}

// download streams url into dest, checking size and sha256 (the feed and the file come from the same release: this
// catches truncation, not forgery). Progress goes to Settings.
func (u *Updater) download(url, dest string, a asset) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("%s: %s", a.File, resp.Status)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	h := sha256.New()
	var got int64
	buf := make([]byte, 256<<10)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			_, _ = f.Write(buf[:n])
			h.Write(buf[:n])
			got += int64(n)
			if a.Size > 0 {
				if pct := int(got * 100 / a.Size); pct != u.progress {
					u.mu.Lock()
					u.progress = pct
					u.mu.Unlock()
					if pct%5 == 0 {
						u.emit()
					}
				}
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			f.Close()
			os.Remove(dest)
			return rerr
		}
	}
	f.Close()
	if (a.Size > 0 && got != a.Size) || (a.Sha256 != "" && hex.EncodeToString(h.Sum(nil)) != a.Sha256) {
		os.Remove(dest)
		return fmt.Errorf("%s: download corrupted (%d bytes)", a.File, got)
	}
	return nil
}

// fetchApp downloads the release's whole package for this platform into <data>/update/ (kept there until installed:
// a second check doesn't download it again).
func (u *Updater) fetchApp(version string, a asset) (string, error) {
	stage := filepath.Join(u.data, "update")
	file := filepath.Join(stage, filepath.Base(a.File))
	if st, err := os.Stat(file); err == nil && st.Size() == a.Size {
		return file, nil
	}
	_ = os.RemoveAll(stage)
	if err := u.download(u.releaseURL(version, a.File), file+".part", a); err != nil {
		return "", err
	}
	return file, os.Rename(file+".part", file)
}

// fetchBackend downloads and unpacks the backend, and only then points current.json at it: a half-written directory
// is never picked.
func (u *Updater) fetchBackend(version string, a asset) error {
	_ = os.MkdirAll(u.root(), 0o755)
	packed := filepath.Join(u.root(), "."+version+".br")
	defer os.Remove(packed)
	if err := u.download(u.releaseURL(version, a.File), packed, a); err != nil {
		return err
	}
	dir := filepath.Join(u.root(), version)
	_ = os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	in, err := os.Open(packed)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := filepath.Join(dir, exeName()+".tmp")
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, brotli.NewReader(in)); err != nil {
		out.Close()
		return fmt.Errorf("unpack %s: %w", a.File, err)
	}
	out.Close()
	if err := os.Rename(tmp, filepath.Join(dir, exeName())); err != nil {
		return err
	}
	cur := filepath.Join(u.root(), "current.json")
	b, _ := json.Marshal(map[string]string{"version": version})
	if err := os.WriteFile(cur+".tmp", b, 0o644); err != nil {
		return err
	}
	return os.Rename(cur+".tmp", cur)
}

// noteRunning: the version the running gerdr reports. Behind what we'd run means a restart is due, also when nothing
// was downloaded (the background service still on the version before an App installed by hand).
func (u *Updater) NoteRunning(running string) {
	want := u.Backend().Version
	u.mu.Lock()
	if u.pending != nil && u.pending.Kind == "app" { // a whole new App waits for a click; the backend can't settle it
		u.mu.Unlock()
		return
	}
	changed := false
	if Newer(want, running) {
		if u.pending == nil || u.pending.Version != want {
			u.pending, changed = &Pending{Kind: "backend", Version: want}, true
		}
	} else if u.pending != nil {
		u.pending, changed = nil, true
	}
	u.mu.Unlock()
	if changed {
		u.emit()
	}
}

// auto: Settings → 版本's automatic updates (on by default), kept by the shell (it decides before any page is up).
func (u *Updater) Auto() bool {
	var p struct {
		Auto *bool `json:"auto"`
	}
	if b, err := os.ReadFile(filepath.Join(u.data, "update-prefs.json")); err == nil && json.Unmarshal(b, &p) == nil && p.Auto != nil {
		return *p.Auto
	}
	return true
}

func (u *Updater) SetAuto(on bool) {
	b, _ := json.Marshal(map[string]bool{"auto": on})
	_ = os.WriteFile(filepath.Join(u.data, "update-prefs.json"), b, 0o644)
	if on {
		go func() { log.Printf("update: %s", u.Check()) }() // turned back on: don't wait hours for the next round
	}
	u.emit()
}

// start: the first check 20s after launch, then every 4 hours, while automatic updates are on.
func (u *Updater) Start(changed func()) {
	u.onChange = changed
	u.cleanup()
	if !u.enabled {
		return
	}
	go func() {
		time.Sleep(20 * time.Second)
		for {
			if u.Auto() {
				log.Printf("update: %s", u.Check())
			}
			time.Sleep(4 * time.Hour)
		}
	}()
}

func (u *Updater) Pending() *Pending {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.pending
}

// Busy: GET /api/busy of the running gerdr.
type Busy struct {
	Version string `json:"version"`
	Turns   int    `json:"turns"`
	Waiting int    `json:"waiting"`
	Tasks   int    `json:"tasks"`
	Terms   int    `json:"terms"`
}

func (b *Busy) Idle() bool { return b != nil && b.Turns+b.Waiting+b.Tasks+b.Terms == 0 }

// status: what Settings → 版本 shows (ui/src/lib/native.ts AppUpdateStatus; desktop/main.js updateStatus).
func (u *Updater) Status(busy *Busy, applying bool) map[string]any {
	u.mu.Lock()
	defer u.mu.Unlock()
	phase := u.phase
	if applying {
		phase = "applying"
	}
	st := map[string]any{
		"enabled": u.enabled, "auto": u.Auto(), "app": u.app, "backend": u.Backend().Version,
		"running": "", "busy": nil, "pending": nil, "phase": phase, "progress": u.progress,
		"downloading": u.downloading, "error": u.errMsg, "checkedAt": u.checkedAt,
	}
	if busy != nil {
		st["running"], st["busy"] = busy.Version, busy
	}
	if u.pending != nil {
		st["pending"] = u.pending
	}
	return st
}
