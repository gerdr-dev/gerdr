// Package doctor is Settings → Labs → Diagnosis: checks that the environment gerdr hands its agents is the
// one the user has in their terminal, and that the claude / codex it runs actually start.
//
// Why it exists: one failed read of the login shell's PATH at service start left every agent without node /
// npx / go / ffmpeg, made gerdr fall back to a stale /usr/local/bin/codex that macOS kills as malware, and
// broke the usage meter — four symptoms that looked unrelated and took several rounds to trace to one cause.
// Each check here is one of the steps that trace took.
//
// Two depths. The light run (background, every few minutes) only compares PATHs and looks files up; it never
// executes anything, because starting a binary macOS has blocklisted pops the "malware blocked" dialog again.
// The deep run (the user clicked "run diagnosis") also runs `--version` on every install it finds.
package doctor

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gerdr-dev/gerdr/internal/hub"
	"github.com/gerdr-dev/gerdr/sysenv"
)

type Level string

const (
	OK   Level = "ok"
	Info Level = "info" // worth knowing, nothing to fix (a tool that isn't installed)
	Warn Level = "warn"
	Bad  Level = "bad"
)

func (l Level) rank() int {
	switch l {
	case Warn:
		return 1
	case Bad:
		return 2
	}
	return 0
}

// Fix is a repair the UI can offer as a button. reloadPath runs here (POST /api/doctor/fix);
// useBin is a config change (agentBins) the UI makes itself, so it also lands on other devices' screens.
type Fix struct {
	Kind  string `json:"kind"` // "reloadPath" | "useBin"
	Label string `json:"label"`
	Agent string `json:"agent,omitempty"`
	Path  string `json:"path,omitempty"`
}

type Install struct {
	Path    string `json:"path"`
	Version string `json:"version,omitempty"`
	Error   string `json:"error,omitempty"`
	Blocked bool   `json:"blocked,omitempty"` // SIGKILLed on start: on macOS that's Gatekeeper / XProtect
	Current bool   `json:"current,omitempty"` // the one gerdr runs
}

type Check struct {
	ID       string    `json:"id"`
	Group    string    `json:"group"` // path | agent | tool | usage
	Title    string    `json:"title"`
	Level    Level     `json:"level"`
	Summary  string    `json:"summary"`
	Detail   string    `json:"detail,omitempty"`
	Installs []Install `json:"installs,omitempty"`
	Fixes    []Fix     `json:"fixes,omitempty"`
}

type Report struct {
	At     int64   `json:"at"` // unix ms
	Deep   bool    `json:"deep"`
	Level  Level   `json:"level"` // the worst check
	Checks []Check `json:"checks"`
}

type Doctor struct {
	usage    func() []hub.UsageInfo
	onReport func(Report)

	mu   sync.Mutex
	last *Report
	run  sync.Mutex // one run at a time
}

func New(usage func() []hub.UsageInfo, onReport func(Report)) *Doctor {
	return &Doctor{usage: usage, onReport: onReport}
}

func (d *Doctor) Last() *Report {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.last
}

// Watch runs the light check shortly after start and then every interval, until ctx ends.
// It leaves a recent deep report alone: that one has more in it (versions, blocked installs).
func (d *Doctor) Watch(ctx context.Context, interval time.Duration) {
	t := time.NewTimer(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if l := d.Last(); l == nil || !l.Deep || time.Since(time.UnixMilli(l.At)) > 30*time.Minute {
			d.Run(ctx, false)
		}
		t.Reset(interval)
	}
}

// Fix applies a server-side fix and returns a fresh report.
func (d *Doctor) Fix(ctx context.Context, f Fix) (Report, error) {
	switch f.Kind {
	case "reloadPath":
		if err := sysenv.ReloadLoginPath(); err != nil {
			return Report{}, err
		}
	default:
		return Report{}, errors.New("unknown fix: " + f.Kind)
	}
	return d.Run(ctx, d.Last() != nil && d.Last().Deep), nil
}

func (d *Doctor) Run(ctx context.Context, deep bool) Report {
	d.run.Lock()
	defer d.run.Unlock()
	sysenv.AdoptLoginPath()
	cur := os.Getenv("PATH")
	login, loginErr := sysenv.LoginShellPath()

	var checks []Check
	checks = append(checks, pathCheck(cur, login, loginErr))
	dirs := append(filepath.SplitList(login), filepath.SplitList(cur)...)
	checks = append(checks, agentCheck(ctx, "claude", "Claude Code", sysenv.FindClaude, dirs, deep))
	checks = append(checks, agentCheck(ctx, "codex", "Codex", sysenv.FindCodex, dirs, deep))
	for _, t := range tools {
		checks = append(checks, toolCheck(t, cur, login, loginErr == nil))
	}
	if d.usage != nil {
		checks = append(checks, usageChecks(d.usage())...)
	}

	r := Report{At: time.Now().UnixMilli(), Deep: deep, Level: OK, Checks: checks}
	for _, c := range checks {
		if c.Level.rank() > r.Level.rank() {
			r.Level = c.Level
		}
	}
	d.mu.Lock()
	d.last = &r
	d.mu.Unlock()
	if d.onReport != nil {
		d.onReport(r)
	}
	return r
}

