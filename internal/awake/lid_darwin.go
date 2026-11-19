package awake

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"github.com/gerdr-dev/gerdr/internal/i18n"
)

// 合盖也不睡：caffeinate 管不了合盖（那是硬件层的睡眠），要 `pmset -a disablesleep 1` —— Amphetamine 的
// 「合盖模式」也是这条。它要 root，所以靠一条 sudoers 规则只放行这两条命令（和 Amphetamine Enhancer 装的
// /etc/sudoers.d/amphetamine_PowerProtect 同一个范围；装过 Amphetamine 的机器直接就能用）。
//
// disablesleep is one switch for the whole machine and outlives the process that set it: if gerdr crashed with it on,
// a Mac put in a bag would stay awake and hot. So three safeguards:
//   - a watcher sh started alongside (its own process group, so killing gerdr's group spares it) lets go once gerdr is gone;
//   - a holder file (below) left behind is let go at the next start (when the watcher never ran either);
//   - on battery at or under 20% it lets go (Keeper.watchBattery).
//
// Shared with others: the lid-awake add-on (and Amphetamine-style tools) flip the same switch, and each turning it off
// when *its* stretch ended switched it off under the other: one night Gerdr's remote stretch ended at 23:18 and the Mac
// slept although the add-on still wanted it awake. So whoever turns it on writes a holder file, <gerdr dir>/run/
// disablesleep/<name>, holding a pid that lives as long as the hold, and turns it off only when no other holder's pid
// is alive. The add-on follows the same rule (internal/addons/builtin/GUIDE.md, "Shared machine switches").

const (
	pmset   = "/usr/bin/pmset"
	sudo    = "/usr/bin/sudo"
	ruleDst = "/etc/sudoers.d/gerdr_pmset"
)

// holderName: Gerdr's own file among the holders of disablesleep.
const holderName = "gerdr"

func holdersDir(dir string) string { return filepath.Join(dir, "run", "disablesleep") }

// oldLidMarker: where the hold was recorded before holder files; only read by lidCleanup.
func oldLidMarker(dir string) string { return filepath.Join(dir, "awake-lid") }

// othersHold: some other holder's pid is still alive. Files of dead holders are removed on the way.
func othersHold(dir string) bool {
	ents, _ := os.ReadDir(holdersDir(dir))
	for _, e := range ents {
		if e.Name() == holderName {
			continue
		}
		f := filepath.Join(holdersDir(dir), e.Name())
		b, _ := os.ReadFile(f)
		pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
		if err == nil && pid > 0 && (syscall.Kill(pid, 0) == nil || errors.Is(syscall.Kill(pid, 0), syscall.EPERM)) {
			return true
		}
		_ = os.Remove(f)
	}
	return false
}

// release drops Gerdr's hold and turns disablesleep off unless someone else still holds it.
func release(dir string) error {
	_ = os.Remove(filepath.Join(holdersDir(dir), holderName))
	if othersHold(dir) {
		return nil
	}
	return setDisableSleep(false)
}

