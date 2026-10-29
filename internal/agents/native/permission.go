package native

import (
	"path/filepath"
	"strings"

	"github.com/coder/acp-go-sdk"

	"github.com/zbysir/gerdr/internal/sysenv"
)

// 权限模式。没有沙箱（codex 那种），所以「自动」只放到改工作区里的文件为止，命令照样要问 ——
// 一条 rm -rf 没人看一眼就跑了是收不回来的。只读的命令（ls、git status、rg……）任何模式都不问。
const (
	modeDefault     = "default"
	modeAcceptEdits = "acceptEdits"
	modeReadOnly    = "plan"
	modeBypass      = "bypassPermissions"
)

// id 和 claude 的同名模式一致：hub / 界面按 id 认（比如 gerdr session new --mode）。
var modes = []acp.SessionMode{
	{Id: modeDefault, Name: "Ask", Description: acp.Ptr("改文件、跑命令前都问你（只读的命令不问）")},
	{Id: modeAcceptEdits, Name: "Accept edits", Description: acp.Ptr("工作目录里的文件直接改；跑命令前问你")},
	{Id: modeReadOnly, Name: "Read only", Description: acp.Ptr("只看不改：不改文件，只跑只读的命令")},
	{Id: modeBypass, Name: "Full access", Description: acp.Ptr("什么都不问")},
}

func validMode(m string) bool {
	for _, x := range modes {
		if string(x.Id) == m {
			return true
		}
	}
	return false
}

// verdict：这次调用要怎么处理。
type verdict int

const (
	allow verdict = iota
	ask
	deny
)

// decide 按模式和已放行的规则判断一次工具调用。rule 是「以后不再问」时要记下的规则（空 = 不给这个选项）。
func decide(mode string, t *tool, args map[string]any, cwd string, allowed func(string) bool) (v verdict, rule string) {
	if t.readOnly {
		return allow, ""
	}
	switch t.name {
	case "write", "edit":
		if mode == modeBypass {
			return allow, ""
		}
		if mode == modeReadOnly {
			return deny, ""
		}
		p := str(args, "path")
		if !filepath.IsAbs(p) {
			p = filepath.Join(cwd, p)
		}
		inside := within(p, cwd)
		if inside && (mode == modeAcceptEdits || allowed("edit")) {
			return allow, ""
		}
		if inside {
			return ask, "edit"
		}
		return ask, ""
	case "bash":
		cmd := str(args, "command")
		if mode == modeBypass || readOnlyCommand(cmd) || sysenv.SafeGerdrCLI(cmd) {
			return allow, ""
		}
		if mode == modeReadOnly {
			return deny, ""
		}
		segs := segments(cmd)
		if len(segs) > 0 {
			ok := true
			for _, s := range segs {
				if !readOnlyCommand(s) && !allowed("bash:"+cmdPrefix(s)) {
					ok = false
				}
			}
			if ok {
				return allow, ""
			}
		}
		if len(segs) == 1 && !strings.ContainsAny(cmd, "`$<>") {
			return ask, "bash:" + cmdPrefix(segs[0])
		}
		return ask, ""
	}
	if mode == modeReadOnly {
		return deny, ""
	}
	if mode == modeBypass || allowed("tool:"+t.name) {
		return allow, ""
	}
	return ask, "tool:" + t.name
}

func within(p, dir string) bool {
	r, err := filepath.Rel(dir, filepath.Clean(p))
	return err == nil && r != ".." && !strings.HasPrefix(r, "../")
}

// segments 把 `a && b | c; d` 拆成几段命令。引号里的不拆（粗糙但够用：拆错了顶多是多问一次）。
func segments(cmd string) []string {
	var out []string
	var cur strings.Builder
	var q byte
	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		if q != 0 {
			cur.WriteByte(c)
			if c == q {
				q = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			q = c
			cur.WriteByte(c)
		case ';', '|', '&', '\n':
			if s := strings.TrimSpace(cur.String()); s != "" {
				out = append(out, s)
			}
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	if s := strings.TrimSpace(cur.String()); s != "" {
		out = append(out, s)
	}
	return out
}

// cmdPrefix：「以后不再问」记的是命令的前一两个词（`go test ./...` → `go test`，`ls -la` → `ls`）。
func cmdPrefix(seg string) string {
	f := strings.Fields(seg)
	if len(f) == 0 {
		return ""
	}
	if len(f) > 1 && !strings.HasPrefix(f[1], "-") && !strings.ContainsAny(f[1], "/.=") {
		return f[0] + " " + f[1]
	}
	return f[0]
}

// 只读命令：不写文件、不改状态。只认最常见的，认不准就问。
var readOnlyCmds = map[string]bool{
	"ls": true, "cat": true, "head": true, "tail": true, "wc": true, "pwd": true, "echo": true, "printf": true,
	"rg": true, "grep": true, "egrep": true, "fgrep": true, "which": true, "whereis": true, "type": true, "file": true,
	"stat": true, "du": true, "df": true, "tree": true, "date": true, "uname": true, "whoami": true, "id": true,
	"basename": true, "dirname": true, "realpath": true, "readlink": true, "sort": true, "uniq": true, "cut": true,
	"tr": true, "diff": true, "cmp": true, "md5": true, "md5sum": true, "shasum": true, "sha256sum": true, "jq": true,
	"less": true, "true": true, "false": true, "test": true, "nl": true, "column": true, "hostname": true,
}

var readOnlyGit = map[string]bool{
	"status": true, "diff": true, "log": true, "show": true, "branch": true, "rev-parse": true, "blame": true,
	"ls-files": true, "remote": true, "describe": true, "shortlog": true, "grep": true, "cat-file": true, "reflog": true,
}

func readOnlyCommand(cmd string) bool {
	if strings.ContainsAny(cmd, "`>") || strings.Contains(cmd, "$(") {
		return false
	}
	segs := segments(cmd)
	if len(segs) == 0 {
		return false
	}
	for _, s := range segs {
		f := strings.Fields(s)
		if len(f) == 0 {
			return false
		}
		name := filepath.Base(f[0])
		switch {
		case readOnlyCmds[name]:
		case name == "cd" && len(f) <= 2:
		case name == "find":
			for _, a := range f[1:] {
				if a == "-exec" || a == "-execdir" || a == "-delete" || a == "-ok" || a == "-fprint" {
					return false
				}
			}
		case name == "git" && len(f) > 1:
			sub := f[1]
			if !readOnlyGit[sub] {
				return false
			}
			// git branch -d / remote add 这类会改东西
			if (sub == "branch" || sub == "remote") && len(f) > 2 && (strings.HasPrefix(f[2], "-d") || strings.HasPrefix(f[2], "-D") || f[2] == "add" || f[2] == "remove" || f[2] == "rm" || f[2] == "set-url" || f[2] == "-m") {
				return false
			}
		case name == "sed" && len(f) > 1 && !strings.Contains(s, " -i"):
		case (name == "go" || name == "cargo" || name == "npm" || name == "pnpm") && len(f) > 1 && (f[1] == "version" || (f[1] == "env" && len(f) == 2) || f[1] == "list" || f[1] == "ls"):
		default:
			return false
		}
	}
	return true
}
