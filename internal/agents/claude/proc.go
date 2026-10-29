package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/gerdr-dev/gerdr/internal/acpx"
	"github.com/gerdr-dev/gerdr/internal/sysenv"
)

// proc 是一个 claude CLI 进程（headless，stream-json 双向）。一个会话一个。
//
// 线路格式没有公开文档，是 Agent SDK 和 CLI 之间的约定；这里照着官方 TS SDK（sdk.d.ts）
// 和抓下来的真实流量写的。要点：
//   - 我们写进 stdin 的：`user` 消息（插话时带 priority）、`control_request`（initialize /
//     interrupt / set_permission_mode / set_model）、`control_response`（回答它问的 can_use_tool）；
//   - 它写到 stdout 的：stream_event（逐 token）、assistant / user（整条消息）、result、
//     system（init / session_state_changed …）、control_request / control_response。
//   - 一轮真正结束的信号是 `system/session_state_changed: idle`，**不是** result：
//     插话会把正在跑的那一次生成截断、先出一个 result，再把插进来的话当成第二次接着跑，
//     第二个 result 之后才 idle。
type proc struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	wmu   sync.Mutex
	log   *slog.Logger

	mu      sync.Mutex
	pending map[string]chan ctlResult
	done    chan struct{}
	exitErr error
	stderr  *ringBuf
}

type ctlResult struct {
	resp json.RawMessage
	err  error
}

type spawnOpts struct {
	bin       string
	cwd       string
	model     string
	mode      string
	effort    string
	sessionID string // --session-id：新会话或分叉出来的新会话用这个 id
	resume    string // --resume
	resumeAt  string // --resume-session-at（配 --fork-session）
	fork      bool
	extra     []string // skill / MCP 那几个（ext.ClaudeArgs）+ 变体自己配的参数
	env       []string // 变体配的环境变量，盖在继承来的上面
}

func spawn(o spawnOpts, log *slog.Logger, onLine func(map[string]json.RawMessage, []byte)) (*proc, error) {
	args := []string{
		"--output-format", "stream-json", "--verbose",
		"--input-format", "stream-json",
		"--permission-prompt-tool", "stdio",
		"--include-partial-messages",
		// 告诉它 gerdr 的界面能直接显示图片 / 视频 / 音频（acpx.MediaHint）
		"--append-system-prompt", acpx.MediaHint,
	}
	if o.model != "" && o.model != "default" {
		args = append(args, "--model", o.model)
	}
	if o.mode != "" {
		args = append(args, "--permission-mode", o.mode)
	}
	if o.effort != "" {
		args = append(args, "--effort", o.effort)
	}
	if o.resume != "" {
		args = append(args, "--resume", o.resume)
		if o.resumeAt != "" {
			args = append(args, "--resume-session-at", o.resumeAt)
		}
		if o.fork {
			args = append(args, "--fork-session")
		}
	}
	if o.sessionID != "" {
		args = append(args, "--session-id", o.sessionID)
	}
	args = append(args, o.extra...)
	cmd := exec.Command(o.bin, args...)
	cmd.Dir = o.cwd
	// CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS：不设的话 CLI 不报 session_state_changed，
	// 只剩 result 能当「这一轮完了」的信号 —— 而插话会把正在跑的那次生成截断、先出一个
	// result，于是插话之后这一轮被提前判成结束（实测：插进去的话没人等，回复全丢）。
	// 官方 SDK 起 CLI 时也带着它。
	cmd.Env = append(sysenv.ChildEnv("CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS=1"), o.env...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	p := &proc{cmd: cmd, stdin: stdin, log: log, pending: map[string]chan ctlResult{}, done: make(chan struct{}), stderr: &ringBuf{max: 8 << 10}}
	cmd.Stderr = p.stderr
	log.Info("spawn claude", "args", strings.Join(args, " "), "cwd", o.cwd)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start claude: %w", err)
	}
	go func() {
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 1<<20), 256<<20)
		for sc.Scan() {
			line := sc.Bytes()
			if len(line) == 0 {
				continue
			}
			var m map[string]json.RawMessage
			if err := json.Unmarshal(line, &m); err != nil {
				log.Debug("claude: non-json line", "line", truncate(string(line), 200))
				continue
			}
			if str(m["type"]) == "control_response" {
				p.deliver(m["response"])
				continue
			}
			cp := make([]byte, len(line))
			copy(cp, line)
			onLine(m, cp)
		}
		err := cmd.Wait()
		p.mu.Lock()
		if err == nil {
			err = errors.New("claude exited")
		}
		if tail := strings.TrimSpace(p.stderr.String()); tail != "" {
			err = fmt.Errorf("%w: %s", err, truncate(tail, 1500))
		}
		p.exitErr = err
		pend := p.pending
		p.pending = map[string]chan ctlResult{}
		p.mu.Unlock()
		for _, ch := range pend {
			ch <- ctlResult{err: err}
		}
		close(p.done)
	}()
	return p, nil
}