var reloadFix = Fix{Kind: "reloadPath", Label: "重新读取 PATH"}

// pathCheck: does this process's PATH have everything the login shell's has? A dir only counts as missing
// when it brings commands the current PATH can't find otherwise — version-fox and friends put a fresh
// per-shell temp dir on PATH every time, which would otherwise always look missing.
func pathCheck(cur, login string, loginErr error) Check {
	c := Check{ID: "path", Group: "path", Title: "PATH", Level: OK}
	st := sysenv.LoginPathStatus()
	var startNote string
	if st.Error != "" {
		startNote = "上一次读 login shell 的 PATH 失败（" + st.At.Format("01-02 15:04:05") + "）：" + st.Error
	}
	if loginErr != nil {
		c.Level = Warn
		c.Summary = "读不到 login shell 的 PATH，没法比对"
		c.Detail = loginErr.Error()
		if startNote != "" {
			c.Detail += "\n" + startNote
		}
		c.Fixes = []Fix{reloadFix}
		return c
	}
	var lost []string // "dir（cmd, cmd…）"
	n := 0
	for _, d := range filepath.SplitList(login) {
		if d == "" || inList(d, cur) {
			continue
		}
		cmds := lostCommands(d, cur)
		if len(cmds) == 0 {
			continue
		}
		n += len(cmds)
		shown := cmds
		if len(shown) > 6 {
			shown = append(shown[:6:6], "…")
		}
		lost = append(lost, d+"（"+strings.Join(shown, ", ")+"）")
	}
	if len(lost) == 0 {
		c.Summary = "和终端里一致"
		if startNote != "" {
			c.Detail = startNote + "\n后来重试成功了"
		}
		return c
	}
	c.Level = Bad
	c.Summary = strconv.Itoa(len(lost)) + " 个目录没进来，agent 里找不到 " + strconv.Itoa(n) + " 个命令"
	c.Detail = "终端里有、Gerdr 里没有的目录：\n" + strings.Join(lost, "\n")
	if startNote != "" {
		c.Detail = startNote + "\n\n" + c.Detail
	}
	c.Fixes = []Fix{reloadFix}
	return c
}

func inList(dir, list string) bool {
	for _, d := range filepath.SplitList(list) {
		if d == dir {
			return true
		}
	}
	return false
}

// lostCommands: executables in dir that nothing on path provides.
func lostCommands(dir, path string) []string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		name := e.Name()
		if strings.HasPrefix(name, ".") || !isExec(filepath.Join(dir, name)) {
			continue
		}
		if lookIn(name, path) == "" {
			out = append(out, name)
		}
	}
	return out
}

func lookIn(name, path string) string {
	for _, d := range filepath.SplitList(path) {
		if d == "" {
			continue
		}
		if p := filepath.Join(d, name); isExec(p) {
			return p
		}
	}
	return ""
}

func isExec(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir() && st.Mode()&0o111 != 0
}

// Commands agents commonly reach for. Not installed is fine (Info); installed but not on gerdr's PATH is not.
var tools = []string{"git", "node", "npx", "python3", "go", "ffmpeg", "rg"}

func toolCheck(name, cur, login string, loginOK bool) Check {
	c := Check{ID: "tool:" + name, Group: "tool", Title: name, Level: OK}
	inCur, inLogin := lookIn(name, cur), lookIn(name, login)
	switch {
	case inCur == "" && inLogin != "":
		// No fix button here: it'd be the PATH check's "re-read PATH" again, once per missing tool.
		c.Level, c.Summary = Bad, "终端里有（"+inLogin+"），agent 里找不到"
	case inCur == "" && loginOK:
		c.Level, c.Summary = Info, "没装"
	case inCur == "":
		c.Level, c.Summary = Info, "找不到"
	default:
		c.Summary = inCur
		if inLogin != "" && realPath(inLogin) != realPath(inCur) {
			c.Level = Info
			c.Detail = "终端里用的是另一份：" + inLogin
		}
	}
	return c
}

func realPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

