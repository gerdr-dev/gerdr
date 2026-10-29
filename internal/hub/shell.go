package hub

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/coder/acp-go-sdk"
	"github.com/google/uuid"

	"github.com/gerdr-dev/gerdr/internal/sysenv"
)

// 输入框里的 `!命令`（照 Claude Code 的 bash 模式）：gerdr 自己在会话目录里跑，不经过 agent —— 不起一轮、不花 token。
// stream-json / app-server 都没有这个口（Claude Code 的 `!` 是 TUI 输入框自己做的），所以只能在这边做。
//
// 日志里记 shell / shell_out / shell_done 三种，界面画成一张命令卡片。输出攒着，下一条发给 agent 的消息前面
// 带上 <bash-input>…</bash-input><bash-stdout>…</bash-stdout>（Claude Code 自己记 `!` 的格式，模型认得），
// agent 就知道人跑了什么、看到了什么。claude / codex / native 一样：都只是消息里多一段文字。
//
// 「哪些还没带给 agent」从日志里推（最后一条用户消息之后跑完的），不另存状态：重启、撤回、回退之后自然对得上 ——
// 撤回 / 回退掉的那条消息不在日志里了，它带着的命令又算回「还没带」。
const (
	EvShell     = "shell"      // MessageID = 这次运行的 id，Text = 命令
	EvShellOut  = "shell_out"  // Text = 一段输出（stdout、stderr 混在一起，按出现的先后）
	EvShellDone = "shell_done" // Exit = 退出码；Level = stopped（人点了停止）/ lost（gerdr 重启时还在跑）
)

const (
	shellLogMax = 256 << 10 // 日志里最多记这么多输出（给界面看），再多只留一句提示
	shellCtxMax = 30000     // 带给 agent 的输出最多这么多字（照 Claude Code 的 bash 输出上限），超了留头尾
	shellTick   = 200 * time.Millisecond
)

// shellPrefix：带给 agent 的那段以它开头。界面、标题、撤回放回输入框都靠它认出来跳过。
const shellPrefix = "<bash-input>"

func isShellBlock(raw json.RawMessage) bool {
	var b struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	return json.Unmarshal(raw, &b) == nil && b.Type == "text" && strings.HasPrefix(b.Text, shellPrefix)
}

// shellOut：命令的输出先攒在这儿，定时成段写进日志（一个字节一条日志的话流式命令能刷出几万条）。
type shellOut struct {
	mu      sync.Mutex
	pending []byte
	total   int
	cut     bool
}

func (o *shellOut) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.cut {
		return len(p), nil
	}
	if room := shellLogMax - o.total; len(p) > room {
		o.pending = append(o.pending, p[:room]...)
		o.total = shellLogMax
		o.cut = true
		return len(p), nil
	}
	o.pending = append(o.pending, p...)
	o.total += len(p)
	return len(p), nil
}

// take：攒着的那段。end=false 时末尾半个 UTF-8 字符留到下一段（不然中文被切成两半，两段各自乱码）。
func (o *shellOut) take(end bool) string {
	o.mu.Lock()
	defer o.mu.Unlock()
	b := o.pending
	if !end {
		for i := len(b) - 1; i >= 0 && i >= len(b)-utf8.UTFMax; i-- {
			if utf8.RuneStart(b[i]) {
				if !utf8.FullRune(b[i:]) {
					o.pending = append([]byte(nil), b[i:]...)
					b = b[:i]
				} else {
					o.pending = nil
				}
				return string(b)
			}
		}
	}
	o.pending = nil
	return strings.ToValidUTF8(string(b), "�")
}

type shellRun struct {
	cmd     *exec.Cmd
	exited  chan struct{} // Wait 返回了
	stopped bool
}

func userShell() string {
	if s := os.Getenv("SHELL"); s != "" {
		return s
	}
	return "/bin/zsh"
}

