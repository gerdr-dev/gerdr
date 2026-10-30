package service

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// 生成的 plist 要 macOS 自己认：手写 XML「看着对、launchd 不认」时 bootstrap 只报一句没头没尾的 Load failed。
func TestPlistIsValid(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("plutil 只有 macOS 上有")
	}
	m := &Manager{Exec: "/Applications/gerdr.app/Contents/Resources/gerdr", Dir: "/Users/x/.gerdr", home: "/Users/x",
		Env: map[string]string{"GERDR_PORT": "7870", "WEIRD": `a<b>&c"d'e`}}
	p := filepath.Join(t.TempDir(), "t.plist")
	if err := os.WriteFile(p, []byte(m.plist()), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("plutil", "-lint", p).CombinedOutput(); err != nil {
		t.Fatalf("plutil 不认：%v\n%s", err, out)
	}
	out, _ := exec.Command("plutil", "-extract", "EnvironmentVariables.WEIRD", "raw", "-o", "-", p).Output()
	if strings.TrimSpace(string(out)) != `a<b>&c"d'e` {
		t.Errorf("转义把值搞坏了：%q", out)
	}
	out, _ = exec.Command("plutil", "-extract", "ProgramArguments.1", "raw", "-o", "-", p).Output()
	if strings.TrimSpace(string(out)) != "--service" {
		t.Error("少了 --service：拿不到锁时服务会退出，KeepAlive 每 10 秒拉一次")
	}
}

// 凭据不进 plist（launchctl print 谁都能看）。
func TestEnvSnapshotNoSecrets(t *testing.T) {
	t.Setenv("GERDR_TOKEN", "secret")
	t.Setenv("GERDR_CLOUDFLARE_DNS_API_TOKEN", "secret")
	t.Setenv("GERDR_PORT", "7870")
	env := EnvSnapshot()
	for k, v := range env {
		if v == "secret" {
			t.Errorf("%s 不该抄进 plist", k)
		}
	}
	if env["GERDR_PORT"] != "7870" {
		t.Error("服务配置要抄进去")
	}
}

func TestWhy(t *testing.T) {
	home, _ := os.UserHomeDir()
	ok := New("/Applications/gerdr.app/Contents/Resources/gerdr", filepath.Join(home, ".gerdr"), nil, false)
	if runtime.GOOS == "darwin" && ok.Why() != "" {
		t.Errorf("App 里的二进制 + 默认目录该能装：%s", ok.Why())
	}
	if New(filepath.Join(os.TempDir(), "go-build1/exe/gerdr"), filepath.Join(home, ".gerdr"), nil, false).Why() == "" {
		t.Error("go run 出来的临时二进制不该能装")
	}
	if New("/Applications/gerdr.app/Contents/Resources/gerdr", filepath.Join(home, ".gerdr-dev"), nil, false).Why() == "" {
		t.Error("调试实例不该能装（会和日常那份抢同一个 Label）")
	}
}

func TestDevInstanceNotInstalled(t *testing.T) {
	home, _ := os.UserHomeDir()
	m := New("/Applications/gerdr.app/Contents/Resources/gerdr", filepath.Join(home, ".gerdr-dev"), nil, false)
	m.home = t.TempDir()
	_ = os.MkdirAll(filepath.Dir(m.PlistPath()), 0o755)
	_ = os.WriteFile(m.PlistPath(), []byte("x"), 0o600)
	if m.Installed() {
		t.Error("调试实例看到日常那份 plist 不该算装了")
	}
}

// The App compares this with its own backend to tell whether the service runs a binary that moved (an update the
// App downloaded lives outside the bundle): a misread means restarting forever or never moving over.
func TestInstalledExec(t *testing.T) {
	m := &Manager{Exec: "/Users/x/Library/Application Support/Gerdr/backend/0.3.0/gerdr&co", Dir: "/Users/x/.gerdr", home: t.TempDir()}
	if m.InstalledExec() != "" {
		t.Error("nothing installed yet")
	}
	_ = os.MkdirAll(filepath.Dir(m.PlistPath()), 0o755)
	if err := os.WriteFile(m.PlistPath(), []byte(m.plist()), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := m.InstalledExec(); got != m.Exec {
		t.Errorf("InstalledExec = %q, want %q", got, m.Exec)
	}
}

func TestOptedOut(t *testing.T) {
	m := &Manager{Dir: t.TempDir()}
	if m.OptedOut() {
		t.Fatal("on by default")
	}
	if err := m.SetOptedOut(true); err != nil || !m.OptedOut() {
		t.Fatal("turning it off must stick", err)
	}
	if err := m.SetOptedOut(false); err != nil || m.OptedOut() {
		t.Fatal("turning it back on clears the marker", err)
	}
	if err := m.SetOptedOut(false); err != nil {
		t.Fatal("clearing twice is fine", err)
	}
}
