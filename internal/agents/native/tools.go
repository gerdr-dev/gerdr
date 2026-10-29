package native

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/coder/acp-go-sdk"

	"github.com/gerdr-dev/gerdr/internal/agents/native/llm"
	"github.com/gerdr-dev/gerdr/internal/sysenv"
)

// 工具：照 Claude Code / pi 的那一套（读、写、改、命令、搜内容、搜文件名、待办），名字用小写下划线
// （两家模型对这种名字最熟）。参数描述写给模型看，要具体。
//
// 每个工具返回给模型的是文字（read 读图片时是图），给界面的另算（diff、命令输出），两边可以不一样：
// 界面上看 diff，模型只要一句「改好了」。

type tool struct {
	name   string
	desc   string
	params map[string]any
	// readOnly：不改任何东西 —— 不用审批，同一批里可以并发跑。
	readOnly bool
	// hidden：不在界面上画成工具调用（todo_write 变成计划清单，不需要再来一行）。
	hidden bool
	// show：调用在界面上的标题、种类、涉及的文件。
	show func(args map[string]any, cwd string) (string, acp.ToolKind, []acp.ToolCallLocation)
	run  func(c *call, args map[string]any) result
}

type result struct {
	text    string
	images  []llm.Part
	err     bool
	display []acp.ToolCallContent // 界面上显示的；nil = 显示 text
}

func errResult(format string, a ...any) result {
	return result{text: fmt.Sprintf(format, a...), err: true}
}

// call 是一次工具调用的上下文。
type call struct {
	ctx context.Context
	r   *run
	id  string
}

func (c *call) cwd() string { return c.r.s.cwd() }

// abs：相对路径按会话的 cwd 算，~ 展开。
func (c *call) abs(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		h, _ := os.UserHomeDir()
		p = filepath.Join(h, p[1:])
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(c.cwd(), p)
	}
	return filepath.Clean(p)
}

// progress：跑着的时候往界面推一次内容（命令的输出）。
func (c *call) progress(text string) {
	c.r.out(acp.UpdateToolCall(acp.ToolCallId(c.id), acp.WithUpdateContent([]acp.ToolCallContent{acp.ToolContent(acp.TextBlock(text))})))
}

func str(args map[string]any, k string) string {
	s, _ := args[k].(string)
	return s
}

func num(args map[string]any, k string) int {
	switch v := args[k].(type) {
	case float64:
		return int(v)
	case string:
		var n int
		fmt.Sscanf(v, "%d", &n)
		return n
	}
	return 0
}

func boolean(args map[string]any, k string) bool {
	switch v := args[k].(type) {
	case bool:
		return v
	case string:
		return v == "true"
	}
	return false
}

func obj(props map[string]any, required ...string) map[string]any {
	if required == nil {
		required = []string{}
	}
	return map[string]any{"type": "object", "properties": props, "required": required}
}

func prop(typ, desc string) map[string]any { return map[string]any{"type": typ, "description": desc} }

func rel(p, cwd string) string {
	if r, err := filepath.Rel(cwd, p); err == nil && !strings.HasPrefix(r, "..") {
		return r
	}
	return p
}

func pathShow(verb string, kind acp.ToolKind) func(map[string]any, string) (string, acp.ToolKind, []acp.ToolCallLocation) {
	return func(args map[string]any, cwd string) (string, acp.ToolKind, []acp.ToolCallLocation) {
		p := str(args, "path")
		if p == "" {
			return verb, kind, nil
		}
		ap := p
		if !filepath.IsAbs(ap) {
			ap = filepath.Join(cwd, p)
		}
		return verb + " " + rel(ap, cwd), kind, []acp.ToolCallLocation{{Path: ap}}
	}
}

// ---- read ----

const (
	readMaxLines = 2000
	readMaxLine  = 2000      // 一行最多多少字符（minified 的 js 一行几百 KB）
	readMaxBytes = 256 << 10 // 一次最多给模型多少
	outMax       = 30_000    // 命令输出 / 搜索结果最多给模型多少字符（头尾各留一半）
)

var imageExt = map[string]string{".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif", ".webp": "image/webp"}