// Shell 在会话目录里跑一条命令，马上返回这次运行的 id；输出和结束都进日志。
// clientID 和 Send 的一样：client 自己生成的 UUID，网络不好重发同一条时不会跑两遍。
func (s *Session) Shell(command, clientID string) (string, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return "", errors.New("命令是空的")
	}
	id := uuid.NewString()
	if _, err := uuid.Parse(clientID); err == nil {
		id = clientID
	}
	// 登录 shell（-l）：PATH 在 profile 里，launchd 起的 gerdr 自己的环境很干（和终端 pane 一样）。
	// 没有 stdin：要人交互输入的命令（密码、y/n）会卡住，人点停止；浏览器登录这类（gcloud auth login）照样能用。
	cmd := exec.Command(userShell(), "-l", "-c", command)
	cmd.Env = sysenv.ChildEnv()
	// 自己一个进程组：停止时连它起的一串子进程一起杀（npm、go test 起的那些不跟着死的话会一直挂着）
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	out := &shellOut{}
	cmd.Stdout, cmd.Stderr = out, out
	// 命令自己退了、它丢到后台的进程还拿着输出管道时（`dev-server &`），Wait 会一直等那个管道 —— 最多再等 2 秒就不管了
	cmd.WaitDelay = 2 * time.Second

	s.mu.Lock()
	if s.shells[id] != nil {
		s.mu.Unlock()
		return id, nil
	}
	for _, e := range s.events {
		if e.Kind == EvShell && e.MessageID == id {
			s.mu.Unlock()
			return id, nil
		}
	}
	cmd.Dir = s.rec.Cwd
	if err := cmd.Start(); err != nil {
		s.mu.Unlock()
		return "", err
	}
	if s.shells == nil {
		s.shells = map[string]*shellRun{}
	}
	run := &shellRun{cmd: cmd, exited: make(chan struct{})}
	s.shells[id] = run
	s.mu.Unlock()

	s.emit(&Event{Kind: EvShell, MessageID: id, Text: command})
	done := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		close(run.exited)
		done <- err
	}()
	go func() {
		tick := time.NewTicker(shellTick)
		defer tick.Stop()
		var err error
	loop:
		for {
			select {
			case err = <-done:
				break loop
			case <-tick.C:
				if t := out.take(false); t != "" {
					s.emit(&Event{Kind: EvShellOut, MessageID: id, Text: t})
				}
			}
		}
		t := out.take(true)
		if out.cut {
			t += fmt.Sprintf("\n…（输出超过 %d KB，后面的没记下来）\n", shellLogMax>>10)
		}
		if t != "" {
			s.emit(&Event{Kind: EvShellOut, MessageID: id, Text: t})
		}
		code := cmd.ProcessState.ExitCode()
		ev := &Event{Kind: EvShellDone, MessageID: id, Exit: &code}
		s.mu.Lock()
		if run.stopped {
			ev.Level = "stopped"
		}
		delete(s.shells, id)
		s.mu.Unlock()
		if err != nil && code == 0 {
			ev.Error = err.Error()
		}
		s.emit(ev)
		s.flush(true)
	}()
	return id, nil
}

// StopShell：停掉一条还在跑的命令（SIGTERM 给整个进程组，2 秒没退再 SIGKILL）。已经跑完的不算错。
func (s *Session) StopShell(id string) error {
	s.mu.Lock()
	run := s.shells[id]
	if run != nil {
		run.stopped = true
	}
	s.mu.Unlock()
	if run == nil {
		return nil
	}
	run.kill()
	return nil
}

func (r *shellRun) kill() {
	pid := r.cmd.Process.Pid
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	go func() {
		select {
		case <-r.exited:
		case <-time.After(2 * time.Second):
			_ = syscall.Kill(-pid, syscall.SIGKILL)
		}
	}()
}

// stopShells：会话关掉时把还在跑的命令都停了（它们在自己的进程组里，gerdr 不管的话没人看得见地一直跑）。
func (s *Session) stopShells() {
	s.mu.Lock()
	var runs []*shellRun
	for _, r := range s.shells {
		r.stopped = true
		runs = append(runs, r)
	}
	s.mu.Unlock()
	for _, r := range runs {
		r.kill()
	}
}

// pendingShellLocked：最后一条用户消息之后跑完的命令，拼成带给 agent 的文字块（一条命令一块）。
// 还在跑的不带（等它跑完，跟着再下一条走）。
func (s *Session) pendingShellLocked() []acp.ContentBlock {
	last := -1
	for i := len(s.events) - 1; i >= 0; i-- {
		if s.events[i].Kind == EvUser {
			last = i
			break
		}
	}
	var out []acp.ContentBlock
	for i := last + 1; i < len(s.events); i++ {
		d := s.events[i]
		if d.Kind != EvShellDone {
			continue
		}
		var cmd string
		var buf strings.Builder
		for _, e := range s.events {
			if e.MessageID != d.MessageID {
				continue
			}
			switch e.Kind {
			case EvShell:
				cmd = e.Text
			case EvShellOut:
				buf.WriteString(e.Text)
			}
		}
		out = append(out, acp.TextBlock(shellContext(cmd, buf.String(), d)))
	}
	return out
}

// shellContext：一条命令带给 agent 的样子（Claude Code 记 `!` 的格式）。stdout / stderr 跑的时候没分开（要按先后显示），
// 全在 stdout 里；stderr 只交代命令是怎么结束的。末尾的换行不能省：claude 记录里一条消息的几块文字是直接拼起来的，
// 和 hub 这边按换行拼的比对（userTextSet）要对得上，不然以为别处接着聊过。
func shellContext(cmd, output string, done *Event) string {
	output = strings.TrimRight(output, "\n")
	if r := []rune(output); len(r) > shellCtxMax {
		head, tail := r[:shellCtxMax/3], r[len(r)-shellCtxMax*2/3:]
		output = string(head) + fmt.Sprintf("\n…（中间省略 %d 字）…\n", len(r)-len(head)-len(tail)) + string(tail)
	}
	var note string
	switch {
	case done.Level == "stopped":
		note = "Stopped by the user before it finished."
	case done.Level == "lost":
		note = "Interrupted: gerdr restarted while the command was running."
	case done.Error != "":
		note = done.Error
	case done.Exit != nil && *done.Exit != 0:
		note = fmt.Sprintf("Exit code %d", *done.Exit)
	}
	return fmt.Sprintf("<bash-input>%s</bash-input>\n<bash-stdout>%s</bash-stdout><bash-stderr>%s</bash-stderr>\n", cmd, output, note)
}

// withShell：发给 agent 的内容前面接上还没带过去的命令输出。
func withShell(pending, content []acp.ContentBlock) []acp.ContentBlock {
	if len(pending) == 0 {
		return content
	}
	return append(append([]acp.ContentBlock(nil), pending...), content...)
}
