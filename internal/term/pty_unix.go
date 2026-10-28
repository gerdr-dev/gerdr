//go:build !windows

package term

import (
	"errors"
	"os/exec"
	"syscall"
	"time"

	"github.com/creack/pty"
)

func (t *Term) start(env []string, cols, rows uint16) error {
	// -l：登录 shell，读 profile（PATH、别名都在那儿；从 launchd 起的 gerdr 自己的环境很干）。
	cmd := exec.Command(t.shell, "-l")
	cmd.Dir = t.cwd
	cmd.Env = env
	// pty.Start 会让 shell 成为新会话的首进程、PTY 做它的控制终端（Setsid + Setctty），
	// 所以 kill 时能按进程组一锅端（shell 里跑着的 vim / dev server 一起走）。
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: cols, Rows: rows})
	if err != nil {
		return err
	}
	t.tty = f
	t.pid = cmd.Process.Pid
	go t.pump()
	go func() {
		err := cmd.Wait()
		code := 0
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		}
		// shell 退了但它起的后台进程还拿着 PTY 时，读那头不会 EOF；关掉它让 pump 收工。
		_ = f.Close()
		t.finish(code)
	}()
	return nil
}

func (t *Term) Resize(cols, rows uint16) error {
	if cols == 0 || rows == 0 {
		return nil
	}
	return pty.Setsize(t.tty, &pty.Winsize{Cols: cols, Rows: rows})
}

// kill：SIGHUP 给整个进程组（关终端窗口时 shell 收到的就是它），2 秒没退再 SIGKILL。
func (t *Term) kill() {
	select {
	case <-t.done:
		return
	default:
	}
	_ = syscall.Kill(-t.pid, syscall.SIGHUP)
	go func() {
		select {
		case <-t.done:
		case <-time.After(2 * time.Second):
			_ = syscall.Kill(-t.pid, syscall.SIGKILL)
		}
	}()
}
