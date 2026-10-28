// Package sysenv 管「子进程在什么环境里跑」：PATH 从哪来、claude / codex 在哪、
// 哪些环境变量不能往下传。
package sysenv

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

var (
	pathOnce sync.Once
)

// AdoptLoginPath 把登录 shell 的 PATH 并进本进程。
//
// 从 Finder / Dock 起的 mac App 拿到的 PATH 只有 /usr/bin:/bin:/usr/sbin:/sbin ——
// claude 装在 ~/.local/bin、codex 装在 nvm / volta / version-fox 的目录里，全都找不到，
// 表现是「装着却说没装」。所以开机问一次登录 shell（-l 读 profile、-i 读 rc，
// 大多数人的 PATH 是在 .zshrc 里改的）。终端里起的进程本来就有完整 PATH，合并一遍无害。
func AdoptLoginPath() {
	pathOnce.Do(func() {
		shell := os.Getenv("SHELL")
		if shell == "" {
			shell = "/bin/zsh"
		}
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()
		// 用标记包住，rc 里打印的欢迎语 / 报错不会混进来。
		cmd := exec.CommandContext(ctx, shell, "-l", "-i", "-c", `printf '\n__GERDR_PATH__%s__GERDR_PATH__\n' "$PATH"`)
		cmd.Stdin = nil
		out, err := cmd.Output()
		if err != nil && len(out) == 0 {
			return
		}
		s := string(out)
		i := strings.Index(s, "__GERDR_PATH__")
		if i < 0 {
			return
		}
		s = s[i+len("__GERDR_PATH__"):]
		j := strings.Index(s, "__GERDR_PATH__")
		if j < 0 {
			return
		}
		merged := mergePath(os.Getenv("PATH"), s[:j])
		// 一些常见位置兜底（rc 里出错退出时上面那步拿不到东西）。
		home, _ := os.UserHomeDir()
		merged = mergePath(merged, strings.Join([]string{
			filepath.Join(home, ".local/bin"),
			"/opt/homebrew/bin", "/usr/local/bin",
		}, ":"))
		os.Setenv("PATH", merged)
	})
}

func mergePath(a, b string) string {
	seen := map[string]bool{}
	var out []string
	for _, p := range append(strings.Split(a, ":"), strings.Split(b, ":")...) {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return strings.Join(out, ":")
}

// ChildEnv 是给 agent 子进程的环境：本进程的环境去掉会让它们认错身份的那几个。
//
//   - CLAUDECODE / CLAUDE_CODE_ENTRYPOINT / CLAUDE_CODE_SSE_PORT：gerdr 常常是在一个
//     claude 会话里被起的，带着这几个的话子 claude 会以为自己是嵌套调用；
//   - HERDR_*：在 herdr 的 pane 里起 gerdr 时会继承到。带着它的子 claude 会通过 herdr 的
//     hook 把**自己的**会话报成那个 pane 的会话（herdr-web 踩过：chat 里出现别人的对话）；
//   - GERDR_TOKEN：本服务的登录令牌，agent 跑的命令不该看得见。
func ChildEnv(extra ...string) []string {
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		switch {
		case k == "CLAUDECODE", k == "CLAUDE_CODE_ENTRYPOINT", k == "CLAUDE_CODE_SSE_PORT",
			strings.HasPrefix(k, "HERDR_"), strings.HasPrefix(k, "GERDR_"):
			continue
		case k == "PATH" && gerdrBinDir != "":
			// agent 跑的命令里能直接敲 gerdr（多个会话之间互相调用，内置的 gerdr skill 靠它）
			kv = "PATH=" + gerdrBinDir + string(os.PathListSeparator) + strings.TrimPrefix(kv, "PATH=")
		}
		env = append(env, kv)
	}
	env = append(env, gerdrEnv...)
	return append(env, extra...)
}

var (
	gerdrEnv    []string
	gerdrBinDir string
)

// SetGerdrContext：这个 gerdr 服务的数据目录。之后起的 agent 都带上 GERDR_ENV=1 和 GERDR_DIR，
// 它们跑的 `gerdr session …` 靠这两个找到这个服务（端口在 <dir>/server.json、令牌在 <dir>/token）。
// GERDR_* 其余的（令牌、端口……）一律不往下传。
func SetGerdrContext(dir string) {
	gerdrEnv = []string{"GERDR_ENV=1", "GERDR_DIR=" + dir}
	if exe, err := os.Executable(); err == nil {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		gerdrBinDir = filepath.Dir(exe)
	}
}

// FindClaude 找 claude 可执行文件。GERDR_CLAUDE_BIN 优先。
func FindClaude() (string, error) {
	if p := os.Getenv("GERDR_CLAUDE_BIN"); p != "" {
		return p, nil
	}
	AdoptLoginPath()
	if p, err := exec.LookPath("claude"); err == nil {
		return p, nil
	}
	home, _ := os.UserHomeDir()
	for _, p := range []string{filepath.Join(home, ".local/bin/claude"), filepath.Join(home, ".claude/local/claude")} {
		if isExec(p) {
			return p, nil
		}
	}
	return "", &NotFoundError{Name: "claude", Hint: "安装 Claude Code：curl -fsSL https://claude.ai/install.sh | bash，或设 GERDR_CLAUDE_BIN"}
}

