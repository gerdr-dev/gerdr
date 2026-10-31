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
)

// 合盖也不睡：caffeinate 管不了合盖（那是硬件层的睡眠），要 `pmset -a disablesleep 1` —— Amphetamine 的
// 「合盖模式」也是这条。它要 root，所以靠一条 sudoers 规则只放行这两条命令（和 Amphetamine Enhancer 装的
// /etc/sudoers.d/amphetamine_PowerProtect 同一个范围；装过 Amphetamine 的机器直接就能用）。
//
// disablesleep 是全局开关、不随进程走，gerdr 崩了它会一直开着 —— 塞进包里的电脑就一直醒着发烫。所以三道保险：
//   - 开的时候另起一个看门的 sh（自己一个进程组，gerdr 被整组杀也不带走它），等 gerdr 没了就关掉；
//   - 留一个标记文件，下次启动看到就关掉（看门的也没跑成的情况）；
//   - 用电池且电量 ≤ 20% 时放手（Keeper.watchBattery）。

const (
	pmset   = "/usr/bin/pmset"
	sudo    = "/usr/bin/sudo"
	ruleDst = "/etc/sudoers.d/gerdr_pmset"
)

func lidMarker(dir string) string { return filepath.Join(dir, "awake-lid") }

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
		return nil, errors.New("还没授权：设置 → 实验室里点「授权」（要在电脑上输一次密码）")
	}
	if err := setDisableSleep(true); err != nil {
		return nil, err
	}
	_ = os.WriteFile(lidMarker(dir), []byte(strconv.Itoa(os.Getpid())), 0o600)
	watch := exec.Command("/bin/sh", "-c", `while kill -0 "$0" 2>/dev/null; do sleep 5; done; `+sudo+` -n `+pmset+` -a disablesleep 0`, strconv.Itoa(os.Getpid()))
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
		if err := setDisableSleep(false); err != nil {
			return // 标记留着，下次启动再关
		}
		_ = os.Remove(lidMarker(dir))
	}, nil
}

// lidCleanup：上次没收拾干净（gerdr 崩了、看门的也没跑成）就关掉。
func lidCleanup(dir string) {
	if _, err := os.Stat(lidMarker(dir)); err != nil {
		return
	}
	if setDisableSleep(false) == nil {
		_ = os.Remove(lidMarker(dir))
	}
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
		return fmt.Errorf("用户名 %q 不认识", u.Username)
	}
	rule := u.Username + " ALL=(root) NOPASSWD: " + pmset + " -a disablesleep 1, " + pmset + " -a disablesleep 0"
	sh := `f=$(/usr/bin/mktemp /tmp/gerdr_pmset.XXXXXX) || exit 1; /usr/bin/printf '%s\n' '` + rule + `' > "$f" && /usr/sbin/visudo -cf "$f" && /usr/bin/install -m 0440 -o root -g wheel "$f" ` + ruleDst + `; r=$?; /bin/rm -f "$f"; exit $r`
	esc := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	script := `do shell script "` + esc.Replace(sh) + `" with administrator privileges with prompt "` +
		esc.Replace("Gerdr 要一次授权：合上盖子时也不让电脑睡（只放行 pmset disablesleep 这一条命令）") + `"`
	out, err := exec.Command("/usr/bin/osascript", "-e", script).CombinedOutput()
	if err != nil {
		s := strings.TrimSpace(string(out))
		if strings.Contains(s, "-128") {
			return errors.New("取消了授权")
		}
		return fmt.Errorf("授权失败：%s", s)
	}
	if !lidReady() {
		return errors.New("装好了规则，但 sudo 还是不放行")
	}
	return nil
}