func agentCheck(ctx context.Context, id, title string, find func() (string, error), dirs []string, deep bool) Check {
	c := Check{ID: "agent:" + id, Group: "agent", Title: title, Level: OK}
	cur, findErr := find()
	paths := sysenv.Installs(id, dirs)
	if cur != "" {
		found := false
		for _, p := range paths {
			if realPath(p) == realPath(cur) {
				found = true
			}
		}
		if !found {
			paths = append([]string{cur}, paths...)
		}
	}
	for _, p := range paths {
		in := Install{Path: p, Current: cur != "" && realPath(p) == realPath(cur)}
		if deep {
			in.Version, in.Error, in.Blocked = version(ctx, p)
		}
		c.Installs = append(c.Installs, in)
	}
	if findErr != nil {
		c.Level, c.Summary = Bad, "找不到："+findErr.Error()
		if len(paths) == 0 {
			c.Level, c.Summary = Info, "没装"
		}
		c.Fixes = append(c.Fixes, reloadFix)
		c.Fixes = append(c.Fixes, useFixes(id, c.Installs, deep)...)
		return c
	}
	c.Summary = cur
	if !deep {
		c.Detail = "点「运行诊断」会实际跑一下每一份，看能不能启动、是什么版本"
		return c
	}
	var current *Install
	best := ""
	for i := range c.Installs {
		in := &c.Installs[i]
		if in.Current {
			current = in
		}
		if in.Error == "" && newer(in.Version, best) {
			best = in.Version
		}
	}
	var notes []string
	switch {
	case current == nil:
	case current.Blocked:
		c.Level, c.Summary = Bad, "Gerdr 用的这一份一启动就被系统杀掉了（macOS 把它当恶意软件拦了）："+current.Path
	case current.Error != "":
		c.Level, c.Summary = Bad, "Gerdr 用的这一份启动不了："+current.Error
	default:
		c.Summary = current.Version + " · " + current.Path
		if best != "" && newer(best, current.Version) {
			c.Level = Warn
			notes = append(notes, "装着更新的一份（"+best+"），Gerdr 用的却是旧的")
		}
	}
	for _, in := range c.Installs {
		if !in.Current && in.Blocked {
			if c.Level.rank() < Warn.rank() {
				c.Level = Warn
			}
			notes = append(notes, "还装着一份会被 macOS 拦掉的 "+id+"："+in.Path+"。PATH 一乱就会用到它，建议删掉或升级")
		}
	}
	c.Detail = strings.Join(notes, "\n")
	c.Fixes = useFixes(id, c.Installs, deep)
	if current != nil && c.Level == OK {
		// The current one works and is the newest: offering an older copy would be a downgrade.
		c.Fixes = nil
	} else if current != nil && current.Error == "" {
		var up []Fix
		for _, f := range c.Fixes {
			for _, in := range c.Installs {
				if in.Path == f.Path && newer(in.Version, current.Version) {
					up = append(up, f)
				}
			}
		}
		c.Fixes = up
	}
	return c
}

// useFixes: "use this one" for every install that isn't the current one and (when we ran them) works.
func useFixes(id string, ins []Install, deep bool) []Fix {
	var out []Fix
	for _, in := range ins {
		if in.Current || deep && in.Error != "" {
			continue
		}
		label := "改用这一份"
		if in.Version != "" {
			label = "改用 " + in.Version
		}
		out = append(out, Fix{Kind: "useBin", Label: label, Agent: id, Path: in.Path})
	}
	return out
}

func version(ctx context.Context, bin string) (ver, errMsg string, blocked bool) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "--version")
	cmd.Env = sysenv.ChildEnv()
	cmd.Dir = os.TempDir()
	cmd.WaitDelay = 2 * time.Second
	out, err := cmd.CombinedOutput()
	first := strings.TrimSpace(strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0])
	if err != nil {
		var ee *exec.ExitError
		// SIGKILL right at exec, with nothing printed: on macOS that's the system refusing the binary
		// (XProtect / a revoked signature); the user saw a "malware blocked" dialog for it.
		if errors.As(err, &ee) && strings.Contains(ee.ProcessState.String(), "killed") && ctx.Err() == nil {
			return "", "启动就被系统杀掉了（SIGKILL）", true
		}
		msg := err.Error()
		if first != "" {
			msg += "：" + first
		}
		return "", msg, false
	}
	if m := verRe.FindString(first); m != "" {
		return m, "", false
	}
	return first, "", false
}

var verRe = regexp.MustCompile(`\d+(\.\d+)+`)

// newer: a > b, comparing dotted numbers; anything beats "".
func newer(a, b string) bool {
	if a == "" {
		return false
	}
	if b == "" {
		return true
	}
	pa, pb := strings.Split(verRe.FindString(a), "."), strings.Split(verRe.FindString(b), ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			return x > y
		}
	}
	return false
}

func usageChecks(list []hub.UsageInfo) []Check {
	var out []Check
	for _, u := range list {
		c := Check{ID: "usage:" + u.Agent, Group: "usage", Title: u.Name + " 额度", Level: OK, Summary: "读得到"}
		if u.Error != "" {
			c.Level, c.Summary = Warn, "最近一次读失败："+readable(u.Error)
			if u.Data != nil {
				c.Summary = "最近一次没刷新上（显示的是上一次的）：" + readable(u.Error)
			}
		}
		out = append(out, c)
	}
	return out
}

// Adapter errors are a JSON-RPC error object; the readable part is data.error.
func readable(s string) string {
	var e struct {
		Message string `json:"message"`
		Data    struct {
			Error string `json:"error"`
		} `json:"data"`
	}
	if json.Unmarshal([]byte(s), &e) != nil {
		return s
	}
	if e.Data.Error != "" {
		return e.Data.Error
	}
	if e.Message != "" {
		return e.Message
	}
	return s
}
