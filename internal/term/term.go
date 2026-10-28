// Package term：界面里的终端。shell 跑在 gerdr 这边的 PTY 里，界面（xterm.js）经 WebSocket 连上来。
//
// 为什么放在后端常驻、而不是「开一个 pane 起一个 shell、关页面就没」：
//   - 和 tmux 一样能断开再接：刷新页面、App 重开、换到手机上，shell 和里面跑着的东西都还在。
//     gerdr 平时是后台服务（launchd），活得比界面长。
//   - 同一个终端可以同时开在几处（电脑和手机），输出广播给每一个连着的，谁敲都算。
//
// 重连时把最近一段原始输出（ring）重放一遍，xterm 自己重建出屏幕。不在服务端维护屏幕状态（那要一个
// 完整的终端模拟器）：代价是重放跨过了 resize 的那一段可能有点错位，全屏程序（vim、claude）按一下
// ctrl+l 或动一下窗口就重画了。临时用一下的终端，够用。
package term

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ringSize：每个终端留多少最近输出给重连时重放。太大重连慢（远程那条路要走中继），太小 scrollback 回来得少。
const ringSize = 512 << 10

var ErrNotFound = errors.New("终端不在了（gerdr 重启过，或者已经关掉）")

type Info struct {
	ID      string `json:"id"`
	Cwd     string `json:"cwd"`
	Shell   string `json:"shell"`
	Pid     int    `json:"pid"`
	Exited  bool   `json:"exited"`
	Code    int    `json:"code"`
	Created int64  `json:"created"`
}

type Manager struct {
	mu    sync.Mutex
	terms map[string]*Term
	env   func() []string
	log   *slog.Logger
}

// New：env 给出 shell 的环境（gerdr 自己那份去掉不该往下传的，见 sysenv.ChildEnv）。
func New(env func() []string, log *slog.Logger) *Manager {
	return &Manager{terms: map[string]*Term{}, env: env, log: log}
}

type CreateOptions struct {
	Cwd string `json:"cwd"`
	// From：从哪个终端旁边开的。它还活着就用它 shell 的当前目录（cd 过之后分屏，新的跟着到那儿，和 tmux 一样）。
	From string `json:"from"`
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

func (m *Manager) Create(o CreateOptions) (*Term, error) {
	home, _ := os.UserHomeDir()
	cwd := o.Cwd
	if o.From != "" {
		if f := m.Get(o.From); f != nil {
			if d := f.LiveCwd(); d != "" {
				cwd = d
			}
		}
	}
	if cwd == "~" || strings.HasPrefix(cwd, "~/") {
		cwd = filepath.Join(home, cwd[1:])
	}
	if st, err := os.Stat(cwd); cwd == "" || err != nil || !st.IsDir() {
		cwd = home
	}
	if o.Cols == 0 || o.Rows == 0 {
		o.Cols, o.Rows = 80, 24
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/zsh"
	}
	env := m.env()
	// launchd 起的 gerdr 没有 LANG：zsh 会把中文当成一串转义，行编辑也按字节算宽度。
	if !hasEnv(env, "LANG") && !hasEnv(env, "LC_ALL") && !hasEnv(env, "LC_CTYPE") {
		env = append(env, "LANG=en_US.UTF-8")
	}
	env = append(env, "TERM=xterm-256color", "COLORTERM=truecolor", "TERM_PROGRAM=gerdr")
	t := &Term{id: uuid.NewString()[:8], cwd: cwd, shell: shell, created: time.Now(), subs: map[*sub]struct{}{}, done: make(chan struct{})}
	if err := t.start(env, o.Cols, o.Rows); err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.terms[t.id] = t
	m.mu.Unlock()
	m.log.Info("term started", "id", t.id, "cwd", cwd, "pid", t.pid)
	return t, nil
}

func hasEnv(env []string, k string) bool {
	for _, kv := range env {
		if strings.HasPrefix(kv, k+"=") {
			return true
		}
	}
	return false
}

func (m *Manager) Get(id string) *Term {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.terms[id]
}

func (m *Manager) List() []Info {
	m.mu.Lock()
	list := make([]*Term, 0, len(m.terms))
	for _, t := range m.terms {
		list = append(list, t)
	}
	m.mu.Unlock()
	out := make([]Info, 0, len(list))
	for _, t := range list {
		out = append(out, t.Info())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created < out[j].Created })
	return out
}

