package native

import (
	"cmp"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf16"
)

// The bash tool's shell. Unix: $SHELL, else /bin/sh. Windows has neither: use Git for Windows' bash (found the way
// Claude Code finds it), and when it isn't installed fall back to PowerShell rather than fail every command — before
// this, a Windows machine without Git got `exec: "/bin/sh": executable file not found` on every call, and the agent
// spent twenty turns writing shims.

// shellCmd is the program and arguments that run command.
func shellCmd(command string) (string, []string) {
	sh := shell()
	if isPowerShell(sh) {
		// -EncodedCommand so no quoting in the command can be mangled on the way in
		return sh, []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-EncodedCommand", encodePS(psUTF8 + command)}
	}
	return sh, []string{"-c", command}
}

// encodePS is PowerShell's -EncodedCommand form: base64 of the UTF-16LE text.
func encodePS(command string) string {
	u := utf16.Encode([]rune(command))
	b := make([]byte, 2*len(u))
	for i, c := range u {
		b[2*i], b[2*i+1] = byte(c), byte(c>>8)
	}
	return base64.StdEncoding.EncodeToString(b)
}

// psUTF8 makes PowerShell write UTF-8 to our pipe (Console.OutputEncoding: a Chinese Windows console defaults to GBK,
// which garbles non-ASCII paths and output) and to programs it pipes into ($OutputEncoding). Without a BOM, and on
// the command's own first line so the line numbers in PowerShell's errors stay right. Windows PowerShell 5.1 needs
// it; PowerShell 7 already writes UTF-8 to a pipe.
const psUTF8 = "try { [Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false); $OutputEncoding = [Console]::OutputEncoding } catch {}; "

// shell is the shell's path. Looked up on every call: Git or PowerShell 7 installed while the app is running is
// used from the next command on.
func shell() string {
	if runtime.GOOS != "windows" {
		if s := os.Getenv("SHELL"); s != "" {
			return s
		}
		return "/bin/sh"
	}
	// $SHELL on Windows is only of use when it is a real Windows path (a Git Bash session sets /usr/bin/bash)
	if s := os.Getenv("SHELL"); s != "" && isFile(s) {
		return s
	}
	if b := gitBash(); b != "" {
		return b
	}
	return powerShell()
}

func isPowerShell(sh string) bool {
	switch exeName(sh) {
	case "pwsh.exe", "powershell.exe", "pwsh", "powershell":
		return true
	}
	return false
}

// exeName is a program path's lower-case file name, split at \ and / alike.
func exeName(p string) string { return strings.ToLower(p[strings.LastIndexAny(p, `\/`)+1:]) }

// powerShell is PowerShell 7 when it is installed, else Windows PowerShell 5.1, which every Windows has. 7 runs
// `a && b` (5.1 rejects the syntax, and models write it all the time) and writes UTF-8. Order as DeepSeek Harness's
// resolvePwshPath: the default install, PATH (a Store install is there), then 5.1.
func powerShell() string {
	cands := []string{filepath.Join(cmp.Or(os.Getenv("ProgramFiles"), `C:\Program Files`), "PowerShell", "7", "pwsh.exe")}
	for _, d := range filepath.SplitList(os.Getenv("PATH")) {
		if d = strings.Trim(strings.TrimSpace(d), `"`); d != "" {
			cands = append(cands, filepath.Join(d, "pwsh.exe"))
		}
	}
	for _, c := range cands {
		// Lstat: the Store's pwsh.exe is an app execution alias whose target Stat may not read (access denied).
		// CreateProcess runs the alias fine.
		if st, err := os.Lstat(c); err == nil && !st.IsDir() {
			return c
		}
	}
	if p := filepath.Join(cmp.Or(os.Getenv("SystemRoot"), `C:\Windows`), "System32", "WindowsPowerShell", "v1.0", "powershell.exe"); isFile(p) {
		return p
	}
	return "powershell.exe"
}

// shellName is how the prompt names the shell.
func shellName() string {
	sh := shell()
	if !isPowerShell(sh) {
		return sh
	}
	name := "PowerShell 7"
	if exeName(sh) == "powershell.exe" {
		name = "Windows PowerShell 5.1 (no `&&` / `||`: use `a; if ($?) { b }`)"
	}
	return name + " (this computer has no bash: write PowerShell, not bash; Git for Windows would add bash)"
}

// gitBash is Git for Windows' bash.exe: next to the git on PATH, else in the default install places. Not
// System32\bash.exe (that is WSL, a different machine with different paths).
func gitBash() string {
	// the variable Claude Code reads for a bash somewhere else
	if b := os.Getenv("CLAUDE_CODE_GIT_BASH_PATH"); b != "" && isFile(b) {
		return b
	}
	if g, err := exec.LookPath("git"); err == nil {
		// <root>\cmd\git.exe or <root>\mingw64\bin\git.exe
		d := filepath.Dir(g)
		for range 3 {
			if b := filepath.Join(d, "bin", "bash.exe"); isFile(b) {
				return b
			}
			d = filepath.Dir(d)
		}
	}
	for _, root := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramW6432"), os.Getenv("ProgramFiles(x86)"), filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs")} {
		if root == "" || root == "Programs" {
			continue
		}
		if b := filepath.Join(root, "Git", "bin", "bash.exe"); isFile(b) {
			return b
		}
	}
	if h := os.Getenv("USERPROFILE"); h != "" { // scoop install git
		if b := filepath.Join(h, "scoop", "apps", "git", "current", "bin", "bash.exe"); isFile(b) {
			return b
		}
	}
	// some other bash on PATH (MSYS2, Cygwin), but not WSL's launcher in System32
	if b, err := exec.LookPath("bash.exe"); err == nil && !isWSLBash(b) {
		return b
	}
	return ""
}

// isWSLBash: C:\Windows\System32\bash.exe (or the WoW64 Sysnative path) runs WSL, a different machine.
func isWSLBash(p string) bool {
	p = strings.ToLower(filepath.Clean(p))
	win := strings.ToLower(os.Getenv("SystemRoot")) // C:\Windows
	if win == "" {
		win = `c:\windows`
	}
	return p == filepath.Join(win, "system32", "bash.exe") || p == filepath.Join(win, "sysnative", "bash.exe")
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