func setDisableSleep(on bool) error {
	v := "0"
	if on {
		v = "1"
	}
	out, err := exec.Command(sudo, "-n", pmset, "-a", "disablesleep", v).CombinedOutput()
	if err != nil {
		return fmt.Errorf("pmset disablesleep %s：%v %s", v, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// lidReady：sudoers 里放行了，不用输密码就能跑。
func lidReady() bool {
	return exec.Command(sudo, "-n", "-l", pmset, "-a", "disablesleep", "1").Run() == nil
}

func lidOn(dir string) (func(), error) {
	if !lidReady() {
		return nil, errors.New(i18n.S("还没授权：设置 → 实验室里点「授权」（要在电脑上输一次密码）", "not authorized yet: click \"Authorize\" in Settings → Labs (you'll enter your password once on this computer)"))
	}
	if err := os.MkdirAll(holdersDir(dir), 0o700); err != nil {
		return nil, err
	}
	holder := filepath.Join(holdersDir(dir), holderName)
	if err := os.WriteFile(holder, []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		return nil, err
	}
	if err := setDisableSleep(true); err != nil {
		_ = os.Remove(holder)
		return nil, err
	}
	// Once gerdr is gone: drop its hold, then let go unless another holder's pid is alive (same rule as othersHold).
	watch := exec.Command("/bin/sh", "-c", `while kill -0 "$0" 2>/dev/null; do sleep 5; done
rm -f "$1/`+holderName+`"
for f in "$1"/*; do
  [ -f "$f" ] || continue
  p=$(cat "$f" 2>/dev/null)
  case "$p" in ''|*[!0-9]*) rm -f "$f"; continue ;; esac
  if kill -0 "$p" 2>/dev/null || ps -p "$p" >/dev/null 2>&1; then exit 0; fi
  rm -f "$f"
done
`+sudo+` -n `+pmset+` -a disablesleep 0`, strconv.Itoa(os.Getpid()), holdersDir(dir))
	watch.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := watch.Start(); err == nil {
		go func() { _ = watch.Wait() }()
	} else {
		watch = nil
	}
	return func() {
		if watch != nil {
			_ = watch.Process.Kill()
		}
		if err := release(dir); err != nil {
			// The hold file is gone already; put it back so the next start retries the release.
			_ = os.WriteFile(holder, []byte(strconv.Itoa(os.Getpid())), 0o600)
		}
	}, nil
}

// lidCleanup: the last run left its hold behind (gerdr crashed and the watcher never ran either): let it go.
func lidCleanup(dir string) {
	_, errOld := os.Stat(oldLidMarker(dir))
	_, errNew := os.Stat(filepath.Join(holdersDir(dir), holderName))
	if errOld != nil && errNew != nil {
		return
	}
	if release(dir) == nil {
		_ = os.Remove(oldLidMarker(dir))
	}
}

// sleepIfLidClosed: called after letting go on a low battery. With the lid closed, nothing else would put the Mac
// to sleep until idle sleep gets round to it (see Keeper.watchBattery); `pmset sleepnow` needs no root.
func sleepIfLidClosed() {
	out, err := exec.Command("/usr/sbin/ioreg", "-r", "-k", "AppleClamshellState", "-d", "1").Output()
	if err != nil || !strings.Contains(string(out), `"AppleClamshellState" = Yes`) {
		return
	}
	_ = exec.Command(pmset, "sleepnow").Run()
}

var battRe = regexp.MustCompile(`(\d+)%; ([a-zA-Z ]+);`)

// battery：读 `pmset -g batt`。台式机没电池 = Percent -1。
func battery() Battery {
	out, err := exec.Command(pmset, "-g", "batt").Output()
	if err != nil {
		return Battery{Percent: -1}
	}
	b := Battery{Percent: -1, OnBattery: strings.Contains(string(out), "'Battery Power'")}
	if m := battRe.FindSubmatch(out); m != nil {
		b.Percent, _ = strconv.Atoi(string(m[1]))
		// charging / charged / finishing charge / AC attached（插着但没在充）；discharging 是在用电池
		b.Charging = string(m[2]) == "charging" || string(m[2]) == "finishing charge"
	}
	return b
}

var userRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// SetupLid 装 sudoers 规则：弹 macOS 的管理员授权框（人在电脑前输一次密码）。
// 规则内容在 root 那边的 sh 里写进 root 自己 mktemp 的文件、visudo 验过再装 —— 不能由我们先写一个临时文件让 root 去拷：
// 那个文件是当前用户的，验完到拷之间被（比如 agent）换成「ALL NOPASSWD: ALL」就是提权。
func SetupLid() error {
	u, err := user.Current()
	if err != nil {
		return err
	}
	if !userRe.MatchString(u.Username) {
		return fmt.Errorf(i18n.S("用户名 %q 不认识", "unrecognized user name %q"), u.Username)
	}
	rule := u.Username + " ALL=(root) NOPASSWD: " + pmset + " -a disablesleep 1, " + pmset + " -a disablesleep 0"
	sh := `f=$(/usr/bin/mktemp /tmp/gerdr_pmset.XXXXXX) || exit 1; /usr/bin/printf '%s\n' '` + rule + `' > "$f" && /usr/sbin/visudo -cf "$f" && /usr/bin/install -m 0440 -o root -g wheel "$f" ` + ruleDst + `; r=$?; /bin/rm -f "$f"; exit $r`
	esc := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	script := `do shell script "` + esc.Replace(sh) + `" with administrator privileges with prompt "` +
		esc.Replace(i18n.S("Gerdr 要一次授权：合上盖子时也不让电脑睡（只放行 pmset disablesleep 这一条命令）", "Gerdr needs one-time authorization to keep the computer awake with the lid closed (it only allows the pmset disablesleep command).")) + `"`
	out, err := exec.Command("/usr/bin/osascript", "-e", script).CombinedOutput()
	if err != nil {
		s := strings.TrimSpace(string(out))
		if strings.Contains(s, "-128") {
			return errors.New(i18n.S("取消了授权", "authorization canceled"))
		}
		return fmt.Errorf(i18n.S("授权失败：%s", "authorization failed: %s"), s)
	}
	if !lidReady() {
		return errors.New(i18n.S("装好了规则，但 sudo 还是不放行", "the rule is installed, but sudo still refuses"))
	}
	return nil
}