// Kill：关掉并忘掉它（界面上关 pane 就是这个，和 tmux 关 pane 一样连里面的进程一起结束）。
func (m *Manager) Kill(id string) error {
	m.mu.Lock()
	t := m.terms[id]
	delete(m.terms, id)
	m.mu.Unlock()
	if t == nil {
		return ErrNotFound
	}
	t.kill()
	return nil
}

// Close：gerdr 退出时。不等它们退干净（kill 自己会在后台补 SIGKILL）。
func (m *Manager) Close() {
	m.mu.Lock()
	list := m.terms
	m.terms = map[string]*Term{}
	m.mu.Unlock()
	for _, t := range list {
		t.kill()
	}
}

// ---- 一个终端 ----

type Term struct {
	id      string
	cwd     string
	shell   string
	created time.Time
	pid     int
	tty     *os.File

	mu     sync.Mutex
	ring   []byte
	subs   map[*sub]struct{}
	exited bool
	code   int
	done   chan struct{}
}

type sub struct {
	c    chan []byte
	gone bool
}

func (t *Term) ID() string { return t.id }

func (t *Term) Info() Info {
	t.mu.Lock()
	defer t.mu.Unlock()
	return Info{ID: t.id, Cwd: t.cwd, Shell: filepath.Base(t.shell), Pid: t.pid, Exited: t.exited, Code: t.code, Created: t.created.UnixMilli()}
}

// Done：shell 退出时关上。
func (t *Term) Done() <-chan struct{} { return t.done }

func (t *Term) ExitCode() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.code
}

// Attach：拿到重放用的那一段输出 + 之后的输出。backlog 和订阅在同一把锁里取，中间不会漏、不会重。
// 消费跟不上（慢网络、远程）时 channel 被关掉，客户端重连拿重放，比无限攒着强。
func (t *Term) Attach() (backlog []byte, c <-chan []byte, detach func()) {
	s := &sub{c: make(chan []byte, 256)}
	t.mu.Lock()
	backlog = append([]byte(nil), t.ring...)
	if t.exited {
		close(s.c)
		s.gone = true
	} else {
		t.subs[s] = struct{}{}
	}
	t.mu.Unlock()
	return backlog, s.c, func() {
		t.mu.Lock()
		if !s.gone {
			delete(t.subs, s)
			s.gone = true
			close(s.c)
		}
		t.mu.Unlock()
	}
}

func (t *Term) Write(p []byte) error {
	_, err := t.tty.Write(p)
	return err
}

// pump：读 PTY → 进 ring、发给每个订阅者。
func (t *Term) pump() {
	buf := make([]byte, 32<<10)
	for {
		n, err := t.tty.Read(buf)
		if n > 0 {
			chunk := append([]byte(nil), buf[:n]...)
			t.mu.Lock()
			t.ring = append(t.ring, chunk...)
			if len(t.ring) > ringSize {
				t.ring = trimRing(t.ring, ringSize)
			}
			for s := range t.subs {
				select {
				case s.c <- chunk:
				default:
					delete(t.subs, s)
					s.gone = true
					close(s.c)
				}
			}
			t.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

// trimRing：丢掉前面的，从一个换行之后开始 —— 从半截转义序列 / 半个 UTF-8 字符开始重放，开头会是乱码。
func trimRing(b []byte, max int) []byte {
	cut := len(b) - max*3/4 // 一次多丢一点，别每次输出都搬一遍
	if i := indexByte(b[cut:], '\n'); i >= 0 && i < 4096 {
		cut += i + 1
	}
	return append([]byte(nil), b[cut:]...)
}

func indexByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}

func (t *Term) finish(code int) {
	t.mu.Lock()
	t.exited = true
	t.code = code
	for s := range t.subs {
		s.gone = true
		close(s.c)
	}
	t.subs = map[*sub]struct{}{}
	t.mu.Unlock()
	close(t.done)
}