// FindBin 找设置里配的可执行文件：绝对路径（~/ 展开）直接用，光秃秃的名字按登录 shell 的 PATH 找。
func FindBin(name string) (string, error) {
	if strings.HasPrefix(name, "~/") {
		home, _ := os.UserHomeDir()
		name = filepath.Join(home, name[2:])
	}
	if strings.Contains(name, "/") {
		if isExec(name) {
			return name, nil
		}
		return "", &NotFoundError{Name: name, Hint: "这个路径不存在或不能执行"}
	}
	AdoptLoginPath()
	p, err := exec.LookPath(name)
	if err != nil {
		return "", &NotFoundError{Name: name, Hint: "PATH 里找不到它"}
	}
	return p, nil
}

// FindCodex 找 codex。npm 装的 `codex` 是一个 node 写的壳，真正的程序是包里那个 Rust
// 二进制 —— 直接起它，省掉一个常驻的 node 进程（几十 MB），也不依赖 node 在 PATH 上。
func FindCodex() (string, error) {
	if p := os.Getenv("GERDR_CODEX_BIN"); p != "" {
		return p, nil
	}
	AdoptLoginPath()
	p, err := exec.LookPath("codex")
	if err != nil {
		return "", &NotFoundError{Name: "codex", Hint: "安装 Codex CLI：npm i -g @openai/codex 或 brew install codex，或设 GERDR_CODEX_BIN"}
	}
	if native := nativeCodex(p); native != "" {
		return native, nil
	}
	return p, nil
}

func nativeCodex(shim string) string {
	if !isScript(shim) {
		return ""
	}
	real, err := filepath.EvalSymlinks(shim)
	if err != nil {
		return ""
	}
	// .../@openai/codex/bin/codex.js → .../@openai/codex
	pkg := filepath.Dir(filepath.Dir(real))
	arch := map[string]string{"arm64": "aarch64", "amd64": "x86_64"}[runtime.GOARCH]
	osName := map[string]string{"darwin": "apple-darwin", "linux": "unknown-linux-musl"}[runtime.GOOS]
	plat := map[string]string{"arm64": "arm64", "amd64": "x64"}[runtime.GOARCH]
	triple := arch + "-" + osName
	for _, c := range []string{
		filepath.Join(pkg, "node_modules/@openai/codex-"+runtime.GOOS+"-"+plat, "vendor", triple, "bin/codex"),
		filepath.Join(pkg, "vendor", triple, "bin/codex"),
		filepath.Join(pkg, "vendor", triple, "codex", "codex"),
	} {
		if isExec(c) {
			return c
		}
	}
	return ""
}

func isExec(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir() && st.Mode()&0o111 != 0
}

func isScript(p string) bool {
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	defer f.Close()
	line, _ := bufio.NewReader(f).ReadString('\n')
	return strings.HasPrefix(line, "#!")
}

type NotFoundError struct{ Name, Hint string }

func (e *NotFoundError) Error() string { return e.Name + " not found: " + e.Hint }

// SafeGerdrCLI：这条 shell 命令是不是「只调了 gerdr 自己的 session / ui 命令」—— 是的话适配器直接放行，
// 不弹审批（多个会话协作时每一步都要人点一下就没法用了；codex 的沙箱连本机端口都算联网，必然会来问）。
//
// 放行的边界：
//   - 只能是一条 `gerdr session …` / `gerdr ui …`，不能用 ; && | 拼别的命令，不能有重定向、$(…)、反引号；
//     单引号里随便写（shell 不展开），双引号里不能有 $ 和反引号；
//   - 不带 --mode：开新会话时自己指定权限模式等于给自己提权（沙箱里的 agent 开一个 bypassPermissions 的会话
//     就什么都能干了），这种还是要人点头。
func SafeGerdrCLI(cmd string) bool {
	cmd = strings.TrimSpace(cmd)
	var words []string
	var cur strings.Builder
	inWord := false
	flush := func() {
		if inWord {
			words = append(words, cur.String())
			cur.Reset()
			inWord = false
		}
	}
	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		switch {
		case c == '\'':
			j := strings.IndexByte(cmd[i+1:], '\'')
			if j < 0 {
				return false
			}
			cur.WriteString(cmd[i+1 : i+1+j])
			inWord = true
			i += j + 1
		case c == '"':
			j := strings.IndexByte(cmd[i+1:], '"')
			if j < 0 {
				return false
			}
			inner := cmd[i+1 : i+1+j]
			if strings.ContainsAny(inner, "$`\\") {
				return false
			}
			cur.WriteString(inner)
			inWord = true
			i += j + 1
		case c == ' ' || c == '\t':
			flush()
		case strings.IndexByte(";&|<>`$(){}\n\r\\*?[]~#!", c) >= 0:
			return false
		default:
			cur.WriteByte(c)
			inWord = true
		}
	}
	flush()
	if len(words) < 2 || (words[0] != "gerdr" && !strings.HasSuffix(words[0], "/gerdr")) {
		return false
	}
	if words[1] != "session" && words[1] != "ui" {
		return false
	}
	for _, w := range words[2:] {
		if w == "--mode" || strings.HasPrefix(w, "--mode=") {
			return false
		}
	}
	return true
}