var readTool = &tool{
	name: "read",
	desc: "Read a file from the local filesystem. Returns the content with line numbers (`<n>\\t<line>`), at most 2000 lines per call — use offset/limit to page through long files. " +
		"Images (png/jpg/gif/webp) are returned as images when the model supports it. Use this instead of `cat`/`head`/`tail` in bash. " +
		"Read a file before editing it.",
	params: obj(map[string]any{
		"path":   prop("string", "Absolute path, or relative to the working directory"),
		"offset": prop("integer", "1-based line number to start reading from (optional)"),
		"limit":  prop("integer", "Maximum number of lines to read (optional, default 2000)"),
	}, "path"),
	readOnly: true,
	show:     pathShow("Read", acp.ToolKindRead),
	run: func(c *call, args map[string]any) result {
		p := c.abs(str(args, "path"))
		st, err := os.Stat(p)
		if err != nil {
			return errResult("%v", err)
		}
		if st.IsDir() {
			return errResult("%s is a directory; use bash `ls` or the glob tool to list it", p)
		}
		if mime, ok := imageExt[strings.ToLower(filepath.Ext(p))]; ok {
			if !c.r.model.seesImages() {
				return errResult("the current model cannot view images")
			}
			if st.Size() > 8<<20 {
				return errResult("image too large (%d bytes)", st.Size())
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return errResult("%v", err)
			}
			return result{text: fmt.Sprintf("Image %s (%d bytes)", p, len(b)), images: []llm.Part{{Type: "image", MIME: mime, Data: base64.StdEncoding.EncodeToString(b)}}}
		}
		f, err := os.Open(p)
		if err != nil {
			return errResult("%v", err)
		}
		defer f.Close()
		b, err := io.ReadAll(io.LimitReader(f, 64<<20))
		if err != nil {
			return errResult("%v", err)
		}
		if bytes.IndexByte(b[:min(len(b), 8000)], 0) >= 0 {
			return errResult("%s looks like a binary file (%d bytes)", p, st.Size())
		}
		lines := strings.Split(string(b), "\n")
		if len(lines) > 0 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
		off := max(num(args, "offset"), 1)
		lim := num(args, "limit")
		if lim <= 0 {
			lim = readMaxLines
		}
		if len(lines) == 0 {
			return result{text: "(empty file)"}
		}
		if off > len(lines) {
			return errResult("offset %d is past the end of the file (%d lines)", off, len(lines))
		}
		var out strings.Builder
		end := min(off-1+lim, len(lines))
		for i := off - 1; i < end; i++ {
			l := lines[i]
			if len(l) > readMaxLine {
				l = truncUTF8(l, readMaxLine) + "… [line truncated]"
			}
			fmt.Fprintf(&out, "%6d\t%s\n", i+1, l)
			if out.Len() > readMaxBytes {
				end = i + 1
				break
			}
		}
		if end < len(lines) {
			fmt.Fprintf(&out, "\n[%d more lines; continue with offset=%d]", len(lines)-end, end+1)
		}
		c.r.s.markRead(p)
		return result{text: out.String(), display: []acp.ToolCallContent{}}
	},
}

func truncUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// ---- write / edit ----

