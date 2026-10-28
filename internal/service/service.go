// Package service 把 gerdr 装成 launchd 的 LaunchAgent：退出 App 之后 gerdr 照样跑着（会话、远程访问都在），
// 登录就起、崩了就拉起来。只做 macOS（照 herdr-web 的 internal/service，那边还有 systemd）。
//
// 和 App 怎么分工（docs/dev/SERVICE.md）：
//   - 装 / 卸只从设置里点（/api/service），不用敲命令行。plist 里的程序就是 App 包里那个 gerdr，
//     所以 App 更新 = 服务的二进制跟着换了；App 一启动发现服务的版本和自己不一样就 kickstart -k 重启它。
//   - 一个数据目录只能有一个 gerdr（server.lock）。服务模式（--service）拿不到锁就**等着**，不退出：
//     退出的话 KeepAlive 每 10 秒拉一次，一直在日志里刷「已经在运行」。App 那边的后端也是同样的思路，
//     见 cmd/gerdr 的 appAttach —— 谁先拿到锁谁干活，另一个等着接班。
package service

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

// Label 是 launchd 的 Label，也是 plist 的文件名。
const Label = "io.github.zbysir.gerdr"

// Manager 管这台机器上的那个服务。Self：本进程就是那个服务（--service 起的）。
type Manager struct {
	Exec string // 要常驻的二进制（解过 symlink）
	Dir  string // 数据目录
	Env  map[string]string
	Self bool

	home string
	uid  string
}

func New(exe, dir string, env map[string]string, self bool) *Manager {
	m := &Manager{Exec: exe, Dir: dir, Env: env, Self: self, uid: strconv.Itoa(os.Getuid())}
	m.home, _ = os.UserHomeDir()
	return m
}

// Why 是装不了的原因；空 = 能装。
func (m *Manager) Why() string {
	if runtime.GOOS != "darwin" {
		return "后台常驻目前只做了 macOS"
	}
	// go run 出来的二进制在临时目录里，装上去下次登录就找不到了
	if m.Exec == "" || strings.HasPrefix(m.Exec, filepath.Clean(os.TempDir())) || strings.Contains(m.Exec, "go-build") {
		return "这个 gerdr 是临时编出来的（" + m.Exec + "），装不了后台服务：用 App 里的那个"
	}
	// 调试实例（make dev）用的是别的数据目录：装上去会和日常用的那份抢同一个 Label
	if def := filepath.Join(m.home, ".gerdr"); filepath.Clean(m.Dir) != def {
		return "调试实例（数据目录不是 ~/.gerdr）不装后台服务"
	}
	return ""
}

func (m *Manager) PlistPath() string {
	return filepath.Join(m.home, "Library", "LaunchAgents", Label+".plist")
}

// LogPath：服务的 stdout / stderr。launchd 起的进程没有终端，出了错只在这里看得到。
func (m *Manager) LogPath() string { return filepath.Join(m.Dir, "logs", "gerdr.log") }

// Installed：装了、而且是**这个**数据目录的那份。调试实例（Why 非空）看到日常那份 plist 不算 ——
// 不然它会以为自己开了后台常驻，一直等一个不会来的服务。
func (m *Manager) Installed() bool {
	if m.Why() != "" {
		return false
	}
	_, err := os.Stat(m.PlistPath())
	return err == nil
}

// Install 写 plist 并加载。已经装过就先卸再装（改了 plist 只 kickstart 不生效，launchd 不重读）。
func (m *Manager) Install() error {
	if w := m.Why(); w != "" {
		return errors.New(w)
	}
	if err := os.MkdirAll(filepath.Dir(m.PlistPath()), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(m.LogPath()), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(m.PlistPath(), []byte(m.plist()), 0o600); err != nil {
		return err
	}
	_ = run("launchctl", "bootout", m.target())
	if err := run("launchctl", "bootstrap", "gui/"+m.uid, m.PlistPath()); err != nil {
		return fmt.Errorf("launchctl bootstrap 失败：%w", err)
	}
	return nil
}

// Uninstall 删 plist 并卸载。Self 的时候 bootout 会把本进程一起停掉，调用方要先把响应发出去。
func (m *Manager) Uninstall() error {
	if err := os.Remove(m.PlistPath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	_ = run("launchctl", "bootout", m.target())
	return nil
}

// Restart：换了二进制之后要它生效（kickstart -k = 杀掉再起，二进制按路径重新读）。
func (m *Manager) Restart() error { return run("launchctl", "kickstart", "-k", m.target()) }

// PID：launchd 那边这个服务的进程；0 = 没加载或没在跑（起来就崩、正在退避）。
func (m *Manager) PID() int {
	out, err := exec.Command("launchctl", "list", Label).Output()
	if err != nil {
		return 0
	}
	// `launchctl list <label>` 吐的是 plist 文本：  "PID" = 1234;
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, `"PID"`) {
			if i := strings.LastIndex(line, "= "); i > 0 {
				n, _ := strconv.Atoi(strings.Trim(strings.TrimSpace(line[i+2:]), ";"))
				return n
			}
		}
	}
	return 0
}

func (m *Manager) target() string { return "gui/" + m.uid + "/" + Label }

// EnvSnapshot：抄进 plist 的环境变量 —— 只抄服务本身的配置（端口、数据目录、界面目录……），
// 不抄 GERDR_TOKEN 和 DNS 凭据：plist 谁都能 `launchctl print` 出来，令牌在 ~/.gerdr/token、
// DNS 凭据在 remote.json，服务起来自己会读。PATH 也不抄：sysenv.AdoptLoginPath 开机问登录 shell。
func EnvSnapshot() map[string]string {
	env := map[string]string{}
	for _, k := range []string{"GERDR_DIR", "GERDR_HOST", "GERDR_PORT", "GERDR_UI", "GERDR_ESM_CDN",
		"GERDR_CLAUDE_IDLE_MINUTES", "GERDR_NO_SNAPSHOTS", "GERDR_DEBUG", "SHELL", "LANG"} {
		if v := os.Getenv(k); v != "" {
			env[k] = v
		}
	}
	return env
}

// plist：
//   - ProcessType=Interactive：不写会被 macOS 当后台任务限速（App Nap），agent 的输出一卡一卡的。
//   - KeepAlive=true：崩了就拉起来。拿不到锁时服务是等着、不是退出（见包注释），不会被反复拉。
//   - LaunchAgent 是登录时起，不是开机时起：系统级 daemon 跑成 root，agent 也就是 root 的，不做。
func (m *Manager) plist() string {
	var env strings.Builder
	keys := make([]string, 0, len(m.Env))
	for k := range m.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		env.WriteString("    <key>" + xmlEsc(k) + "</key>\n    <string>" + xmlEsc(m.Env[k]) + "</string>\n")
	}
	log := xmlEsc(m.LogPath())
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>` + Label + `</string>
  <key>ProgramArguments</key>
  <array>
    <string>` + xmlEsc(m.Exec) + `</string>
    <string>--service</string>
  </array>
  <key>EnvironmentVariables</key>
  <dict>
` + env.String() + `  </dict>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>ProcessType</key>
  <string>Interactive</string>
  <key>WorkingDirectory</key>
  <string>` + xmlEsc(m.home) + `</string>
  <key>StandardOutPath</key>
  <string>` + log + `</string>
  <key>StandardErrorPath</key>
  <string>` + log + `</string>
</dict>
</plist>
`
}

func xmlEsc(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;").Replace(s)
}

func run(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		if s := strings.TrimSpace(string(out)); s != "" {
			return fmt.Errorf("%s: %s", err, s)
		}
		return err
	}
	return nil
}
