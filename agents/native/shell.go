package native

import (
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
	if sh == powershell {
		// -EncodedCommand so no quoting in the command can be mangled on the way in
		return sh, []string{"-NoProfile", "-NonInteractive", "-EncodedCommand", encodePS(psUTF8 + command)}
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

const powershell = "powershell.exe"

// psUTF8 makes PowerShell write UTF-8 to the pipe (pi's UTF8_OUTPUT_PREFIX): a Chinese Windows console defaults to
// GBK, which garbles non-ASCII paths and output.
const psUTF8 = "try { [Console]::OutputEncoding=[System.Text.Encoding]::UTF8 } catch {}\n"

// shell is the shell's path (powershell = PowerShell). Looked up on every call: Git installed while the app is
// running is used from the next command on.
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
	return powershell
}

// shellName is how the prompt names the shell.
func shellName() string {
	switch s := shell(); s {
	case powershell:
		return "Windows PowerShell (this computer has no bash: write PowerShell, not bash; Git for Windows would add bash)"
	default:
		return s
	}
}

// gitBash is Git for Windows' bash.exe: next to the git on PATH, else in the default install places. Not
// System32\bash.exe (that is WSL, a different machine with different paths).
func gitBash() string {
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