var writeTool = &tool{
	name: "write",
	desc: "Write a file (creates parent directories, overwrites if it exists). Prefer `edit` for changing part of an existing file. " +
		"When overwriting an existing file, read it first.",
	params: obj(map[string]any{
		"path":    prop("string", "Absolute path, or relative to the working directory"),
		"content": prop("string", "The full content to write"),
	}, "path", "content"),
	show: pathShow("Write", acp.ToolKindEdit),
	run: func(c *call, args map[string]any) result {
		p := c.abs(str(args, "path"))
		content := str(args, "content")
		var old *string
		if b, err := os.ReadFile(p); err == nil {
			s := string(b)
			old = &s
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return errResult("%v", err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			return errResult("%v", err)
		}
		c.r.s.markRead(p)
		verb := "Created"
		if old != nil {
			verb = "Overwrote"
		}
		return result{text: fmt.Sprintf("%s %s (%d lines)", verb, p, strings.Count(content, "\n")+1),
			display: []acp.ToolCallContent{{Diff: &acp.ToolCallContentDiff{Path: p, OldText: old, NewText: content, Type: "diff"}}}}
	},
}

var editTool = &tool{
	name: "edit",
	desc: "Replace an exact string in a file. old_string must match the file exactly (including whitespace and indentation) and be unique, " +
		"unless replace_all is true. Include enough surrounding lines to make it unique. Read the file first.",
	params: obj(map[string]any{
		"path":        prop("string", "Absolute path, or relative to the working directory"),
		"old_string":  prop("string", "The exact text to replace"),
		"new_string":  prop("string", "The replacement text"),
		"replace_all": prop("boolean", "Replace every occurrence (default false)"),
	}, "path", "old_string", "new_string"),
	show: pathShow("Edit", acp.ToolKindEdit),
	run: func(c *call, args map[string]any) result {
		p := c.abs(str(args, "path"))
		oldS, newS := str(args, "old_string"), str(args, "new_string")
		b, err := os.ReadFile(p)
		if err != nil {
			return errResult("%v", err)
		}
		src := string(b)
		if oldS == "" {
			return errResult("old_string is empty; use write to create a file")
		}
		if oldS == newS {
			return errResult("old_string and new_string are the same")
		}
		n := strings.Count(src, oldS)
		if n == 0 {
			// 模型常把 read 输出里的行号 / tab 带进来，或者缩进差一点：说清楚，让它重读
			return errResult("old_string not found in %s. It must match exactly, including indentation; do not include the line-number prefix from read output. Read the file again and retry.", p)
		}
		if n > 1 && !boolean(args, "replace_all") {
			return errResult("old_string appears %d times in %s; add more surrounding context to make it unique, or set replace_all", n, p)
		}
		var out string
		if boolean(args, "replace_all") {
			out = strings.ReplaceAll(src, oldS, newS)
		} else {
			out = strings.Replace(src, oldS, newS, 1)
		}
		if err := os.WriteFile(p, []byte(out), 0o644); err != nil {
			return errResult("%v", err)
		}
		return result{text: fmt.Sprintf("Edited %s (%d replacement(s))", p, max(1, boolToInt(boolean(args, "replace_all"))*n)),
			display: []acp.ToolCallContent{{Diff: &acp.ToolCallContentDiff{Path: p, OldText: &src, NewText: out, Type: "diff"}}}}
	},
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ---- bash ----

var bashTool = &tool{
	name: "bash",
	desc: "Run a shell command in the working directory and return its output (stdout and stderr combined) and exit code. " +
		"Each call is a fresh shell: `cd` does not persist, so use absolute paths or `cd dir && cmd`. Default timeout 120s (max 600s). " +
		"Do not use it to read or search files when read/grep/glob can do it. Avoid interactive commands (they get no stdin). " +
		"Give a short description of what the command does.",
	params: obj(map[string]any{
		"command":     prop("string", "The command to run"),
		"description": prop("string", "Short description of what this command does (5-10 words)"),
		"timeout":     prop("integer", "Timeout in seconds (default 120, max 600)"),
		"run_in_background": prop("boolean", "Run in the background and return immediately (dev servers, watchers, long builds). "+
			"You are notified with the output when it finishes; check it with task_output, stop it with task_stop."),
	}, "command"),
	show: func(args map[string]any, cwd string) (string, acp.ToolKind, []acp.ToolCallLocation) {
		cmd := str(args, "command")
		return oneLine(cmd, 200), acp.ToolKindExecute, nil
	},
	run: runBash,
}

func shell() string {
	if s := os.Getenv("SHELL"); s != "" {
		return s
	}
	return "/bin/sh"
}

// proc 是一个跑着的命令。
type proc struct {
	cmd   *exec.Cmd
	buf   *lockedBuf
	done  chan error
	start time.Time
}

func startProc(c *call, command string) (*proc, error) {
	cmd := exec.Command(shell(), "-c", command)
	cmd.Dir = c.cwd()
	cmd.Env = sysenv.ChildEnv(sessionEnvKey + "=" + c.r.s.currentID())
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	p := &proc{cmd: cmd, buf: &lockedBuf{}, done: make(chan error, 1), start: time.Now()}
	cmd.Stdout = p.buf
	cmd.Stderr = p.buf
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go func() { p.done <- cmd.Wait() }()
	return p, nil
}

// kill：整个进程组一起杀（sh -c 起的子进程 —— npm、go test 起的一串 —— 不跟着死的话会一直挂着），等它退出。
func (p *proc) kill() error {
	_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGTERM)
	select {
	case err := <-p.done:
		return err
	case <-time.After(2 * time.Second):
		_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
		return <-p.done
	}
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

func runBash(c *call, args map[string]any) result {
	command := str(args, "command")
	if strings.TrimSpace(command) == "" {
		return errResult("empty command")
	}
	desc := str(args, "description")
	if desc == "" {
		desc = command
	}
	p, err := startProc(c, command)
	if err != nil {
		return errResult("%v", err)
	}
	s := c.r.s
	if boolean(args, "run_in_background") {
		s.setBgOut(c.id, p.buf)
		c.bgBash(p, desc)
		return result{text: fmt.Sprintf("Started in the background (task id %s). You will be notified when it finishes; use task_output to check on it, task_stop to stop it.", c.id),
			display: []acp.ToolCallContent{}}
	}
	timeout := time.Duration(num(args, "timeout")) * time.Second
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	timeout = min(timeout, 600*time.Second)
	ctx, cancel := context.WithTimeout(c.ctx, timeout)
	defer cancel()
	detach, undetach := s.detachable(c.id)
	defer undetach()
	tick := time.NewTicker(400 * time.Millisecond)
	defer tick.Stop()
	var werr error
	killed := ""
loop:
	for {
		select {
		case werr = <-p.done:
			break loop
		case <-tick.C:
			if out, changed := p.buf.snapshot(); changed {
				c.progress(tailStr(out, 16<<10))
			}
		case <-detach:
			// 界面上点了「转到后台」：命令接着跑，这一轮往下走
			s.setBgOut(c.id, p.buf)
			c.bgBash(p, desc)
			out, _ := p.buf.snapshot()
			return result{text: fmt.Sprintf("The user moved this command to the background (task id %s); it is still running. You will be notified when it finishes. Output so far:\n%s", c.id, headTail(out, outMax/2)),
				display: []acp.ToolCallContent{acp.ToolContent(acp.TextBlock(tailStr(out, 64<<10)))}}
		case <-ctx.Done():
			werr = p.kill()
			if c.ctx.Err() != nil {
				killed = "interrupted by the user"
			} else {
				killed = fmt.Sprintf("timed out after %s; for long-running commands use run_in_background", timeout)
			}
			break loop
		}
	}
	out, _ := p.buf.snapshot()
	code := exitCode(werr)
	text := headTail(out, outMax)
	if text == "" {
		text = "(no output)"
	}
	switch {
	case killed != "":
		text += "\n[command " + killed + "]"
	case code != 0:
		text += fmt.Sprintf("\n[exit code %d]", code)
	}
	if d := time.Since(p.start); d > 10*time.Second {
		text += fmt.Sprintf("\n[took %s]", d.Round(time.Second))
	}
	disp := tailStr(out, 64<<10)
	var display []acp.ToolCallContent
	if disp != "" {
		display = []acp.ToolCallContent{acp.ToolContent(acp.TextBlock(disp))}
	} else {
		display = []acp.ToolCallContent{}
	}
	return result{text: text, err: code != 0 || killed != "", display: display}
}

// sessionEnvKey：bash 里能看到自己是哪个会话（`gerdr session current` 拿它问 /api/resolve）。
const sessionEnvKey = "GERDR_NATIVE_SESSION_ID"

type lockedBuf struct {
	mu   sync.Mutex
	b    bytes.Buffer
	seen int
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.b.Len() > 8<<20 { // 输出疯长（死循环打印）时不撑爆内存：只留头尾
		return len(p), nil
	}
	return l.b.Write(p)
}

func (l *lockedBuf) snapshot() (string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	changed := l.b.Len() != l.seen
	l.seen = l.b.Len()
	return l.b.String(), changed
}

func tailStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[len(s)-n:]
	for len(s) > 0 && !utf8.RuneStart(s[0]) {
		s = s[1:]
	}
	return s
}

// headTail：太长的输出留头尾（错误常在最后，命令回显 / 编译的第一条错误常在最前）。
func headTail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	h := truncUTF8(s, n/2)
	t := tailStr(s, n/2)
	return h + fmt.Sprintf("\n\n… [%d characters omitted] …\n\n", len(s)-len(h)-len(t)) + t
}