func (p *proc) alive() bool {
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}

func (p *proc) err() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.exitErr
}

func (p *proc) send(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	p.wmu.Lock()
	defer p.wmu.Unlock()
	if !p.alive() {
		return errors.New("claude process is not running")
	}
	_, err = p.stdin.Write(b)
	return err
}

// control 发一个 control_request 并等它的 control_response。
func (p *proc) control(ctx context.Context, req map[string]any) (json.RawMessage, error) {
	id := uuid.NewString()
	ch := make(chan ctlResult, 1)
	p.mu.Lock()
	if p.exitErr != nil {
		p.mu.Unlock()
		return nil, p.exitErr
	}
	p.pending[id] = ch
	p.mu.Unlock()
	if err := p.send(map[string]any{"type": "control_request", "request_id": id, "request": req}); err != nil {
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
		return nil, err
	}
	select {
	case r := <-ch:
		return r.resp, r.err
	case <-ctx.Done():
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
		return nil, ctx.Err()
	}
}

func (p *proc) deliver(raw json.RawMessage) {
	var r struct {
		Subtype   string          `json:"subtype"`
		RequestID string          `json:"request_id"`
		Response  json.RawMessage `json:"response"`
		Error     string          `json:"error"`
	}
	_ = json.Unmarshal(raw, &r)
	p.mu.Lock()
	ch := p.pending[r.RequestID]
	delete(p.pending, r.RequestID)
	p.mu.Unlock()
	if ch == nil {
		return
	}
	if r.Subtype == "error" {
		ch <- ctlResult{err: errors.New(r.Error)}
		return
	}
	ch <- ctlResult{resp: r.Response}
}

// respond 回答它发来的 control_request。
func (p *proc) respond(reqID string, resp any, errMsg string) {
	var body map[string]any
	if errMsg != "" {
		body = map[string]any{"subtype": "error", "request_id": reqID, "error": errMsg}
	} else {
		body = map[string]any{"subtype": "success", "request_id": reqID, "response": resp}
	}
	if err := p.send(map[string]any{"type": "control_response", "response": body}); err != nil {
		p.log.Debug("claude: respond failed", "err", err)
	}
}

// stop 先关 stdin 让它自己收尾（会话记录写完整），等不到再杀。
func (p *proc) stop() {
	_ = p.stdin.Close()
	select {
	case <-p.done:
	case <-time.After(3 * time.Second):
		if p.cmd.Process != nil {
			_ = p.cmd.Process.Kill()
		}
		<-p.done
	}
}

type ringBuf struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func (r *ringBuf) Write(b []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf = append(r.buf, b...)
	if len(r.buf) > r.max {
		r.buf = r.buf[len(r.buf)-r.max:]
	}
	return len(b), nil
}

func (r *ringBuf) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return string(r.buf)
}

func str(raw json.RawMessage) string {
	var s string
	_ = json.Unmarshal(raw, &s)
	return s
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