// ---- grep / glob（ripgrep，没有就退回 grep / find）----

var grepTool = &tool{
	name: "grep",
	desc: "Search file contents with a regular expression (ripgrep syntax). Respects .gitignore. " +
		"output_mode: \"files\" (default) lists matching files; \"content\" shows matching lines with line numbers; \"count\" shows match counts. " +
		"Use this instead of grep/rg in bash.",
	params: obj(map[string]any{
		"pattern":     prop("string", "Regular expression to search for"),
		"path":        prop("string", "File or directory to search (default: working directory)"),
		"glob":        prop("string", "Only search files matching this glob, e.g. \"*.go\" or \"src/**/*.ts\""),
		"output_mode": map[string]any{"type": "string", "enum": []string{"files", "content", "count"}, "description": "What to return (default files)"},
		"ignore_case": prop("boolean", "Case-insensitive search"),
		"context":     prop("integer", "Lines of context around each match (content mode only)"),
	}, "pattern"),
	readOnly: true,
	show: func(args map[string]any, cwd string) (string, acp.ToolKind, []acp.ToolCallLocation) {
		t := "Grep " + str(args, "pattern")
		if p := str(args, "path"); p != "" {
			t += " in " + p
		}
		return t, acp.ToolKindSearch, nil
	},
	run: func(c *call, args map[string]any) result {
		pat := str(args, "pattern")
		dir := c.cwd()
		if p := str(args, "path"); p != "" {
			dir = c.abs(p)
		}
		rg, err := exec.LookPath("rg")
		var argv []string
		if err == nil {
			argv = []string{rg, "--no-heading", "--color=never", "--max-columns=400", "--max-columns-preview"}
			switch str(args, "output_mode") {
			case "content":
				argv = append(argv, "-n")
				if n := num(args, "context"); n > 0 {
					argv = append(argv, fmt.Sprintf("-C%d", n))
				}
			case "count":
				argv = append(argv, "-c")
			default:
				argv = append(argv, "-l")
			}
			if boolean(args, "ignore_case") {
				argv = append(argv, "-i")
			}
			if g := str(args, "glob"); g != "" {
				argv = append(argv, "--glob", g)
			}
			argv = append(argv, "-e", pat, dir)
		} else {
			argv = []string{"grep", "-rE", "--exclude-dir=.git", "--exclude-dir=node_modules"}
			switch str(args, "output_mode") {
			case "content":
				argv = append(argv, "-n")
			case "count":
				argv = append(argv, "-c")
			default:
				argv = append(argv, "-l")
			}
			if boolean(args, "ignore_case") {
				argv = append(argv, "-i")
			}
			if g := str(args, "glob"); g != "" {
				argv = append(argv, "--include="+g)
			}
			argv = append(argv, "-e", pat, dir)
		}
		out, code, err := runCapture(c.ctx, c.cwd(), argv)
		if err != nil {
			return errResult("%v", err)
		}
		if code == 1 && strings.TrimSpace(out) == "" {
			return result{text: "No matches."}
		}
		if code > 1 {
			return errResult("%s", strings.TrimSpace(out))
		}
		out = relLines(out, c.cwd())
		return result{text: headTail(out, outMax)}
	},
}

var globTool = &tool{
	name: "glob",
	desc: "Find files by name with a glob pattern (e.g. \"**/*.go\", \"src/**/*.test.ts\"). Respects .gitignore. Returns paths sorted by modification time (newest first), at most 500.",
	params: obj(map[string]any{
		"pattern": prop("string", "Glob pattern"),
		"path":    prop("string", "Directory to search in (default: working directory)"),
	}, "pattern"),
	readOnly: true,
	show: func(args map[string]any, cwd string) (string, acp.ToolKind, []acp.ToolCallLocation) {
		return "Glob " + str(args, "pattern"), acp.ToolKindSearch, nil
	},
	run: func(c *call, args map[string]any) result {
		dir := c.cwd()
		if p := str(args, "path"); p != "" {
			dir = c.abs(p)
		}
		pat := str(args, "pattern")
		var files []string
		if rg, err := exec.LookPath("rg"); err == nil {
			out, code, err := runCapture(c.ctx, dir, []string{rg, "--files", "--color=never", "--glob", pat})
			if err != nil {
				return errResult("%v", err)
			}
			if code > 1 {
				return errResult("%s", strings.TrimSpace(out))
			}
			for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
				if l != "" {
					files = append(files, filepath.Join(dir, l))
				}
			}
		} else {
			_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				if d.IsDir() && (d.Name() == ".git" || d.Name() == "node_modules") {
					return filepath.SkipDir
				}
				if r, _ := filepath.Rel(dir, p); !d.IsDir() && globMatch(pat, r) {
					files = append(files, p)
				}
				return nil
			})
		}
		if len(files) == 0 {
			return result{text: "No files found."}
		}
		mt := map[string]int64{}
		for _, f := range files {
			if st, err := os.Stat(f); err == nil {
				mt[f] = st.ModTime().UnixNano()
			}
		}
		sort.SliceStable(files, func(i, j int) bool { return mt[files[i]] > mt[files[j]] })
		more := ""
		if len(files) > 500 {
			more = fmt.Sprintf("\n[%d more files not shown; narrow the pattern]", len(files)-500)
			files = files[:500]
		}
		for i, f := range files {
			files[i] = rel(f, c.cwd())
		}
		return result{text: strings.Join(files, "\n") + more}
	},
}

// globMatch：没有 rg 时的简陋版本，** 匹配任意层目录。
func globMatch(pat, p string) bool {
	if !strings.Contains(pat, "/") {
		ok, _ := filepath.Match(pat, filepath.Base(p))
		return ok
	}
	if strings.Contains(pat, "**") {
		pre, suf, _ := strings.Cut(pat, "**")
		suf = strings.TrimPrefix(suf, "/")
		if !strings.HasPrefix(p, pre) {
			return false
		}
		rest := strings.TrimPrefix(p, pre)
		parts := strings.Split(rest, "/")
		for i := range parts {
			if ok, _ := filepath.Match(suf, strings.Join(parts[i:], "/")); ok {
				return true
			}
		}
		return false
	}
	ok, _ := filepath.Match(pat, p)
	return ok
}

func runCapture(ctx context.Context, dir string, argv []string) (string, int, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	var buf lockedBuf
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	out, _ := buf.snapshot()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return out, ee.ExitCode(), nil
	}
	if err != nil {
		return out, -1, err
	}
	return out, 0, nil
}

// relLines：搜索结果里的绝对路径换成相对 cwd 的（省 token）。
func relLines(out, cwd string) string {
	prefix := strings.TrimRight(cwd, "/") + "/"
	return strings.ReplaceAll(out, prefix, "")
}

// ---- todo ----

var todoTool = &tool{
	name: "todo_write",
	desc: "Create or update the task list for the current work (shown to the user as a checklist). Use it for multi-step tasks: " +
		"write all steps up front, keep exactly one in_progress, mark each completed as soon as it is done. Pass the full list every time.",
	params: obj(map[string]any{
		"todos": map[string]any{"type": "array", "items": obj(map[string]any{
			"content": prop("string", "What to do"),
			"status":  map[string]any{"type": "string", "enum": []string{"pending", "in_progress", "completed"}},
		}, "content", "status")},
	}, "todos"),
	readOnly: true,
	hidden:   true,
	show: func(args map[string]any, cwd string) (string, acp.ToolKind, []acp.ToolCallLocation) {
		return "Update todos", acp.ToolKindThink, nil
	},
	run: func(c *call, args map[string]any) result {
		raw, _ := json.Marshal(args["todos"])
		var todos []struct {
			Content string `json:"content"`
			Status  string `json:"status"`
		}
		if err := json.Unmarshal(raw, &todos); err != nil {
			return errResult("todos must be an array of {content, status}")
		}
		var entries []acp.PlanEntry
		for _, t := range todos {
			st := acp.PlanEntryStatusPending
			switch t.Status {
			case "in_progress":
				st = acp.PlanEntryStatusInProgress
			case "completed":
				st = acp.PlanEntryStatusCompleted
			}
			entries = append(entries, acp.PlanEntry{Content: t.Content, Priority: acp.PlanEntryPriorityMedium, Status: st})
		}
		c.r.out(acp.UpdatePlan(entries...))
		return result{text: "Todo list updated.", display: []acp.ToolCallContent{}}
	},
}

// previewDiff：审批卡片上的 diff（还没改，算出改完的样子）。算不出来（找不到 old_string）就不给，执行时再报错。
func previewDiff(cwd, name string, args map[string]any) []acp.ToolCallContent {
	p := str(args, "path")
	if !filepath.IsAbs(p) {
		p = filepath.Join(cwd, p)
	}
	var old *string
	if b, err := os.ReadFile(p); err == nil {
		s := string(b)
		old = &s
	}
	var next string
	switch name {
	case "write":
		next = str(args, "content")
	case "edit":
		if old == nil {
			return nil
		}
		o := str(args, "old_string")
		if o == "" || !strings.Contains(*old, o) {
			return nil
		}
		if boolean(args, "replace_all") {
			next = strings.ReplaceAll(*old, o, str(args, "new_string"))
		} else {
			next = strings.Replace(*old, o, str(args, "new_string"), 1)
		}
	}
	return []acp.ToolCallContent{{Diff: &acp.ToolCallContentDiff{Path: p, OldText: old, NewText: next, Type: "diff"}}}
}
